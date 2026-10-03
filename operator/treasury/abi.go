// 本文件编码 PolicyVault 的 pay 与 sweepToReserve 调用数据。
package treasury

import (
	"fmt"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
)

const vaultABIJSON = `[
 {"type":"function","name":"pay","stateMutability":"nonpayable","inputs":[{"name":"category","type":"bytes32"},{"name":"payee","type":"address"},{"name":"amount","type":"uint256"},{"name":"decisionHash","type":"bytes32"}],"outputs":[{"name":"paid","type":"bool"},{"name":"requestId","type":"uint256"}]},
 {"type":"function","name":"sweepToReserve","stateMutability":"nonpayable","inputs":[{"name":"amount","type":"uint256"},{"name":"decisionHash","type":"bytes32"}],"outputs":[]},
 {"type":"function","name":"balance","stateMutability":"view","inputs":[],"outputs":[{"name":"","type":"uint256"}]},
 {"type":"function","name":"paused","stateMutability":"view","inputs":[],"outputs":[{"name":"","type":"bool"}]},
 {"type":"function","name":"reserve","stateMutability":"view","inputs":[],"outputs":[{"name":"","type":"address"}]},
 {"type":"function","name":"getCategory","stateMutability":"view","inputs":[{"name":"category","type":"bytes32"}],"outputs":[{"name":"v","type":"tuple","components":[{"name":"enabled","type":"bool"},{"name":"budget","type":"uint256"},{"name":"perTxCap","type":"uint256"},{"name":"period","type":"uint64"},{"name":"epoch","type":"uint64"},{"name":"epochStart","type":"uint64"},{"name":"epochEnd","type":"uint64"},{"name":"spent","type":"uint256"},{"name":"autoSpent","type":"uint256"},{"name":"approvedSpent","type":"uint256"},{"name":"remaining","type":"uint256"}]}]},
 {"type":"function","name":"isPayeeAllowed","stateMutability":"view","inputs":[{"name":"category","type":"bytes32"},{"name":"payee","type":"address"}],"outputs":[{"name":"","type":"bool"}]},
 {"type":"function","name":"pendingRequestIds","stateMutability":"view","inputs":[],"outputs":[{"name":"","type":"uint256[]"}]},
 {"type":"function","name":"getRequest","stateMutability":"view","inputs":[{"name":"requestId","type":"uint256"}],"outputs":[{"name":"","type":"tuple","components":[{"name":"category","type":"bytes32"},{"name":"payee","type":"address"},{"name":"amount","type":"uint256"},{"name":"decisionHash","type":"bytes32"},{"name":"createdAt","type":"uint64"},{"name":"reason","type":"uint8"},{"name":"status","type":"uint8"}]}]},
 {"type":"function","name":"decisionUsed","stateMutability":"view","inputs":[{"name":"decisionHash","type":"bytes32"}],"outputs":[{"name":"","type":"bool"}]},
 {"type":"event","name":"AgentPaid","inputs":[{"name":"category","type":"bytes32","indexed":true},{"name":"payee","type":"address","indexed":true},{"name":"amount","type":"uint256"},{"name":"epoch","type":"uint64"},{"name":"decisionHash","type":"bytes32","indexed":true}]},
 {"type":"event","name":"ApprovalRequested","inputs":[{"name":"requestId","type":"uint256","indexed":true},{"name":"category","type":"bytes32","indexed":true},{"name":"payee","type":"address","indexed":true},{"name":"amount","type":"uint256"},{"name":"reason","type":"uint8"},{"name":"decisionHash","type":"bytes32"}]},
 {"type":"event","name":"SweptToReserve","inputs":[{"name":"reserve","type":"address","indexed":true},{"name":"amount","type":"uint256"},{"name":"decisionHash","type":"bytes32","indexed":true}]}
]`

var contractABI = mustContractABI()

func mustContractABI() abi.ABI {
	a, err := abi.JSON(strings.NewReader(vaultABIJSON))
	if err != nil {
		panic(err)
	}
	return a
}

// PackPay 编码 pay(category, payee, amount, decisionHash)。
func PackPay(category, payee string, amount *big.Int, decision common.Hash) ([]byte, error) {
	word, err := CategoryWord(category)
	if err != nil {
		return nil, err
	}
	if !common.IsHexAddress(payee) {
		return nil, fmt.Errorf("payee is not an address")
	}
	return contractABI.Pack("pay", word, common.HexToAddress(payee), unitsOrZero(amount), decision)
}

// PackSweep 编码 sweepToReserve(amount, decisionHash)。
func PackSweep(amount *big.Int, decision common.Hash) ([]byte, error) {
	return contractABI.Pack("sweepToReserve", unitsOrZero(amount), decision)
}
