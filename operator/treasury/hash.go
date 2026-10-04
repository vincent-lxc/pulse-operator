// 本文件把决策记录编码成稳定 JSON，并用 keccak256 得到 decisionHash。
package treasury

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

// Canonical 是上链 decisionHash 所覆盖的字段，不含交易哈希和执行结果。
type Canonical struct {
	V           int    `json:"v"`
	AgentID     string `json:"agent_id"`
	ChainID     string `json:"chain_id"`
	Vault       string `json:"vault"`
	PayableID   string `json:"payable_id"`
	Action      string `json:"action"`
	Category    string `json:"category"`
	Payee       string `json:"payee"`
	AmountUnits string `json:"amount_units"`
	ReasonCode  string `json:"reason_code"`
}

// CanonicalBytes 返回不含换行的确定性 JSON。
func CanonicalBytes(c Canonical) ([]byte, error) {
	c.Vault = NormalizeAddress(c.Vault)
	if c.Payee != "" && c.Payee != "cycle" {
		if common.IsHexAddress(c.Payee) {
			c.Payee = NormalizeAddress(c.Payee)
		}
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(c); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}

// DecisionHash 返回 0x 前缀的 keccak256。
func DecisionHash(c Canonical) (string, error) {
	b, err := CanonicalBytes(c)
	if err != nil {
		return "", err
	}
	sum := crypto.Keccak256(b)
	return "0x" + fmt.Sprintf("%x", sum), nil
}

// Hash32 把 0x 哈希转成定长字节。
func Hash32(hexHash string) (common.Hash, error) {
	raw := strings.TrimPrefix(strings.TrimSpace(hexHash), "0x")
	if len(raw) != 64 {
		return common.Hash{}, fmt.Errorf("decision hash must be 32 bytes")
	}
	return common.HexToHash(hexHash), nil
}

// NormalizeAddress 返回 EIP-55 校验和地址；空串保持为空。
func NormalizeAddress(addr string) string {
	if strings.TrimSpace(addr) == "" {
		return ""
	}
	return common.HexToAddress(addr).Hex()
}

// CategoryWord 把不超过 32 字节的品类名右填充成 bytes32。
func CategoryWord(name string) ([32]byte, error) {
	b := []byte(name)
	if len(b) == 0 || len(b) > 32 || strings.ContainsRune(name, 0) {
		return [32]byte{}, fmt.Errorf("category must be 1..32 bytes without NUL")
	}
	var out [32]byte
	copy(out[:], b)
	return out, nil
}
