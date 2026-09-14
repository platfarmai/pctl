// pctl — Platfarm 平台工具：new / sync / check / list。
// 纪律执行器：网关与服务编排配置只能由本工具从 service.yaml 生成。
package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "pctl:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return usageError()
	}
	if args[0] == "init" {
		dir := "."
		if len(args) > 1 {
			dir = args[1]
		}
		return runInit(dir)
	}
	root, err := findRoot()
	if err != nil {
		return err
	}
	switch args[0] {
	case "new":
		id, lang := "", "go" // 语言优先级：go > rust > py（ADR #17）
		for i := 1; i < len(args); i++ {
			if args[i] == "--lang" && i+1 < len(args) {
				lang = args[i+1]
				i++
				continue
			}
			id = args[i]
		}
		if id == "" {
			return fmt.Errorf("用法: pctl new <svc-id> [--lang py]")
		}
		return runNew(root, id, lang)
	case "sync":
		return runSync(root)
	case "check":
		e2e := len(args) > 1 && args[1] == "--e2e"
		return runCheck(root, e2e)
	case "list":
		return runList(root)
	case "install":
		if len(args) < 2 {
			return fmt.Errorf("用法: pctl install <插件目录>")
		}
		return runInstall(root, args[1])
	case "enable", "disable":
		if len(args) < 2 {
			return fmt.Errorf("用法: pctl %s <svc-id>", args[0])
		}
		return runToggle(root, args[1], args[0] == "enable")
	case "uninstall":
		if len(args) < 2 {
			return fmt.Errorf("用法: pctl uninstall <svc-id> [--purge]")
		}
		purge := len(args) > 2 && args[2] == "--purge"
		return runUninstall(root, args[1], purge)
	case "serve":
		return runServe(root)
	case "upgrade":
		if len(args) < 3 {
			return fmt.Errorf("用法: pctl upgrade <svc-id> <新插件目录>")
		}
		if err := runUninstall(root, args[1], false); err != nil {
			return err
		}
		return runInstall(root, args[2])
	default:
		return usageError()
	}
}

func usageError() error {
	return fmt.Errorf(`用法:
  pctl init [dir]                   空目录生成 .keys/ + gateway/kong.yml（拉镜像启动用）
  pctl new <svc-id> [--lang go|rust|py|php]  从模板生成第一方服务骨架（默认 go）
  pctl sync                         清单 → kong.yml + compose 片段（含密钥自举）
  pctl check [--e2e]                清单校验（--e2e 附加运行容器内契约测试）
  pctl list                         平台服务总览
  pctl install <插件目录>            安装第三方插件（开库开号+发凭据+契约测试闸门）
  pctl enable|disable <svc-id>      启停服务/插件（摘挂路由）
  pctl uninstall <svc-id> [--purge] 卸载（--purge 连数据一起删）
  pctl serve                      启动 Web 控制台（容器内运行，见 compose console 服务）
  pctl upgrade <svc-id> <新目录>     升级（卸载保数据 + 重装）`)
}

// findRoot 从当前目录向上找平台根（以 gateway/ 目录 + docker-compose.yml 为标志）。
// 容器内运行（console）用 PLATFARM_ROOT 直指挂载点。
func findRoot() (string, error) {
	if v := os.Getenv("PLATFARM_ROOT"); v != "" {
		return v, nil
	}
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(dir + string(os.PathSeparator) + "gateway"); err == nil {
			if _, err := os.Stat(dir + string(os.PathSeparator) + "docker-compose.yml"); err == nil {
				return dir, nil
			}
			// Release workspace: gateway/ + compose.yml (from compose.release.yml)
			if _, err := os.Stat(dir + string(os.PathSeparator) + "compose.yml"); err == nil {
				return dir, nil
			}
		}
		parent := parentDir(dir)
		if parent == dir {
			return "", fmt.Errorf("未找到平台根目录（需含 gateway/ 与 docker-compose.yml 或 compose.yml）")
		}
		dir = parent
	}
}

func parentDir(dir string) string {
	trimmed := strings.TrimRight(dir, string(os.PathSeparator))
	idx := strings.LastIndex(trimmed, string(os.PathSeparator))
	if idx < 0 {
		return dir
	}
	if parent := trimmed[:idx]; parent != "" {
		return parent
	}
	return dir
}

func runList(root string) error {
	manifests, err := loadManifests(root)
	if err != nil {
		return err
	}
	fmt.Printf("%-20s %-9s %-14s %-12s %-8s %-8s %s\n", "ID", "VERSION", "MOUNT", "TRUST", "AUTH", "STATE", "RATE/MIN")
	for _, m := range manifests {
		auth := "public"
		if m.Auth.Required {
			auth = "jwt"
		}
		trust := m.Trust
		if trust == "" {
			trust = "first-party"
		}
		state := "enabled"
		if !m.IsEnabled() {
			state = "disabled"
		}
		fmt.Printf("%-20s %-9s %-14s %-12s %-8s %-8s %d\n", m.ID, m.Version, m.Mount.Path, trust, auth, state, m.Limits.RatePerMinute)
	}
	return nil
}

func composeExec(root string, cmdArgs ...string) error {
	args := append([]string{"compose", "exec", "-T"}, cmdArgs...)
	cmd := exec.Command(containerCLI(), args...)
	cmd.Dir = root
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
