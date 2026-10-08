package procurement

import (
	"github.com/ethereum/go-ethereum/crypto"
)

func mustABISelector(sig string) []byte {
	return crypto.Keccak256([]byte(sig))[:4]
}

// PendingCountCalldata 是 PolicyVault.pendingCount() 的调用数据，用来确认合约身份。
func PendingCountCalldata() []byte {
	return mustABISelector("pendingCount()")
}
