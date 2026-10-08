// 本文件从环境变量或权限不宽于 0600 的文件读取十六进制私钥。
package treasury

import (
	"crypto/ecdsa"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/ethereum/go-ethereum/common"
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
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "{") {
		return hexFromJSON(raw)
	}
	return strings.TrimPrefix(raw, "0x"), nil
}

// hexFromJSON 接受 {"private_key":"0x..."}。有 address 时必须和私钥推出的地址一致。
// 错误文本不包含密钥内容。
func hexFromJSON(raw string) (string, error) {
	var body struct {
		Address    string `json:"address"`
		PrivateKey string `json:"private_key"`
	}
	if err := json.Unmarshal([]byte(raw), &body); err != nil {
		return "", fmt.Errorf("invalid private key file")
	}
	hexKey := strings.TrimPrefix(strings.TrimSpace(body.PrivateKey), "0x")
	key, err := crypto.HexToECDSA(hexKey)
	if err != nil {
		return "", fmt.Errorf("invalid private key")
	}
	if strings.TrimSpace(body.Address) != "" {
		got := crypto.PubkeyToAddress(key.PublicKey)
		if !common.IsHexAddress(body.Address) || common.HexToAddress(body.Address) != got {
			return "", fmt.Errorf("key file address does not match the private key")
		}
	}
	return hexKey, nil
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
