// 本文件把 PolicyVault 的自定义回退解成可读原因。
package treasury

import (
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
)

// RevertError 是已经解开的合约回退。调用方应把它变成 4xx，而不是 500。
type RevertError struct {
	Reason string
}

func (e *RevertError) Error() string {
	if e == nil {
		return ""
	}
	return e.Reason
}

// AnnotateRevert 在错误里带了回退数据时，换成 RevertError。解不开就原样返回。
func AnnotateRevert(err error) error {
	if err == nil {
		return nil
	}
	if _, ok := err.(*RevertError); ok {
		return err
	}
	if reason, ok := DecodeRevertData(revertBytes(err)); ok {
		return &RevertError{Reason: reason}
	}
	return err
}

// RevertReason 返回已解开的原因。普通错误返回空字符串。
func RevertReason(err error) string {
	var rev *RevertError
	if ok := asRevert(err, &rev); ok {
		return rev.Reason
	}
	return ""
}

func asRevert(err error, dest **RevertError) bool {
	for err != nil {
		if rev, ok := err.(*RevertError); ok {
			*dest = rev
			return true
		}
		unwrap, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = unwrap.Unwrap()
	}
	return false
}

// DecodeRevertData 按 ABI 解自定义错误。data 可以带 0x 前缀。
func DecodeRevertData(data []byte) (string, bool) {
	if decoded := trimRevert(data); decoded != nil {
		data = decoded
	}
	if len(data) < 4 {
		return "", false
	}
	if reason, err := abi.UnpackRevert(data); err == nil && reason != "" {
		return reason, true
	}
	selector := data[:4]
	for name, item := range contractABI.Errors {
		if len(item.ID) < 4 || !bytesEqual(item.ID[:4], selector) {
			continue
		}
		vals, err := item.Inputs.Unpack(data[4:])
		if err != nil {
			return name, true
		}
		return formatRevert(name, vals), true
	}
	return "", false
}

func formatRevert(name string, vals []interface{}) string {
	if len(vals) == 0 {
		return name
	}
	parts := make([]string, len(vals))
	for i, v := range vals {
		parts[i] = formatABIValue(v)
	}
	return name + "(" + strings.Join(parts, ", ") + ")"
}

func formatABIValue(v interface{}) string {
	switch n := v.(type) {
	case *big.Int:
		if n == nil {
			return "0"
		}
		return n.String()
	case common.Address:
		return n.Hex()
	case common.Hash:
		return n.Hex()
	case [32]byte:
		return "0x" + hex.EncodeToString(n[:])
	case []byte:
		return "0x" + hex.EncodeToString(n)
	default:
		return fmt.Sprint(v)
	}
}

type dataError interface {
	ErrorData() interface{}
}

func revertBytes(err error) []byte {
	if err == nil {
		return nil
	}
	if de, ok := err.(dataError); ok {
		if raw := bytesOf(de.ErrorData()); len(raw) > 0 {
			return raw
		}
	}
	return bytesOf(err.Error())
}

func bytesOf(v interface{}) []byte {
	switch n := v.(type) {
	case nil:
		return nil
	case []byte:
		if decoded := trimRevert(n); decoded != nil {
			return decoded
		}
		if len(n) >= 4 {
			return n
		}
		return nil
	case string:
		return trimRevert([]byte(n))
	default:
		return trimRevert([]byte(fmt.Sprint(n)))
	}
}

func trimRevert(raw []byte) []byte {
	text := string(raw)
	if i := strings.LastIndex(text, "0x"); i >= 0 {
		text = text[i+2:]
	} else {
		text = strings.TrimPrefix(strings.TrimSpace(text), "0x")
	}
	text = strings.TrimSpace(text)
	if len(text) < 8 || len(text)%2 != 0 {
		return nil
	}
	for _, c := range text {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return nil
		}
	}
	out, err := hex.DecodeString(text)
	if err != nil {
		return nil
	}
	return out
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
