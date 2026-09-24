package main

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// 插件生命周期（附录 H.6）：install → 校验 → 落盘 → sync → 开库开号 → 发凭据 → 起容器 → 契约测试。
// 测试失败自动 disable，不留半装状态。

func runInstall(root, pluginDir string) error {
	m, err := loadManifestFile(filepath.Join(pluginDir, "plugin.yaml"))
	if err != nil {
		return err
	}
	destDir := filepath.Join(root, "services", m.ID)
	if _, err := os.Stat(destDir); err == nil {
		return fmt.Errorf("%s 已存在（升级用 pctl upgrade）", destDir)
	}
	if err := copyDir(pluginDir, destDir); err != nil {
		return err
	}
	// env 文件必须先于任何 compose 命令存在（生成的编排引用 env_file）
	if err := writePluginEnv(root, m.ID, nil); err != nil {
		return err
	}
	if err := runSync(root); err != nil {
		_ = os.RemoveAll(destDir)
		return err
	}

	if m.Data.Database != "" {
		if err := provisionPluginDB(root, m); err != nil {
			return fmt.Errorf("插件库开号失败: %w", err)
		}
	}
	if err := registerPluginClient(root, m); err != nil {
		return fmt.Errorf("插件凭据注册失败: %w", err)
	}

	if err := composeUp(root, "--build"); err != nil {
		return err
	}
	if err := restartGateway(root); err != nil {
		return err
	}

	if m.Test.Command != "" {
		fmt.Printf("运行契约测试：%s ...\n", m.ID)
		cmdArgs := append([]string{m.ID}, strings.Fields(m.Test.Command)...)
		if err := composeExec(root, cmdArgs...); err != nil {
			fmt.Println("❌ 契约测试失败，自动 disable")
			_ = runToggle(root, m.ID, false)
			return fmt.Errorf("契约测试未通过，插件已置为 disabled")
		}
	}
	fmt.Printf("✅ 插件 %s 安装完成（网络 %s）\n", m.ID, m.NetworkName())
	return nil
}

// provisionPluginDB 在 plugin-pg 上开角色+库并收回 PUBLIC 连接权（附录 H.3 双层隔离的权限层）。
func provisionPluginDB(root string, m Manifest) error {
	if err := composeUp(root, "--wait", "plugin-pg"); err != nil {
		return err
	}
	role := m.Data.Database // 角色名 = 库名（pf_plugin_*）
	pass := randomHex(16)
	stmts := []string{
		fmt.Sprintf(`CREATE ROLE %s LOGIN PASSWORD '%s' NOSUPERUSER NOCREATEDB NOCREATEROLE`, role, pass),
		fmt.Sprintf(`CREATE DATABASE %s OWNER %s`, m.Data.Database, role),
		fmt.Sprintf(`REVOKE CONNECT ON DATABASE %s FROM PUBLIC`, m.Data.Database),
		fmt.Sprintf(`GRANT CONNECT ON DATABASE %s TO %s`, m.Data.Database, role),
	}
	for _, stmt := range stmts {
		if err := composeExec(root, "plugin-pg", "psql", "-U", "postgres", "-c", stmt); err != nil {
			return fmt.Errorf("执行 %q: %w", stmt, err)
		}
	}
	url := fmt.Sprintf("postgres://%s:%s@plugin-pg:5432/%s", role, pass, m.Data.Database)
	return writePluginEnv(root, m.ID, map[string]string{"DATABASE_URL": url})
}

// registerPluginClient 经 auth 的 register-client CLI 开出 client_credentials，写入插件 env。
func registerPluginClient(root string, m Manifest) error {
	if err := composeUp(root, "--wait", "auth"); err != nil {
		return err
	}
	out, err := composeExecCapture(root, "auth", "/auth",
		"-register-client", m.ID, "-scopes", strings.Join(m.Permissions.Calls, ","))
	if err != nil {
		return err
	}
	vars := map[string]string{"PF_CLIENT_ID": m.ID}
	for _, line := range strings.Split(out, "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "CLIENT_SECRET="); ok {
			vars["PF_CLIENT_SECRET"] = v
		}
	}
	if vars["PF_CLIENT_SECRET"] == "" {
		return fmt.Errorf("未能从 register-client 输出解析 secret")
	}
	return writePluginEnv(root, m.ID, vars)
}

// writePluginEnv 追加/创建 .env.plugins/<id>.env（gitignore，仅注入该插件容器）。
func writePluginEnv(root, id string, vars map[string]string) error {
	dir := filepath.Join(root, ".env.plugins")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(dir, id+".env")
	existing, _ := os.ReadFile(path)
	var b strings.Builder
	b.Write(existing)
	for k, v := range vars {
		fmt.Fprintf(&b, "%s=%s\n", k, v)
	}
	return os.WriteFile(path, []byte(b.String()), 0o600)
}

func runToggle(root, id string, enable bool) error {
	marker := filepath.Join(root, "services", id, ".disabled")
	if enable {
		_ = os.Remove(marker)
	} else if err := os.WriteFile(marker, []byte("disabled by pctl\n"), 0o644); err != nil {
		return err
	}
	if err := runSync(root); err != nil {
		return err
	}
	if err := composeUp(root, "--remove-orphans"); err != nil {
		return err
	}
	if err := restartGateway(root); err != nil {
		return err
	}
	fmt.Printf("%s → enabled=%v\n", id, enable)
	return nil
}

func runUninstall(root, id string, purge bool) error {
	dir := filepath.Join(root, "services", id)
	if _, err := os.Stat(dir); err != nil {
		return fmt.Errorf("服务 %s 不存在", id)
	}
	m, merr := loadManifestFile(filepath.Join(dir, "plugin.yaml"))
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	if purge && merr == nil && m.Data.Database != "" {
		// specs/020：drop 前自动快照，误删可救
		if dump, derr := composeExecCapture(root, "plugin-pg", "pg_dump", "-U", "postgres", m.Data.Database); derr == nil {
			snapDir := filepath.Join(root, "backups")
			_ = os.MkdirAll(snapDir, 0o700)
			snap := filepath.Join(snapDir, fmt.Sprintf("%s-purge-%s.sql", id, time.Now().Format("20060102-150405")))
			if os.WriteFile(snap, []byte(dump), 0o600) == nil {
				fmt.Printf("已快照插件库 → %s\n", snap)
			}
		}
		for _, stmt := range []string{
			fmt.Sprintf(`DROP DATABASE IF EXISTS %s`, m.Data.Database),
			fmt.Sprintf(`DROP ROLE IF EXISTS %s`, m.Data.Database),
		} {
			_ = composeExec(root, "plugin-pg", "psql", "-U", "postgres", "-c", stmt)
		}
		_ = os.Remove(filepath.Join(root, ".env.plugins", id+".env"))
	}
	if err := runSync(root); err != nil {
		return err
	}
	if err := composeUp(root, "--remove-orphans"); err != nil {
		return err
	}
	if err := restartGateway(root); err != nil {
		return err
	}
	fmt.Printf("已卸载 %s（purge=%v）\n", id, purge)
	return nil
}

// ── 工具函数 ──────────────────────────────────────────────────

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func copyDir(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, raw, 0o644)
	})
}

func composeUp(root string, extra ...string) error {
	args := append([]string{"compose", "up", "-d"}, extra...)
	return dockerRun(root, args...)
}

func restartGateway(root string) error {
	return dockerRun(root, "compose", "restart", "gateway")
}

func dockerRun(root string, args ...string) error {
	cmd := exec.Command(containerCLI(), args...)
	cmd.Dir = root
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func composeExecCapture(root string, cmdArgs ...string) (string, error) {
	args := append([]string{"compose", "exec", "-T"}, cmdArgs...)
	cmd := exec.Command(containerCLI(), args...)
	cmd.Dir = root
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = os.Stderr
	err := cmd.Run()
	return out.String(), err
}
