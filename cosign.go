package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// cosign 验签（specs/012）：外部 CLI，pctl 保持单 Go 二进制。
//
// 环境变量：
//   PF_COSIGN         cosign 二进制路径（默认 PATH 上的 cosign）
//   PF_COSIGN_MODE    enforce（默认）| warn —— cosign 缺失时报错还是仅警告
//   PF_COSIGN_REQUIRED true —— 索引条目无 signature 时拒绝安装（默认仅警告）

func cosignBin() string {
	if v := strings.TrimSpace(os.Getenv("PF_COSIGN")); v != "" {
		return v
	}
	return "cosign"
}

func cosignAvailable() bool {
	_, err := exec.LookPath(cosignBin())
	return err == nil
}

func cosignWarnMode() bool {
	return strings.EqualFold(os.Getenv("PF_COSIGN_MODE"), "warn")
}

func cosignRequired() bool {
	return strings.EqualFold(os.Getenv("PF_COSIGN_REQUIRED"), "true")
}

// verifySignature 对 image@digest 验签。返回 error 表示应拒绝安装。
func verifySignature(imageDigest string, sig *sigRef) error {
	if sig == nil {
		if cosignRequired() {
			return fmt.Errorf("索引条目无 signature，且 PF_COSIGN_REQUIRED=true → 拒绝安装")
		}
		fmt.Println("⚠️  该插件未声明 signature（未验签）。生产建议要求签名：PF_COSIGN_REQUIRED=true")
		return nil
	}
	if !cosignAvailable() {
		if cosignWarnMode() {
			fmt.Println("⚠️  未安装 cosign，PF_COSIGN_MODE=warn → 跳过验签。安装：https://docs.sigstore.dev/cosign/installation/")
			return nil
		}
		return fmt.Errorf("未安装 cosign（验签所需）。安装后重试，或临时设 PF_COSIGN_MODE=warn 跳过")
	}

	args := []string{"verify"}
	switch sig.Mode {
	case "keyless", "":
		if sig.Identity == "" || sig.Issuer == "" {
			return fmt.Errorf("keyless 验签需 signature.identity 与 signature.issuer")
		}
		args = append(args,
			"--certificate-identity", sig.Identity,
			"--certificate-oidc-issuer", sig.Issuer)
	case "key":
		if sig.PublicKey == "" {
			return fmt.Errorf("key 验签需 signature.publicKey")
		}
		keyArg := sig.PublicKey
		if !strings.HasPrefix(keyArg, "-----BEGIN") { // 非内联 PEM 视为路径
			keyArg = sig.PublicKey
		}
		args = append(args, "--key", keyArg)
	default:
		return fmt.Errorf("signature.mode %q 不支持（keyless|key）", sig.Mode)
	}
	args = append(args, imageDigest)

	fmt.Printf("cosign verify %s ...\n", imageDigest)
	cmd := exec.Command(cosignBin(), args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("cosign 验签失败: %w", err)
	}
	fmt.Println("✅ 签名验证通过")
	return nil
}
