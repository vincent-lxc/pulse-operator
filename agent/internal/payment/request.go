// Package payment implements a deterministic invoice-to-PolicyVault pipeline.
// It does not claim to run a live AI model or hold a treasury key.
package payment

import (
	"encoding/json"
	"fmt"
	"math/big"
	"regexp"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

type Request struct {
	PaymentID  string `json:"payment_id"`
	Category   string `json:"category"`
	Payee      string `json:"payee"`
	AmountUSDC string `json:"amount_usdc"`
}

type Intent struct {
	PaymentID    string         `json:"payment_id"`
	ChainID      string         `json:"chain_id"`
	Vault        common.Address `json:"vault"`
	Agent        common.Address `json:"agent"`
	Category     [32]byte       `json:"category"`
	Payee        common.Address `json:"payee"`
	Amount       string         `json:"amount_units"`
	DecisionHash common.Hash    `json:"decision_hash"`
}

var decimal = regexp.MustCompile(`^(0|[1-9][0-9]*)(\.[0-9]{1,6})?$`)

func Prepare(r Request, chain *big.Int, vault, agent common.Address) (Intent, error) {
	if strings.TrimSpace(r.PaymentID) == "" || len(r.PaymentID) > 160 {
		return Intent{}, fmt.Errorf("payment_id required (max 160 bytes)")
	}
	if r.Category == "" || len([]byte(r.Category)) > 32 || strings.ContainsRune(r.Category, 0) {
		return Intent{}, fmt.Errorf("category must be 1..32 bytes without NUL")
	}
	if chain == nil || chain.Sign() <= 0 || vault == (common.Address{}) || agent == (common.Address{}) {
		return Intent{}, fmt.Errorf("chain, vault and agent required")
	}
	if !common.IsHexAddress(r.Payee) || common.HexToAddress(r.Payee) == (common.Address{}) {
		return Intent{}, fmt.Errorf("valid nonzero payee required")
	}
	amount, err := ParseUSDC(r.AmountUSDC)
	if err != nil {
		return Intent{}, err
	}
	in := Intent{PaymentID: r.PaymentID, ChainID: chain.String(), Vault: vault, Agent: agent, Payee: common.HexToAddress(r.Payee), Amount: amount.String()}
	copy(in.Category[:], []byte(r.Category))
	// Invoice identity is independent of run IDs, timestamps, model responses and
	// mutable payment terms. Reusing an invoice cannot mint a new on-chain identity.
	identity := struct {
		Domain string
		Chain  string
		Vault  common.Address
		ID     string
	}{"pulse-operator/payment/v1", in.ChainID, vault, r.PaymentID}
	b, _ := json.Marshal(identity)
	in.DecisionHash = crypto.Keccak256Hash(b)
	return in, nil
}

// ParseUSDC uses integer arithmetic with the Arc ERC-20 interface's 6 decimals.
func ParseUSDC(s string) (*big.Int, error) {
	if !decimal.MatchString(s) {
		return nil, fmt.Errorf("amount_usdc must be a positive decimal with at most 6 places")
	}
	parts := strings.SplitN(s, ".", 2)
	fraction := ""
	if len(parts) == 2 {
		fraction = parts[1]
	}
	n, ok := new(big.Int).SetString(parts[0]+fraction+strings.Repeat("0", 6-len(fraction)), 10)
	if !ok || n.Sign() <= 0 || n.BitLen() > 256 {
		return nil, fmt.Errorf("amount_usdc out of uint256 range")
	}
	return n, nil
}
