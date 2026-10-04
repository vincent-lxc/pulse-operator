// 本文件记住 USDC 日志已经扫到的区块，避免每次从部署块重扫。
package treasury

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

type scanCursorFile struct {
	Vault string `json:"vault"`
	Block uint64 `json:"block"`
}

// LoadScanCursor 读取上一次成功扫描的区块。文件不存在时返回 0。
func LoadScanCursor(path, vault string) (uint64, error) {
	if strings.TrimSpace(path) == "" {
		return 0, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, nil
		}
		return 0, err
	}
	var file scanCursorFile
	if err := json.Unmarshal(b, &file); err != nil {
		return 0, err
	}
	if !strings.EqualFold(file.Vault, vault) {
		return 0, nil
	}
	return file.Block, nil
}

// SaveScanCursor 写下一次扫描的起点（已扫到的区块）。
func SaveScanCursor(path, vault string, block uint64) error {
	if strings.TrimSpace(path) == "" || block == 0 {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	raw, err := json.Marshal(scanCursorFile{Vault: vault, Block: block})
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(raw, '\n'), 0o644)
}
