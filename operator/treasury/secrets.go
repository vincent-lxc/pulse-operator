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
	if envName != "" {
		if v := strings.TrimSpace(os.Getenv(envName)); v != "" {
			return strings.TrimPrefix(v, "0x"), nil
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
		return "", fmt.Errorf("key file %s must not be readable by group or others (mode %o)", filePath, perm)
	}
	b, err := os.ReadFile(filePath)
	if err != nil {
		return "", err
	}
	return strings.TrimPrefix(strings.TrimSpace(string(b)), "0x"), nil
}
