// pctl decrypt：用登录响应里的 dataKey 解开网关 AES-256-GCM 响应体（附录 F）。
package main

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
)

type encryptedBody struct {
	V   int    `json:"v"`
	Alg string `json:"alg"`
	IV  string `json:"iv"`
	CT  string `json:"ct"`
}

func runDecrypt(args []string) error {
	keyB64 := ""
	for i := 0; i < len(args); i++ {
		if args[i] == "--key" && i+1 < len(args) {
			keyB64 = args[i+1]
			i++
		}
	}
	if keyB64 == "" {
		keyB64 = os.Getenv("PF_DATA_KEY")
	}
	if keyB64 == "" {
		return fmt.Errorf("用法: pctl decrypt --key <dataKey> <ciphertext.json>（或设 PF_DATA_KEY，密文走 stdin）")
	}
	raw, err := readCipherInput(args)
	if err != nil {
		return err
	}
	plain, err := decryptPayload(keyB64, raw)
	if err != nil {
		return err
	}
	fmt.Println(string(plain))
	return nil
}

func readCipherInput(args []string) ([]byte, error) {
	for _, a := range args {
		if a == "--key" || strings.HasPrefix(a, "-") {
			continue
		}
		if _, err := os.Stat(a); err == nil {
			return os.ReadFile(a)
		}
	}
	return io.ReadAll(os.Stdin)
}

func decryptPayload(keyB64 string, raw []byte) ([]byte, error) {
	var env encryptedBody
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("不是加密响应 JSON: %w", err)
	}
	if env.V != 1 || env.Alg != "A256GCM" {
		return nil, fmt.Errorf("不支持的封装 v=%d alg=%s", env.V, env.Alg)
	}
	key, err := base64.StdEncoding.DecodeString(keyB64)
	if err != nil || len(key) != 32 {
		return nil, fmt.Errorf("dataKey 须为 32 字节的标准 base64")
	}
	iv, err := base64.StdEncoding.DecodeString(env.IV)
	if err != nil || len(iv) != 12 {
		return nil, fmt.Errorf("iv 无效")
	}
	ct, err := base64.StdEncoding.DecodeString(env.CT)
	if err != nil || len(ct) < 16 {
		return nil, fmt.Errorf("ct 无效")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return gcm.Open(nil, iv, ct, nil)
}
