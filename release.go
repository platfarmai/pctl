// pctl release：第一方服务的版本发布记录（仅记账，不改 compose / 不碰运行中容器）。
// 状态文件：deploy/releases.json；插件升级仍走 pctl upgrade。
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// releaseEntry 一次发布记账；services 切片按时间追加，末条即当前。
type releaseEntry struct {
	Service    string `json:"service"`
	Version    string `json:"version"`
	Image      string `json:"image"`
	Digest     string `json:"digest"`
	DeployedAt string `json:"deployed_at"`
	Note       string `json:"note,omitempty"`
}

// releaseFile 为零值时等价于 {"services":[]}。
type releaseFile struct {
	Services []releaseEntry `json:"services"`
}

// imageDigest 可被测试替换，避免真实 docker。
var imageDigest = defaultImageDigest

func defaultImageDigest(image string) (string, error) {
	cmd := exec.Command("docker", "image", "inspect", "--format", "{{index .RepoDigests 0}}", image)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("解析镜像 digest 失败（请先 docker pull %s）: %w", image, err)
	}
	dig := strings.TrimSpace(string(out))
	if dig == "" {
		return "", fmt.Errorf("镜像 %s 无 RepoDigests（请先 docker pull）", image)
	}
	return dig, nil
}

func releasesPath(root string) string {
	return filepath.Join(root, "deploy", "releases.json")
}

func loadReleases(root string) (releaseFile, error) {
	path := releasesPath(root)
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return releaseFile{Services: []releaseEntry{}}, nil
		}
		return releaseFile{}, fmt.Errorf("读 %s: %w", path, err)
	}
	var f releaseFile
	if err := json.Unmarshal(raw, &f); err != nil {
		return releaseFile{}, fmt.Errorf("解析 %s: %w", path, err)
	}
	if f.Services == nil {
		f.Services = []releaseEntry{}
	}
	return f, nil
}

func saveReleases(root string, f releaseFile) error {
	dir := filepath.Join(root, "deploy")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if f.Services == nil {
		f.Services = []releaseEntry{}
	}
	raw, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	return os.WriteFile(releasesPath(root), raw, 0o644)
}

func runRelease(root string, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("用法: pctl release <list|deploy|rollback> ...")
	}
	switch args[0] {
	case "list":
		return releaseList(root, os.Stdout)
	case "deploy":
		if len(args) < 3 {
			return fmt.Errorf("用法: pctl release deploy <service-id> <image:tag>")
		}
		return releaseDeploy(root, args[1], args[2])
	case "rollback":
		if len(args) < 2 {
			return fmt.Errorf("用法: pctl release rollback <service-id>")
		}
		return releaseRollback(root, args[1], os.Stdout)
	default:
		return fmt.Errorf("用法: pctl release <list|deploy|rollback> ...")
	}
}

func releaseList(root string, w io.Writer) error {
	f, err := loadReleases(root)
	if err != nil {
		return err
	}
	if len(f.Services) == 0 {
		fmt.Fprintln(w, "no releases yet")
		return nil
	}
	for _, e := range f.Services {
		fmt.Fprintf(w, "%s %s %s %s %s\n", e.Service, e.Version, e.Image, e.Digest, e.DeployedAt)
	}
	return nil
}

func releaseDeploy(root, serviceID, imageRef string) error {
	version := imageTag(imageRef)
	digest, err := imageDigest(imageRef)
	if err != nil {
		return err
	}
	f, err := loadReleases(root)
	if err != nil {
		return err
	}
	f.Services = append(f.Services, releaseEntry{
		Service:    serviceID,
		Version:    version,
		Image:      imageRef,
		Digest:     digest,
		DeployedAt: time.Now().UTC().Format(time.RFC3339),
	})
	if err := saveReleases(root, f); err != nil {
		return err
	}
	fmt.Printf("已记录发布：%s %s digest=%s（仅记账，未改运行中容器）\n", serviceID, version, digest)
	return nil
}

func releaseRollback(root, serviceID string, w io.Writer) error {
	f, err := loadReleases(root)
	if err != nil {
		return err
	}
	var hist []releaseEntry
	for _, e := range f.Services {
		if e.Service == serviceID {
			hist = append(hist, e)
		}
	}
	if len(hist) < 2 {
		return fmt.Errorf("%s: 无上一版可回滚（需至少两条发布记录）", serviceID)
	}
	prev := hist[len(hist)-2]
	f.Services = append(f.Services, releaseEntry{
		Service:    serviceID,
		Version:    prev.Version,
		Image:      prev.Image,
		Digest:     prev.Digest,
		DeployedAt: time.Now().UTC().Format(time.RFC3339),
		Note:       "rollback",
	})
	if err := saveReleases(root, f); err != nil {
		return err
	}
	ref := imageAtDigest(prev)
	fmt.Fprintln(w, ref)
	fmt.Fprintf(w, "提示: docker compose up -d %s  # 切回 %s\n", serviceID, ref)
	return nil
}

func imageTag(ref string) string {
	if i := strings.LastIndex(ref, "@"); i >= 0 {
		ref = ref[:i]
	}
	if i := strings.LastIndex(ref, ":"); i >= 0 && !strings.Contains(ref[i:], "/") {
		return ref[i+1:]
	}
	return "latest"
}

func imageAtDigest(e releaseEntry) string {
	if strings.Contains(e.Digest, "@") {
		return e.Digest
	}
	base := e.Image
	if i := strings.LastIndex(base, "@"); i >= 0 {
		base = base[:i]
	}
	if i := strings.LastIndex(base, ":"); i >= 0 && !strings.Contains(base[i:], "/") {
		base = base[:i]
	}
	return base + "@" + e.Digest
}
