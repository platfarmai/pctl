package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
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
	Source      struct {
		Type   string `yaml:"type"` // build（默认）| image
		Image  string `yaml:"image"`
		Digest string `yaml:"digest"`
	} `yaml:"source"`
	Mount struct {
		Path         string   `yaml:"path"`
		StripPath    bool     `yaml:"strip_path"`
		PublicRoutes []string `yaml:"public_routes"`
		AdminRoutes  []string `yaml:"admin_routes"`
		// Website 根站点插件（企业官网等）：mount.path 必须为 "/"，全站公开、免认证，
		// 网关根路由改指该服务（替代 auth 的平台索引）。每实例至多一个。
		Website bool `yaml:"website"`
		// BrowserPages 仅根站点可用：浏览器（Accept 含 text/html）访问这些平台保留路径时
		// 交给站点渲染成页面；程序调用（Accept 不含 text/html）仍由 auth 返回 JSON。
		// 可选值见 websiteBrowserPages。不声明则这些路径只有 JSON。
		BrowserPages []string `yaml:"browser_pages"`
	} `yaml:"mount"`
	Auth struct {
		Required            bool     `yaml:"required"`
		AcceptServiceTokens []string `yaml:"accept_service_tokens"`
	} `yaml:"auth"`
	// Crypto 响应加密（附录 F）：网关对匹配响应做 AES-GCM，默认关。
	Crypto struct {
		EncryptResponse bool     `yaml:"encrypt_response"`
		Exempt          []string `yaml:"exempt"`
	} `yaml:"crypto"`
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
		Port   int    `yaml:"port"`
		Health string `yaml:"health"`
		// Healthcheck 探活方式：""/pid（默认，任何镜像可用）| http（需镜像自带 wget）| none
		Healthcheck  string   `yaml:"healthcheck"`
		DrainSeconds int      `yaml:"drain_seconds"` // SIGTERM 优雅上限，默认 25（specs/005）
		Env          []string `yaml:"env"`
	} `yaml:"runtime"`
	Data struct {
		Database    string `yaml:"database"`
		TablePrefix string `yaml:"table_prefix"` // 可选表前缀（specs/007）；空=不加，否则须 ^[a-z][a-z0-9_]*_$
	} `yaml:"data"`
	Roles struct { // ② 我定义什么角色给用户（specs/003；语义永远在服务代码）
		Vocabulary []NamedDecl `yaml:"vocabulary"`
		Bootstrap  string      `yaml:"bootstrap"` // 仅允许 platform-admin=<role>
	} `yaml:"roles"`
	Exposes struct { // ③ 我暴露什么 scope 给其它服务（calls 的校验目标）
		Scopes []NamedDecl `yaml:"scopes"`
	} `yaml:"exposes"`
	OpenAPI []OpenRoute `yaml:"open_api"` // 开放平台路由→scope 映射（specs/009）
		Docs struct { // 可选：服务自己的 OpenAPI 文档地址，聚合进网关 /docs
			OpenAPI string `yaml:"openapi"`
		} `yaml:"docs"`
		AdminUI struct { // 可嵌入的管理后台入口（specs/006）
		Path  string `yaml:"path"`  // 相对 mount.path，如 /console
		Embed bool   `yaml:"embed"` // 允许被平台壳 iframe 嵌入
		Title string `yaml:"title"` // 壳里显示的名字
		Icon  string `yaml:"icon"`  // 可选图标 key
	} `yaml:"admin_ui"`
	Test struct {
		Command string `yaml:"command"`
		Extra   string `yaml:"extra"` // 业务自有测试命令，check --e2e 在三件套之后执行
	} `yaml:"test"`

	Dir  string `yaml:"-"` // 清单所在目录
	Root string `yaml:"-"` // 运行目录，用于把 Dir 算成相对的构建上下文
}

// NamedDecl 角色/scope 声明：name 给机器，desc 给通用 UI 与市场弹窗渲染。
type NamedDecl struct {
	Name string `yaml:"name"`
	Desc string `yaml:"desc"`
}

// OpenRoute 开放平台路由声明（specs/009）：route 如 "GET /api/demo/data"，scope 须在 exposes.scopes。
type OpenRoute struct {
	Route string `yaml:"route"`
	Scope string `yaml:"scope"`
}

// Method 返回 route 的 HTTP 方法（默认 GET）。
func (o OpenRoute) Method() string {
	f := strings.Fields(o.Route)
	if len(f) == 2 {
		return strings.ToUpper(f[0])
	}
	return "GET"
}

// Path 返回 route 的路径部分。
func (o OpenRoute) Path() string {
	f := strings.Fields(o.Route)
	return f[len(f)-1]
}

// KongPath 返回写进 Kong 路由 paths 的值。
// 不含通配符的路径原样返回（Kong 按前缀匹配）；含 `*` 段的路径转成带锚点的正则路径
// （Kong 3.x 以 `~` 开头表示正则），每个 `*` 只匹配一段：
//
//	/api/pay/v1/orders/*        → ~/api/pay/v1/orders/[^/]+$
//	/api/pay/v1/orders/*/close  → ~/api/pay/v1/orders/[^/]+/close$
//
// Kong 不认识 `*`：原样写进 paths 只会被当成字面前缀，永远匹配不到真实请求，
// 请求会落到 -protected 路由，scope 校验与按 app 限流都不生效。
// 结尾加 `$` 让同一前缀下的多条通配路由互不重叠（正则路由之间没有“最长匹配”）。
func (o OpenRoute) KongPath() string {
	p := o.Path()
	if !strings.Contains(p, "*") {
		return p
	}
	segs := strings.Split(p, "/")
	for i, s := range segs {
		if s == "*" {
			segs[i] = "[^/]+"
		} else {
			segs[i] = regexp.QuoteMeta(s)
		}
	}
	return "~" + strings.Join(segs, "/") + "$"
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

// websiteBrowserPages 根站点可以接管浏览器展示的平台路径（mount.browser_pages）。
// 这些路径在 auth 上只有 JSON，浏览器直接打开是一段原始 JSON。
var websiteBrowserPages = []string{"/docs", "/platform/info"}

// servicesDirs 返回要扫描清单的目录列表。
// 镜像部署（compose.release.yml）的运行目录没有 services/，插件源码常放在别处（如 _src/services），
// 此时 sync 会扫不到任何清单，生成出只含 auth 的 kong.yml。
// 用 PF_SERVICES_DIR 指定实际位置（多个用 os.PathListSeparator 分隔），相对路径相对 root 解析。
func servicesDirs(root string) []string {
	raw := strings.TrimSpace(envFromRoot(root, "PF_SERVICES_DIR"))
	if raw == "" {
		return []string{filepath.Join(root, "services")}
	}
	var out []string
	for _, dir := range strings.Split(raw, string(os.PathListSeparator)) {
		dir = strings.TrimSpace(dir)
		if dir == "" {
			continue
		}
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(root, dir)
		}
		out = append(out, dir)
	}
	if len(out) == 0 {
		return []string{filepath.Join(root, "services")}
	}
	return out
}

func loadManifests(root string) ([]Manifest, error) {
	var files []string
	for _, dir := range servicesDirs(root) {
		for _, name := range []string{"service.yaml", "plugin.yaml"} {
			matched, err := filepath.Glob(filepath.Join(dir, "*", name))
			if err != nil {
				return nil, err
			}
			files = append(files, matched...)
		}
	}
	sort.Strings(files)
	manifests := make([]Manifest, 0, len(files))
	for _, f := range files {
		m, err := loadManifestFile(f)
		if err != nil {
			return nil, err
		}
		m.Root = root
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
	websiteOwner := ""

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
		case m.Mount.Website:
			// 根站点插件（企业官网）：独占 "/"，全站公开，网关不挂 JWT。
			if p != "/" {
				problems = append(problems, fmt.Sprintf("%s: mount.website=true 时 mount.path 必须为 \"/\"（当前 %q）", m.ID, p))
			}
			if websiteOwner != "" {
				problems = append(problems, fmt.Sprintf("%s: 根站点插件重复（%s 已占用 /）", m.ID, websiteOwner))
			}
			websiteOwner = m.ID
			if m.Auth.Required {
				problems = append(problems, fmt.Sprintf("%s: 根站点插件必须 auth.required=false（全站公开）", m.ID))
			}
			if m.IsThirdParty() {
				problems = append(problems, fmt.Sprintf("%s: 根站点插件必须为 first-party（占据根路由）", m.ID))
			}
			if len(m.Mount.PublicRoutes)+len(m.Mount.AdminRoutes)+len(m.OpenAPI) > 0 {
				problems = append(problems, fmt.Sprintf("%s: 根站点插件不支持 public_routes/admin_routes/open_api", m.ID))
			}
			seenPage := map[string]bool{}
			for _, page := range m.Mount.BrowserPages {
				switch {
				case !slices.Contains(websiteBrowserPages, page):
					problems = append(problems, fmt.Sprintf("%s: mount.browser_pages 不支持 %q（可选：%s）", m.ID, page, strings.Join(websiteBrowserPages, "、")))
				case seenPage[page]:
					problems = append(problems, fmt.Sprintf("%s: mount.browser_pages %q 重复", m.ID, page))
				}
				seenPage[page] = true
			}
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

		if !m.Mount.Website && len(m.Mount.BrowserPages) > 0 {
			problems = append(problems, fmt.Sprintf("%s: mount.browser_pages 只能用于根站点插件（mount.website=true）", m.ID))
		}

		if m.Runtime.Port <= 0 {
			problems = append(problems, fmt.Sprintf("%s: runtime.port 缺失", m.ID))
		}
		if m.Data.Database != "" && !strings.HasPrefix(m.Data.Database, "pf_") {
			problems = append(problems, fmt.Sprintf("%s: data.database %q 必须以 pf_ 前缀命名", m.ID, m.Data.Database))
		}
		// specs/015：第一方声明自有库 ⇒ 必须携带迁移文件（启动时自应用，append-only）
		if m.Data.Database != "" && !m.IsThirdParty() {
			matched, _ := filepath.Glob(filepath.Join(m.Dir, "migrations", "*.sql"))
			if len(matched) == 0 {
				problems = append(problems, fmt.Sprintf("%s: 声明了 data.database 但缺少 migrations/*.sql（specs/015）", m.ID))
			}
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
