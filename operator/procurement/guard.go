// 本文件是主网付款前的全部闸门。少任何一项都拒绝，而且不给出替代的付款路径。
package procurement

import (
	"fmt"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/common"
)

// Flags 是 CLI 显式带上的确认。
type Flags struct {
	UnderstandRealMoney bool
	Yes                 bool
	Auto                bool
	BillCount           int
}

// Gate 是一次运行已经核对过的链上事实。
type Gate struct {
	Mode                  string
	ArcChainID            *big.Int
	BaseChainID           *big.Int
	Vault                 string
	VaultCodeOK           bool
	Procurement           string
	BaseProcurement       string
	BaseAddressControlled bool
	PorkbunKey            bool
	MaxSpend              *big.Int
	MaxBill               *big.Int
	ConfirmMainnetEnv     bool
}

// Check 检查模式闸门。dry-run 始终通过。testnet 只核对链和金库代码。mainnet 要全部条件。
func Check(g Gate, flags Flags) error {
	switch g.Mode {
	case "", "dry-run":
		return nil
	case "testnet", "live":
		return checkTestnet(g)
	case "mainnet":
		return checkMainnet(g, flags)
	default:
		return fmt.Errorf("unknown mode %s", g.Mode)
	}
}

func checkTestnet(g Gate) error {
	if !eqChain(g.ArcChainID, ArcTestnetChainID) {
		return fmt.Errorf("testnet requires Arc chain id %d", ArcTestnetChainID)
	}
	if g.BaseChainID != nil && !eqChain(g.BaseChainID, BaseSepoliaChainID) {
		return fmt.Errorf("testnet requires Base Sepolia chain id %d", BaseSepoliaChainID)
	}
	if sameAddr(g.Vault, TestnetPolicyVault) {
		return nil
	}
	if !g.VaultCodeOK {
		return fmt.Errorf("testnet vault did not answer a PolicyVault view")
	}
	return nil
}

func checkMainnet(g Gate, flags Flags) error {
	if !flags.UnderstandRealMoney {
		return fmt.Errorf("mainnet requires --i-understand-real-money")
	}
	if !g.ConfirmMainnetEnv {
		return fmt.Errorf("mainnet requires CONFIRM_MAINNET=1")
	}
	if !eqChain(g.ArcChainID, ArcMainnetChainID) {
		return fmt.Errorf("mainnet requires Arc chain id %d", ArcMainnetChainID)
	}
	if !eqChain(g.BaseChainID, BaseChainID) {
		return fmt.Errorf("mainnet requires Base chain id %d", BaseChainID)
	}
	if sameAddr(g.Vault, TestnetPolicyVault) {
		return fmt.Errorf("refusing %s on Arc mainnet; that address is PulseReceipt, not PolicyVault", TestnetPolicyVault)
	}
	if !g.VaultCodeOK {
		return fmt.Errorf("vault code check failed; pendingRequestIds did not answer")
	}
	if g.MaxSpend == nil || g.MaxSpend.Sign() <= 0 || g.MaxBill == nil || g.MaxBill.Sign() <= 0 {
		return fmt.Errorf("mainnet requires maxSpendPerRunUSDC and maxBillUSDC")
	}
	if !g.PorkbunKey {
		return fmt.Errorf("mainnet requires a Porkbun live API key")
	}
	if strings.TrimSpace(g.Procurement) == "" {
		return fmt.Errorf("mainnet requires the procurement wallet address")
	}
	baseAddr := g.BaseProcurement
	if baseAddr == "" {
		baseAddr = g.Procurement
	}
	if !sameAddr(baseAddr, g.Procurement) && !g.BaseAddressControlled {
		return fmt.Errorf("configured Base procurement address is not controlled by the signer")
	}
	if flags.Auto {
		return nil
	}
	if flags.Yes && flags.BillCount == 1 {
		return nil
	}
	if flags.Yes && flags.BillCount > 1 {
		return fmt.Errorf("mainnet --yes confirms one bill; use --id or --auto with the spend caps")
	}
	return fmt.Errorf("mainnet requires --yes for one bill or --auto with the spend caps")
}

func eqChain(got *big.Int, want int64) bool {
	return got != nil && got.Cmp(big.NewInt(want)) == 0
}

func sameAddr(a, b string) bool {
	if !common.IsHexAddress(a) || !common.IsHexAddress(b) {
		return false
	}
	return common.HexToAddress(a) == common.HexToAddress(b)
}
