package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Manifest：service.yaml（第一方）/ plugin.yaml（第三方，超集）的类型化表示。
// 契约见 docs/architecture-v2.md §3 + 附录 H.1。
type Manifest struct {
	SpecVersion string `yaml:"manifest"` // 权限声明规范版本（specs/003）；空 = v1
	ID          string `yaml:"id"`
	Version     string `yaml:"version"`
	Lang        string `yaml:"lang"`
	Trust       string `yaml:"trust"` // ""/first-party | third-party
	Source  struct {
		Type   string `yaml:"type"` // build（默认）| image
		Image  string `yaml:"image"`
		Digest string `yaml:"digest"`
	} `yaml:"source"`
	Mount struct {
		Path         string   `yaml:"path"`
		StripPath    bool     `yaml:"strip_path"`
		PublicRoutes []string `yaml:"public_routes"`
		AdminRoutes  []string `yaml:"admin_routes"`
	} `yaml:"mount"`
	Auth struct {
		Required            bool     `yaml:"required"`
		AcceptServiceTokens []string `yaml:"accept_service_tokens"`
	} `yaml:"auth"`
	Permissions struct {
		NeedsIdentity bool     `yaml:"needs_identity"`
		Calls         []string `yaml:"calls"`
		Egress        []string `yaml:"egress"`
	} `yaml:"permissions"`
	Limits struct {
		RatePerMinute int `yaml:"rate_per_minute"`
	} `yaml:"limits"`
	Resources struct {
		Memory string `yaml:"memory"`
		Cpus   string `yaml:"cpus"`
	} `yaml:"resources"`
	Runtime struct {
		Port         int      `yaml:"port"`
		Health       string   `yaml:"health"`
		DrainSeconds int      `yaml:"drain_seconds"` // SIGTERM 排空上限，默认 25（specs/005）
		Env          []string `yaml:"env"`
	} `yaml:"runtime"`
	Data struct {
		Database string `yaml:"database"`
	} `yaml:"data"`
	Roles struct { // ② 我定义什么角色给用户（specs/003；语义永远在服务代码）
		Vocabulary []NamedDecl `yaml:"vocabulary"`
		Bootstrap  string      `yaml:"bootstrap"` // 仅允许 platform-admin=<role>
	} `yaml:"roles"`
	Exposes struct { // ③ 我暴露什么 scope 给其它服务（calls 的校验目标）
		Scopes []NamedDecl `yaml:"scopes"`
	} `yaml:"exposes"`
	AdminUI struct { // 可嵌入的管理后台入口（specs/006）
		Path  string `yaml:"path"`  // 相对 mount.path，如 /console
		Embed bool   `yaml:"embed"` // 允许被平台壳 iframe 嵌入
		Title string `yaml:"title"` // 壳里显示的名字
		Icon  string `yaml:"icon"`  // 可选图标 key
	} `yaml:"admin_ui"`
	Test struct {
		Command string `yaml:"command"`
	} `yaml:"test"`

	Dir string `yaml:"-"`
}

// NamedDecl 角色/scope 声明：name 给机器，desc 给通用 UI 与市场弹窗渲染。
type NamedDecl struct {
	Name string `yaml:"name"`
	Desc string `yaml:"desc"`
}

func (m Manifest) IsThirdParty() bool { return m.Trust == "third-party" }

// AdminURL 返回可嵌入后台的网关绝对路径（mount.path + admin_ui.path），无则空。
func (m Manifest) AdminURL() string {
	if m.AdminUI.Path == "" {
		return ""
	}
	return strings.TrimRight(m.Mount.Path, "/") + "/" + strings.TrimLeft(m.AdminUI.Path, "/")
}

// IsEnabled 以 .disabled 标记文件为准（pctl enable/disable 管理）。
func (m Manifest) IsEnabled() bool {
	_, err := os.Stat(filepath.Join(m.Dir, ".disabled"))
	return err != nil
}

func (m Manifest) NetworkName() string {
	if m.IsThirdParty() {
		return "net-plugin-" + strings.TrimPrefix(m.ID, "svc-plugin-")
	}
	return "core-net"
}

var reservedPrefixes = []string{"/auth", "/platform", "/internal", "/docs"}

func loadManifests(root string) ([]Manifest, error) {
	var files []string
	for _, name := range []string{"service.yaml", "plugin.yaml"} {
		matched, err := filepath.Glob(filepath.Join(root, "services", "*", name))
		if err != nil {
			return nil, err
		}
		files = append(files, matched...)
	}
	sort.Strings(files)
	manifests := make([]Manifest, 0, len(files))
	for _, f := range files {
		m, err := loadManifestFile(f)
		if err != nil {
			return nil, err
		}
		manifests = append(manifests, m)
	}
	return manifests, nil
}

func loadManifestFile(path string) (Manifest, error) {
	var m Manifest
	raw, err := os.ReadFile(path)
	if err != nil {
		return m, fmt.Errorf("read %s: %w", path, err)
	}
	if err := yaml.Unmarshal(raw, &m); err != nil {
		return m, fmt.Errorf("parse %s: %w", path, err)
	}
	m.Dir = filepath.Dir(path)
	return m, nil
}

func validateManifests(manifests []Manifest) error {
	var problems []string
	seenID := map[string]bool{}
	seenPath := map[string]string{}

	for _, m := range manifests {
		dirName := filepath.Base(m.Dir)
		switch {
		case m.ID == "":
			problems = append(problems, fmt.Sprintf("%s: id 缺失", m.Dir))
		case m.ID != dirName:
			problems = append(problems, fmt.Sprintf("%s: id(%q) 必须与目录名一致", m.Dir, m.ID))
		case seenID[m.ID]:
			problems = append(problems, fmt.Sprintf("%s: id %q 重复", m.Dir, m.ID))
		}
		seenID[m.ID] = true

		p := m.Mount.Path
		switch {
		case !strings.HasPrefix(p, "/api/"):
			problems = append(problems, fmt.Sprintf("%s: mount.path %q 必须位于 /api/ 命名空间", m.ID, p))
		default:
			for _, r := range reservedPrefixes {
				if strings.HasPrefix(p, r) {
					problems = append(problems, fmt.Sprintf("%s: mount.path %q 占用保留段 %s", m.ID, p, r))
				}
			}
			for owner, existing := range seenPath {
				if strings.HasPrefix(p+"/", existing+"/") || strings.HasPrefix(existing+"/", p+"/") {
					problems = append(problems, fmt.Sprintf("%s: mount.path %q 与 %s 的 %q 冲突", m.ID, p, owner, existing))
				}
			}
			seenPath[m.ID] = p
		}

		if m.Runtime.Port <= 0 {
			problems = append(problems, fmt.Sprintf("%s: runtime.port 缺失", m.ID))
		}
		if m.Data.Database != "" && !strings.HasPrefix(m.Data.Database, "pf_") {
			problems = append(problems, fmt.Sprintf("%s: data.database %q 必须以 pf_ 前缀命名", m.ID, m.Data.Database))
		}
		if m.IsThirdParty() {
			if m.Source.Image == "" {
				problems = append(problems, fmt.Sprintf("%s: trust=third-party 必须提供 source.image", m.ID))
			}
			if m.Source.Digest == "" {
				fmt.Printf("⚠️  %s: 未提供 source.digest（生产环境必须按 digest 锁定）\n", m.ID)
			}
		}
	}
	problems = append(problems, validatePermissionDecls(manifests)...)
	if len(problems) > 0 {
		return errors.New("清单校验失败:\n  - " + strings.Join(problems, "\n  - "))
	}
	return nil
}

func publicPrefixes(m Manifest) []string {
	seen := map[string]bool{}
	var out []string
	for _, r := range m.Mount.PublicRoutes {
		fields := strings.Fields(r)
		p := fields[len(fields)-1]
		p = strings.TrimSuffix(p, "/*")
		p = strings.TrimSuffix(p, "/")
		full := m.Mount.Path + p
		if !seen[full] {
			seen[full] = true
			out = append(out, full)
		}
	}
	return out
}
