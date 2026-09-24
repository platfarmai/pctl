// pctl backup / restore（specs/020）：平台数据的最小备份面。
// 备份 = bundled postgres 全量 pg_dumpall + plugin-pg 全量 + .keys/ + .env.plugins/。
// 外部数据库模式（DATABASE_URL 指向平台外实例）由所有者自备份，本命令提示后跳过。
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func execCommand(root string, args ...string) *exec.Cmd {
	cmd := exec.Command(containerCLI(), args...)
	cmd.Dir = root
	return cmd
}

func runBackup(root string, args []string) error {
	out := filepath.Join(root, "backups", time.Now().Format("20060102-150405"))
	for i := 0; i < len(args); i++ {
		if args[i] == "--out" && i+1 < len(args) {
			out = args[i+1]
			i++
		}
	}
	if err := os.MkdirAll(out, 0o700); err != nil {
		return err
	}

	// 平台库（bundled-db profile 有 postgres 容器时）
	if dump, err := composeExecCapture(root, "postgres", "pg_dumpall", "-U", "platfarm"); err == nil {
		if werr := os.WriteFile(filepath.Join(out, "platform-pg.sql"), []byte(dump), 0o600); werr != nil {
			return werr
		}
		fmt.Println("✅ platform-pg.sql（bundled postgres 全量）")
	} else {
		fmt.Println("ℹ️  bundled postgres 未运行——外部数据库模式请自行执行:")
		fmt.Println("    pg_dumpall（或对 pf_auth / pf_svc_* 各库 pg_dump），连接串见 .env DATABASE_URL")
	}

	// 插件库（有第三方插件时才有 plugin-pg）
	if dump, err := composeExecCapture(root, "plugin-pg", "pg_dumpall", "-U", "postgres"); err == nil {
		if werr := os.WriteFile(filepath.Join(out, "plugin-pg.sql"), []byte(dump), 0o600); werr != nil {
			return werr
		}
		fmt.Println("✅ plugin-pg.sql（插件库全量）")
	}

	// 密钥与插件凭据（gitignore 的真相源，丢失不可再生）
	for _, dir := range []string{".keys", ".env.plugins"} {
		src := filepath.Join(root, dir)
		if _, err := os.Stat(src); err != nil {
			continue
		}
		if err := copyDir(src, filepath.Join(out, dir)); err != nil {
			return fmt.Errorf("copy %s: %w", dir, err)
		}
		fmt.Printf("✅ %s/\n", dir)
	}

	meta := fmt.Sprintf("created_at: %s\nnote: restore with `pctl restore %s --force`\n",
		time.Now().Format(time.RFC3339), out)
	if err := os.WriteFile(filepath.Join(out, "backup-info.txt"), []byte(meta), 0o600); err != nil {
		return err
	}
	fmt.Printf("备份完成：%s（含密钥与凭据，请妥善保管）\n", out)
	return nil
}

func runRestore(root string, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("用法: pctl restore <备份目录> --force")
	}
	dir := args[0]
	force := false
	for _, a := range args[1:] {
		if a == "--force" {
			force = true
		}
	}
	if !force {
		return fmt.Errorf("restore 会覆盖数据库现有数据，确认请加 --force")
	}

	if raw, err := os.ReadFile(filepath.Join(dir, "platform-pg.sql")); err == nil {
		if err := composeExecStdin(root, string(raw), "postgres", "psql", "-U", "platfarm", "-d", "postgres"); err != nil {
			return fmt.Errorf("平台库恢复失败: %w", err)
		}
		fmt.Println("✅ 平台库已恢复")
	}
	if raw, err := os.ReadFile(filepath.Join(dir, "plugin-pg.sql")); err == nil {
		if err := composeExecStdin(root, string(raw), "plugin-pg", "psql", "-U", "postgres", "-d", "postgres"); err != nil {
			return fmt.Errorf("插件库恢复失败: %w", err)
		}
		fmt.Println("✅ 插件库已恢复")
	}
	for _, sub := range []string{".keys", ".env.plugins"} {
		src := filepath.Join(dir, sub)
		if _, err := os.Stat(src); err != nil {
			continue
		}
		if err := copyDir(src, filepath.Join(root, sub)); err != nil {
			return fmt.Errorf("restore %s: %w", sub, err)
		}
		fmt.Printf("✅ %s/ 已恢复\n", sub)
	}
	fmt.Println("恢复完成；执行 docker compose restart 使各服务重连")
	return nil
}

// composeExecStdin 以字符串作 stdin 执行 compose exec（psql 导入用）。
func composeExecStdin(root, stdin string, cmdArgs ...string) error {
	args := append([]string{"compose", "exec", "-T"}, cmdArgs...)
	cmd := execCommand(root, args...)
	cmd.Stdin = strings.NewReader(stdin)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
