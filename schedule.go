// pctl backup --schedule：把备份注册成宿主机计划任务（Windows schtasks / Linux cron）。
// 备份产物仍是 pctl backup 的目录，异地复制由 --remote 指向的命令完成（平台不内置传输）。
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

func runBackupSchedule(root string, args []string) error {
	at := "03:00"
	remote := ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--at":
			if i+1 < len(args) {
				at = args[i+1]
				i++
			}
		case "--remote":
			if i+1 < len(args) {
				remote = args[i+1]
				i++
			}
		}
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	script := fmt.Sprintf("%s backup --out %s", quote(exe), quote(filepath.Join(root, "backups", "scheduled")))
	if remote != "" {
		script += " && " + remote + " " + quote(filepath.Join(root, "backups", "scheduled"))
	}

	switch runtime.GOOS {
	case "windows":
		task := "PlatfarmBackup"
		cmd := exec.Command("schtasks", "/Create", "/F", "/TN", task, "/SC", "DAILY", "/ST", at, "/TR", script)
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("注册计划任务失败: %w", err)
		}
		fmt.Printf("已注册每日 %s 备份（schtasks /TN %s）。卸载：schtasks /Delete /TN %s /F\n", at, task, task)
	default:
		line := fmt.Sprintf("%s %s %s", cronHour(at), script, "# platfarm-backup")
		fmt.Println("把下面这行加入 crontab（crontab -e）：")
		fmt.Println(line)
	}
	return nil
}

func cronHour(at string) string {
	parts := strings.Split(at, ":")
	if len(parts) != 2 {
		return "0 3 * * *"
	}
	return fmt.Sprintf("%s %s * * *", strings.TrimPrefix(parts[1], "0"), strings.TrimPrefix(parts[0], "0"))
}

func quote(s string) string {
	if strings.ContainsAny(s, " \t") {
		return `"` + s + `"`
	}
	return s
}
