package main

import (
	"fmt"
	"strings"
)

// runCheck 校验全部服务清单；--e2e 时在各服务容器内执行契约测试（需要平台已 up）。
func runCheck(root string, e2e bool) error {
	manifests, err := loadManifests(root)
	if err != nil {
		return err
	}
	if err := validateManifests(manifests); err != nil {
		return err
	}
	fmt.Printf("清单校验通过：%d 个服务\n", len(manifests))

	if !e2e {
		return nil
	}
	var failed []string
	for _, m := range manifests {
		if m.Test.Command == "" {
			fmt.Printf("  %s: 无 test.command，跳过\n", m.ID)
			continue
		}
		fmt.Printf("  %s: 运行契约测试 ...\n", m.ID)
		cmdArgs := append([]string{m.ID}, strings.Fields(m.Test.Command)...)
		if err := composeExec(root, cmdArgs...); err != nil {
			failed = append(failed, m.ID)
			fmt.Printf("  %s: ❌ 契约测试失败: %v\n", m.ID, err)
			continue
		}
		fmt.Printf("  %s: ✅\n", m.ID)
	}
	if len(failed) > 0 {
		return fmt.Errorf("契约测试未通过: %s", strings.Join(failed, ", "))
	}
	return nil
}
