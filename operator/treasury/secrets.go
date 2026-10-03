// 本文件从环境变量或权限不宽于 0600 的文件读取十六进制私钥。
package treasury

import (
	"crypto/ecdsa"
	"fmt"
	"os"
	"strings"

	"github.com/ethereum/go-ethereum/crypto"
)

// LoadPrivateKey 读取私钥。两者都为空时返回 nil，不视为错误。
func LoadPrivateKey(envName, filePath string) (*ecdsa.PrivateKey, error) {
	hexKey, err := loadHex(envName, filePath)
	if err != nil || hexKey == "" {
		return nil, err
	}
	key, err := crypto.HexToECDSA(hexKey)
	if err != nil {
		return nil, fmt.Errorf("invalid private key")
	}
	return key, nil
}

func loadHex(envName, filePath string) (string, error) {
	raw, err := LoadSecret(envName, filePath)
	if err != nil || raw == "" {
		return "", err
	}
	return strings.TrimPrefix(raw, "0x"), nil
}

// LoadSecret 读取一段机密。环境变量优先；文件必须是 0600 或更严。两者都空时返回空串。
func LoadSecret(envName, filePath string) (string, error) {
	if envName != "" {
		if v := strings.TrimSpace(os.Getenv(envName)); v != "" {
			return v, nil
		}
	}
	if strings.TrimSpace(filePath) == "" {
		return "", nil
	}
	info, err := os.Stat(filePath)
	if err != nil {
		return "", err
	}
	perm := info.Mode().Perm()
	if perm&0o077 != 0 {
		return "", fmt.Errorf("secret file %s must not be readable by group or others (mode %o)", filePath, perm)
	}
	b, err := os.ReadFile(filePath)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}
