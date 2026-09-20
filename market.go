package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// 插件市场 M1（specs/008）：git 索引 + digest 锁定安装。
// 索引条目 plugins/<id>/index.yaml：一个插件的全部已发布版本。

const defaultMarketIndex = "https://raw.githubusercontent.com/platfarmai/market-index/main"

type marketVersion struct {
	Version   string   `yaml:"version"`
	Image     string   `yaml:"image"`
	Digest    string   `yaml:"digest"`
	Manifest  Manifest `yaml:"manifest"`
	Published string   `yaml:"published"`
	Signature *sigRef  `yaml:"signature"` // cosign 验签引用（specs/012）
}

type sigRef struct {
	Mode      string `yaml:"mode"`      // keyless | key
	Identity  string `yaml:"identity"`  // keyless: 证书身份
	Issuer    string `yaml:"issuer"`    // keyless: OIDC issuer
	PublicKey string `yaml:"publicKey"` // key: PEM 或路径
}

type marketEntry struct {
	ID       string          `yaml:"id"`
	Title    string          `yaml:"title"`
	Desc     string          `yaml:"desc"`
	Versions []marketVersion `yaml:"versions"`
}

func marketIndexBase() string {
	if v := strings.TrimSpace(os.Getenv("PF_MARKET_INDEX")); v != "" {
		return strings.TrimRight(v, "/")
	}
	return defaultMarketIndex
}

// fetchIndexFile 读取索引下的一个相对路径，支持 file:// 与 http(s)。
func fetchIndexFile(base, rel string) ([]byte, error) {
	if strings.HasPrefix(base, "file://") {
		p := strings.TrimPrefix(base, "file://")
		// file:///C:/x → /C:/x → C:/x（去掉盘符前多余的 /）
		if len(p) >= 3 && p[0] == '/' && p[2] == ':' {
			p = p[1:]
		}
		return os.ReadFile(filepath.Join(filepath.FromSlash(p), filepath.FromSlash(rel)))
	}
	url := base + "/" + rel
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("fetch %s: HTTP %d", url, resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 1<<20))
}

func loadMarketEntry(base, id string) (marketEntry, error) {
	var e marketEntry
	raw, err := fetchIndexFile(base, "plugins/"+id+"/index.yaml")
	if err != nil {
		return e, err
	}
	if err := yaml.Unmarshal(raw, &e); err != nil {
		return e, fmt.Errorf("parse index for %s: %w", id, err)
	}
	if e.ID == "" {
		e.ID = id
	}
	return e, nil
}

// pickVersion 选指定版本；ver 为空取最新（按 published 倒序，退化为切片末尾）。
func pickVersion(e marketEntry, ver string) (marketVersion, error) {
	if len(e.Versions) == 0 {
		return marketVersion{}, fmt.Errorf("%s: 索引无任何版本", e.ID)
	}
	if ver != "" {
		for _, v := range e.Versions {
			if v.Version == ver {
				return v, nil
			}
		}
		return marketVersion{}, fmt.Errorf("%s 无版本 %s", e.ID, ver)
	}
	sorted := append([]marketVersion(nil), e.Versions...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Published > sorted[j].Published })
	return sorted[0], nil
}

func splitIDVer(arg string) (id, ver string) {
	if i := strings.LastIndex(arg, "@"); i > 0 {
		return arg[:i], arg[i+1:]
	}
	return arg, ""
}

func runMarket(root string, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("用法: pctl market <search|info|install> ...")
	}
	base := marketIndexBase()
	switch args[0] {
	case "search":
		if len(args) < 2 {
			return fmt.Errorf("用法: pctl market search <关键词>")
		}
		return marketSearch(base, args[1])
	case "info":
		if len(args) < 2 {
			return fmt.Errorf("用法: pctl market info <id>[@版本]")
		}
		id, ver := splitIDVer(args[1])
		return marketInfo(base, id, ver)
	case "install":
		dryRun := false
		var target string
		for _, a := range args[1:] {
			if a == "--dry-run" {
				dryRun = true
				continue
			}
			target = a
		}
		if target == "" {
			return fmt.Errorf("用法: pctl market install <id>[@版本] [--dry-run]")
		}
		id, ver := splitIDVer(target)
		return marketInstall(root, base, id, ver, dryRun)
	default:
		return fmt.Errorf("未知子命令 %q（search|info|install）", args[0])
	}
}

func marketSearch(base, q string) error {
	// 索引根 catalog.yaml 列出所有插件 id/title/desc，供离线搜索
	raw, err := fetchIndexFile(base, "catalog.yaml")
	if err != nil {
		return fmt.Errorf("读取 catalog.yaml 失败（索引仓需提供）: %w", err)
	}
	var catalog struct {
		Plugins []struct{ ID, Title, Desc string } `yaml:"plugins"`
	}
	if err := yaml.Unmarshal(raw, &catalog); err != nil {
		return err
	}
	ql := strings.ToLower(q)
	fmt.Printf("%-24s %-20s %s\n", "ID", "TITLE", "DESC")
	for _, p := range catalog.Plugins {
		hay := strings.ToLower(p.ID + " " + p.Title + " " + p.Desc)
		if strings.Contains(hay, ql) {
			fmt.Printf("%-24s %-20s %s\n", p.ID, p.Title, p.Desc)
		}
	}
	return nil
}

func marketInfo(base, id, ver string) error {
	e, err := loadMarketEntry(base, id)
	if err != nil {
		return err
	}
	fmt.Printf("%s — %s\n%s\n\n", e.ID, e.Title, e.Desc)
	for _, v := range e.Versions {
		if ver != "" && v.Version != ver {
			continue
		}
		fmt.Printf("版本 %s (%s)\n  镜像: %s@%s\n", v.Version, v.Published, v.Image, v.Digest)
		if len(v.Manifest.Permissions.Calls) > 0 {
			fmt.Printf("  权限-调用: %s\n", strings.Join(v.Manifest.Permissions.Calls, ", "))
		}
		if len(v.Manifest.Permissions.Egress) > 0 {
			fmt.Printf("  权限-出网: %s\n", strings.Join(v.Manifest.Permissions.Egress, ", "))
		}
		if v.Manifest.Permissions.NeedsIdentity {
			fmt.Printf("  权限-身份: 需要用户身份\n")
		}
	}
	return nil
}

func marketInstall(root, base, id, ver string, dryRun bool) error {
	e, err := loadMarketEntry(base, id)
	if err != nil {
		return err
	}
	v, err := pickVersion(e, ver)
	if err != nil {
		return err
	}
	if v.Digest == "" {
		return fmt.Errorf("%s@%s 索引缺 digest，拒绝安装（M1 强制 digest 锁定）", id, v.Version)
	}

	// 用内嵌 manifest 作底，覆盖为第三方镜像来源 + digest
	m := v.Manifest
	m.ID = id
	m.Trust = "third-party"
	m.Source.Type = "image"
	m.Source.Image = v.Image
	m.Source.Digest = v.Digest

	fmt.Printf("即将安装 %s@%s\n  镜像: %s@%s\n", id, v.Version, v.Image, v.Digest)
	if len(m.Permissions.Calls) > 0 {
		fmt.Printf("  申请权限（calls）: %s\n", strings.Join(m.Permissions.Calls, ", "))
	}
	if len(m.Permissions.Egress) > 0 {
		fmt.Printf("  申请出网（egress）: %s\n", strings.Join(m.Permissions.Egress, ", "))
	}

	// cosign 验签（specs/012）：dry-run 不验，真实安装前对 image@digest 验签
	if !dryRun {
		if err := verifySignature(v.Image+"@"+v.Digest, v.Signature); err != nil {
			return err
		}
	}

	// 落盘到临时插件目录，复用既有 install 闸门
	stage, err := os.MkdirTemp("", "pf-market-"+id+"-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	out, err := yaml.Marshal(&m)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(stage, "plugin.yaml"), out, 0o644); err != nil {
		return err
	}
	if dryRun {
		fmt.Println("--- dry-run：生成的 plugin.yaml（不安装）---")
		fmt.Print(string(out))
		return nil
	}
	return runInstall(root, stage)
}
