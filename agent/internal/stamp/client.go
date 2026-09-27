package stamp

import (
	"context"
	"crypto/ecdsa"
	"encoding/hex"
	"fmt"
	"math/big"
	"os"
	"strings"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
)

const (
	DefaultRPC      = "https://testnet-rpc.monad.xyz"
	DefaultChainID  = 10143
	DefaultContract = "0x6eC692C5792AD122c459AC8Df3FDF67eE80842F4"
)

// Config is filled from env. Dry-run is the default / CI path.
type Config struct {
	RPC        string
	ChainID    int64
	Contract   common.Address
	PrivateKey *ecdsa.PrivateKey
	DryRun     bool
}

type Result struct {
	Calldata             string `json:"calldata"`
	To                   string `json:"to"`
	DryRun               bool   `json:"dry_run"`
	TxHash               string `json:"tx_hash,omitempty"`
	ExplorerURL          string `json:"explorer_url,omitempty"`
	ReceiptID            string `json:"receipt_id,omitempty"`
	OnchainDecisionHash  string `json:"onchain_decision_hash,omitempty"`
	HashMatch            bool   `json:"hash_match,omitempty"`
	ReadbackErr          string `json:"readback_err,omitempty"`
}

func FromEnv() (Config, error) {
	rpc := getenv("MONAD_RPC_URL", DefaultRPC)
	var chain int64 = DefaultChainID
	if v := os.Getenv("MONAD_CHAIN_ID"); v != "" {
		var n int64
		if _, err := fmt.Sscanf(v, "%d", &n); err == nil && n > 0 {
			chain = n
		}
	}
	addr := getenv("PULSE_TRADE_STAMP", DefaultContract)
	if !common.IsHexAddress(addr) {
		return Config{}, fmt.Errorf("stamp: invalid PULSE_TRADE_STAMP %q", addr)
	}
	dry := true
	if v := strings.ToLower(strings.TrimSpace(os.Getenv("PULSE_DRY_RUN"))); v == "false" || v == "0" || v == "no" {
		dry = false
	}
	if WantLiveFromEnv() {
		dry = false
	}
	cfg := Config{
		RPC:      rpc,
		ChainID:  chain,
		Contract: common.HexToAddress(addr),
		DryRun:   dry,
	}
	if pk := strings.TrimSpace(os.Getenv("PRIVATE_KEY")); pk != "" {
		key, err := crypto.HexToECDSA(strings.TrimPrefix(pk, "0x"))
		if err != nil {
			return Config{}, fmt.Errorf("stamp: PRIVATE_KEY: %w", err)
		}
		cfg.PrivateKey = key
	}
	return cfg, nil
}

func getenv(k, def string) string {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		return v
	}
	return def
}

// Stamp encodes calldata and either returns it (dry-run) or broadcasts on Monad.
func (c Config) Stamp(ctx context.Context, req Request) (*Result, error) {
	data, err := EncodeCalldata(req)
	if err != nil {
		return nil, err
	}
	out := &Result{
		Calldata: Hex(data),
		To:       c.Contract.Hex(),
		DryRun:   c.DryRun,
	}
	if c.DryRun {
		return out, nil
	}
	if c.PrivateKey == nil {
		return nil, fmt.Errorf("stamp: live send requires PRIVATE_KEY (use dry-run, or set --live / PULSE_STAMP_LIVE=1 with a funded testnet key)")
	}
	if strings.TrimSpace(c.RPC) == "" {
		return nil, fmt.Errorf("stamp: live send requires MONAD_RPC_URL")
	}
	if (c.Contract == common.Address{}) {
		return nil, fmt.Errorf("stamp: live send requires PULSE_TRADE_STAMP")
	}

	client, err := ethclient.DialContext(ctx, c.RPC)
	if err != nil {
		return nil, fmt.Errorf("stamp: rpc: %w", err)
	}
	defer client.Close()

	onchainID, err := client.ChainID(ctx)
	if err != nil {
		return nil, fmt.Errorf("stamp: chain id: %w", err)
	}
	if c.ChainID != 0 && onchainID.Int64() != c.ChainID {
		return nil, fmt.Errorf("stamp: RPC chain id %s != configured %d", onchainID, c.ChainID)
	}

	from := crypto.PubkeyToAddress(c.PrivateKey.PublicKey)
	nonce, err := client.PendingNonceAt(ctx, from)
	if err != nil {
		return nil, err
	}
	gasPrice, err := client.SuggestGasPrice(ctx)
	if err != nil {
		return nil, err
	}
	gas := uint64(250_000)
	if est, err := client.EstimateGas(ctx, ethereum.CallMsg{From: from, To: &c.Contract, Data: data}); err == nil && est > 0 {
		gas = est + est/5
	}
	tx := types.NewTransaction(nonce, c.Contract, big.NewInt(0), gas, gasPrice, data)
	signer := types.LatestSignerForChainID(onchainID)
	signed, err := types.SignTx(tx, signer, c.PrivateKey)
	if err != nil {
		return nil, err
	}
	if err := client.SendTransaction(ctx, signed); err != nil {
		return nil, fmt.Errorf("stamp: send: %w", err)
	}
	out.TxHash = signed.Hash().Hex()
	out.ExplorerURL = ExplorerTxURL(out.TxHash)

	// Stamp already landed. Readback must never panic or fail the send.
	fillReadback(ctx, client, c.Contract, signed.Hash(), req.DecisionHash, out)
	return out, nil
}

func fillReadback(ctx context.Context, client *ethclient.Client, to common.Address, txHash common.Hash, want [32]byte, out *Result) {
	defer func() {
		if r := recover(); r != nil {
			out.ReadbackErr = fmt.Sprintf("stamp: readback panic: %v", r)
		}
	}()
	rcpt, err := waitReceipt(ctx, client, txHash)
	if err != nil {
		out.ReadbackErr = err.Error()
		return
	}
	if rcpt.Status == 0 {
		out.ReadbackErr = "stamp: transaction reverted"
		return
	}
	id, err := ParseStampedID(rcpt.Logs)
	if err != nil {
		out.ReadbackErr = err.Error()
		return
	}
	out.ReceiptID = id.String()

	on, err := callGetReceipt(ctx, client, to, id)
	if err != nil {
		out.ReadbackErr = err.Error()
		return
	}
	out.OnchainDecisionHash = "0x" + hex.EncodeToString(on.DecisionHash[:])
	out.HashMatch = on.DecisionHash == want
}
