// 本文件按 Circle 的实体密钥算法生成一次性密文。
// 算法与 github.com/circlefin/w3s-entity-secret-sample-code 的 Go 示例一致：
// 32 字节实体密钥，用 Circle 公钥做 RSA-OAEP（SHA-256，MGF1 也是 SHA-256），再做标准 base64。
package treasury

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"strings"
)

// ParseEntitySecret 接受 64 位十六进制实体密钥。
func ParseEntitySecret(raw string) ([]byte, error) {
	raw = trimHexPrefix(raw)
	secret, err := hex.DecodeString(raw)
	if err != nil || len(secret) != 32 {
		return nil, fmt.Errorf("CIRCLE_ENTITY_SECRET must be 32 bytes of hex")
	}
	return secret, nil
}

// EncryptEntitySecret 生成一次性 entitySecretCiphertext。每次请求都要重新加密。
func EncryptEntitySecret(pub *rsa.PublicKey, secret []byte) (string, error) {
	if pub == nil || len(secret) != 32 {
		return "", fmt.Errorf("entity secret encryption needs a public key and 32 secret bytes")
	}
	cipher, err := rsa.EncryptOAEP(sha256.New(), rand.Reader, pub, secret, nil)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(cipher), nil
}

// ParseCirclePublicKey 解析 Circle 返回的 PKIX PEM 公钥。
func ParseCirclePublicKey(pemText string) (*rsa.PublicKey, error) {
	block, _ := pem.Decode([]byte(pemText))
	if block == nil {
		return nil, fmt.Errorf("circle public key is not PEM")
	}
	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	rsaPub, ok := pub.(*rsa.PublicKey)
	if !ok {
		return nil, fmt.Errorf("circle public key is not RSA")
	}
	return rsaPub, nil
}

func trimHexPrefix(raw string) string {
	raw = strings.TrimSpace(raw)
	if len(raw) >= 2 && (raw[0:2] == "0x" || raw[0:2] == "0X") {
		return raw[2:]
	}
	return raw
}
