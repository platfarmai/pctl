package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// runNew 从 templates/<lang>-service 生成服务骨架到 services/<id>。
func runNew(root, id, lang string) error {
	if !strings.HasPrefix(id, "svc-") {
		return fmt.Errorf("服务 id 必须以 svc- 开头，收到 %q", id)
	}
	templateDir := filepath.Join(root, "templates", lang+"-service")
	if _, err := os.Stat(templateDir); err != nil {
		return fmt.Errorf("模板 %s 不存在（当前支持: go/rust/py/php）: %w", templateDir, err)
	}
	destDir := filepath.Join(root, "services", id)
	if _, err := os.Stat(destDir); err == nil {
		return fmt.Errorf("%s 已存在，拒绝覆盖", destDir)
	}

	mount := "/api/" + strings.TrimPrefix(id, "svc-")
	replacer := strings.NewReplacer("__SVC_ID__", id, "__MOUNT__", mount)

	err := filepath.WalkDir(templateDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(templateDir, path)
		if err != nil {
			return err
		}
		dest := filepath.Join(destDir, rel)
		if d.IsDir() {
			return os.MkdirAll(dest, 0o755)
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(dest, []byte(replacer.Replace(string(raw))), 0o644)
	})
	if err != nil {
		return fmt.Errorf("生成骨架失败: %w", err)
	}

	fmt.Printf("已生成 %s（挂载点 %s）\n", destDir, mount)
	fmt.Println("下一步：实现业务 → pctl sync → docker compose up -d --build → pctl check")
	return nil
}
