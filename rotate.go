// pctl rotate-keys：RS256 签名密钥重叠轮换。
// 新密钥立即成为签发密钥；旧公钥保留在 JWKS（.keys/jwks-extra/）一个 access TTL，
// 在途 token 继续可验。网关只认单把公钥，所以轮换后必须重新 sync + 重启 gateway 才会切验签。
package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

func runRotateKeys(root string) error {
	keyDir := filepath.Join(root, ".keys")
	privPath := filepath.Join(keyDir, "pf-auth.pem")
	pubPath := privPath + ".pub"
	oldPriv, err := os.ReadFile(privPath)
	if err != nil {
		return fmt.Errorf("读取现用私钥失败（先 pctl sync 生成）: %w", err)
	}
	oldPub, err := os.ReadFile(pubPath)
	if err != nil {
		return fmt.Errorf("读取现用公钥失败: %w", err)
	}

	extra := filepath.Join(keyDir, "jwks-extra")
	if err := os.MkdirAll(extra, 0o700); err != nil {
		return err
	}
	stamp := time.Now().UTC().Format("20060102T150405Z")
	if err := os.WriteFile(filepath.Join(extra, stamp+".pub"), oldPub, 0o644); err != nil {
		return err
	}
	retired := filepath.Join(keyDir, "retired")
	_ = os.MkdirAll(retired, 0o700)
	if err := os.WriteFile(filepath.Join(retired, stamp+".pem"), oldPriv, 0o600); err != nil {
		return err
	}

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return err
	}
	privPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	pubDER, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		return err
	}
	pubPEM := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubDER})
	if err := os.WriteFile(privPath, privPEM, 0o600); err != nil {
		return err
	}
	if err := os.WriteFile(pubPath, pubPEM, 0o644); err != nil {
		return err
	}

	fmt.Printf("已轮换签名密钥。旧公钥保留至 .keys/jwks-extra/%s.pub（在途 token 仍可验）。\n", stamp)
	fmt.Println("下一步：pctl sync && docker compose up -d auth gateway，然后重启各服务以加载新公钥。")
	fmt.Println("确认无在途旧 token 后删除 .keys/jwks-extra 与 .keys/retired。")
	return nil
}
