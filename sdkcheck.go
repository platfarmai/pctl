// SDK 漂移检查：templates/<lang> 里的验签中间件应与 sdk/<lang> 的唯一实现一致。
// 模板是生成起点，sdk 是收敛后的事实来源；两边分叉时 check 报黄，不阻断。
package main

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
)

// sdkSources 语言 → 需与 sdk/<lang> 保持一致的模板相对路径。
var sdkSources = map[string]string{
	"go":   "main.go",
	"py":   "main.py",
	"php":  "router.php",
	"rust": filepath.Join("src", "main.rs"),
}

func reportSDKDrift(root string) {
	for lang, rel := range sdkSources {
		tmpl := filepath.Join(root, "templates", lang+"-service", rel)
		canon := filepath.Join(root, "sdk", lang, filepath.Base(rel))
		tb, errT := os.ReadFile(tmpl)
		cb, errC := os.ReadFile(canon)
		switch {
		case errC != nil:
			continue // 该语言尚未收敛到 sdk/，不报
		case errT != nil:
			fmt.Printf("⚠️  sdk/%s 存在但模板缺失 %s\n", lang, rel)
		case sha256.Sum256(tb) != sha256.Sum256(cb):
			fmt.Printf("⚠️  %s 验签中间件与 sdk/%s 漂移（改模板后同步 sdk，或反过来）\n",
				filepath.Join("templates", lang+"-service", rel), lang)
		}
	}
}
