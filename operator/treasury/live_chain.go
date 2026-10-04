// 本文件通过 RPC 读取 PolicyVault，并在 live 模式下签名发送 pay / sweepToReserve。
// dry-run 会做 eth_call，不会广播。
package treasury

import (
	"context"
	"crypto/ecdsa"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"os"
	"reflect"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ethereum/go-ethereum/rpc"
)

// LiveChain 是 Arc 上的 PolicyVault 客户端。
type LiveChain struct {
	eth        *ethclient.Client
	mode       string
	chainID    *big.Int
	vault      common.Address
	agent      common.Address
	usdc       common.Address
	categories []string
	payees     map[string][]string
	fromBlock  uint64
	agentKey   *ecdsa.PrivateKey
	ownerKey   *ecdsa.PrivateKey
	gasPrice   *big.Int
	lookback   uint64
	chunk      uint64
	fullScan   bool
}

// LiveOptions 是 DialLive 的参数。密钥可以为空：dry-run 只读不需要它们。
type LiveOptions struct {
	RPC        string
	Mode       string
	ChainID    *big.Int
	Vault      common.Address
	Agent      common.Address
	USDC       common.Address
	Categories []string
	Payees     map[string][]string
	FromBlock  uint64
	AgentKey   *ecdsa.PrivateKey
	OwnerKey   *ecdsa.PrivateKey
	GasGwei    int64
	Lookback   uint64
	Chunk      uint64
	FullScan   bool
}

// DialLive 连接 RPC 并核对 chainId。
func DialLive(ctx context.Context, opt LiveOptions) (*LiveChain, error) {
	if opt.RPC == "" {
		return nil, fmt.Errorf("rpc url is empty")
	}
	if opt.GasGwei < 20 {
		return nil, fmt.Errorf("gas price must be at least 20 gwei")
	}
	eth, err := dialRPC(ctx, opt.RPC)
	if err != nil {
		return nil, err
	}
	id, err := eth.ChainID(ctx)
	if err != nil {
		eth.Close()
		return nil, err
	}
	if opt.ChainID != nil && id.Cmp(opt.ChainID) != 0 {
		eth.Close()
		return nil, fmt.Errorf("rpc chain id %s != config %s", id, opt.ChainID)
	}
	return &LiveChain{
		eth:        eth,
		mode:       opt.Mode,
		chainID:    id,
		vault:      opt.Vault,
		agent:      opt.Agent,
		usdc:       opt.USDC,
		categories: opt.Categories,
		payees:     opt.Payees,
		fromBlock:  opt.FromBlock,
		agentKey:   opt.AgentKey,
		ownerKey:   opt.OwnerKey,
		gasPrice:   new(big.Int).Mul(big.NewInt(opt.GasGwei), big.NewInt(1_000_000_000)),
		lookback:   opt.Lookback,
		chunk:      opt.Chunk,
		fullScan:   opt.FullScan,
	}, nil
}

func dialRPC(ctx context.Context, url string) (*ethclient.Client, error) {
	client, err := rpc.DialOptions(ctx, url, rpc.WithHTTPClient(&http.Client{
		Timeout:   30 * time.Second,
		Transport: &retryTransport{base: http.DefaultTransport},
	}))
	if err != nil {
		return nil, err
	}
	return ethclient.NewClient(client), nil
}

// Close 关闭 RPC 连接。
func (c *LiveChain) Close() { c.eth.Close() }

// ListPending 读取仍在链上等待的审批。
func (c *LiveChain) ListPending(ctx context.Context) ([]Approval, error) {
	return c.pending(ctx)
}

// ProbeOwner 以金库 owner 身份 eth_call，把回退解成自定义错误。
func (c *LiveChain) ProbeOwner(ctx context.Context, data []byte) error {
	owner, err := c.callAddress(ctx, "owner")
	if err != nil {
		return err
	}
	return c.probe(ctx, owner, data)
}

// ProbeAgent 以配置里的 agent 地址 eth_call。
func (c *LiveChain) ProbeAgent(ctx context.Context, data []byte) error {
	return c.probe(ctx, c.agent, data)
}

func (c *LiveChain) probe(ctx context.Context, from common.Address, data []byte) error {
	_, err := c.eth.CallContract(ctx, ethereum.CallMsg{From: from, To: &c.vault, Data: data}, nil)
	if err == nil {
		return nil
	}
	return AnnotateRevert(err)
}

func (c *LiveChain) withRevert(ctx context.Context, from common.Address, data []byte, err error) error {
	if err == nil {
		return nil
	}
	if rev := AnnotateRevert(err); RevertReason(rev) != "" {
		return rev
	}
	if from != (common.Address{}) && len(data) > 0 {
		if rev := c.probe(ctx, from, data); RevertReason(rev) != "" {
			return rev
		}
	}
	return err
}

// Observe 读取余额、品类、白名单、待审批和 USDC 转入。
func (c *LiveChain) Observe(ctx context.Context) (Snapshot, error) {
	balance, err := c.callUint(ctx, "balance")
	if err != nil {
		return Snapshot{}, err
	}
	paused, err := c.callBool(ctx, "paused")
	if err != nil {
		return Snapshot{}, err
	}
	reserve, err := c.callAddress(ctx, "reserve")
	if err != nil {
		return Snapshot{}, err
	}
	snap := Snapshot{
		Balance:    balance,
		Paused:     paused,
		Reserve:    reserve.Hex(),
		Categories: map[string]Category{},
	}
	for _, name := range c.categories {
		cat, err := c.readCategory(ctx, name)
		if err != nil {
			return Snapshot{}, err
		}
		for _, payee := range c.payees[name] {
			allowed, err := c.payeeAllowed(ctx, name, payee)
			if err != nil {
				return Snapshot{}, err
			}
			if cat.Payees == nil {
				cat.Payees = map[string]bool{}
			}
			cat.Payees[NormalizeAddress(payee)] = allowed
		}
		snap.Categories[name] = cat
	}
	pending, err := c.pending(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	snap.Pending = pending
	inflows, block, err := c.inflows(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	snap.Inflows = inflows
	snap.Block = block
	return snap, nil
}

// Pay 在 live 模式下发送交易；dry-run 只做 eth_call。
func (c *LiveChain) Pay(ctx context.Context, call PayCall) (ExecResult, error) {
	data, err := PackPay(call.Category, call.Payee, call.Amount, call.DecisionHash)
	if err != nil {
		return ExecResult{}, err
	}
	if c.mode != "live" {
		return c.simulatePay(ctx, data)
	}
	if c.agentKey == nil {
		return ExecResult{}, ErrAgentKeyRequired
	}
	if err := c.guardMainnet(); err != nil {
		return ExecResult{}, err
	}
	from := crypto.PubkeyToAddress(c.agentKey.PublicKey)
	tx, err := c.send(ctx, from, c.agentKey, data)
	if err != nil {
		return ExecResult{}, c.withRevert(ctx, from, data, err)
	}
	receipt, err := c.wait(ctx, tx.Hash())
	if err != nil {
		return ExecResult{}, c.withRevert(ctx, from, data, err)
	}
	res := ExecResult{TxHash: tx.Hash().Hex(), Calldata: "0x" + fmt.Sprintf("%x", data)}
	if paid, id, ok := decodePayReceipt(receipt, call.DecisionHash); ok {
		if paid {
			res.Status = "paid"
		} else {
			res.Status = "approval_requested"
			res.RequestID = id
		}
		return res, nil
	}
	return ExecResult{}, fmt.Errorf("receipt %s had no pay event", tx.Hash().Hex())
}

// Sweep 在 live 模式下用 owner 密钥发送；没有 owner 密钥时返回 ErrOwnerKeyRequired。
func (c *LiveChain) Sweep(ctx context.Context, call SweepCall) (ExecResult, error) {
	data, err := PackSweep(call.Amount, call.DecisionHash)
	if err != nil {
		return ExecResult{}, err
	}
	if c.mode != "live" {
		return c.simulate(ctx, "sweepToReserve", data, "dry_run")
	}
	if c.ownerKey == nil {
		return ExecResult{}, ErrOwnerKeyRequired
	}
	if err := c.guardMainnet(); err != nil {
		return ExecResult{}, err
	}
	from := crypto.PubkeyToAddress(c.ownerKey.PublicKey)
	tx, err := c.send(ctx, from, c.ownerKey, data)
	if err != nil {
		return ExecResult{}, c.withRevert(ctx, from, data, err)
	}
	if _, err := c.wait(ctx, tx.Hash()); err != nil {
		return ExecResult{}, c.withRevert(ctx, from, data, err)
	}
	return ExecResult{Status: "swept", TxHash: tx.Hash().Hex(), Calldata: "0x" + fmt.Sprintf("%x", data)}, nil
}

// Approve 由 owner 调用 PolicyVault.approve。dry-run 只做 eth_call。
func (c *LiveChain) Approve(ctx context.Context, requestID string) (ExecResult, error) {
	return c.ownerAction(ctx, requestID, "approve", "approved", "dry_run_approved", PackApprove)
}

// Reject 由 owner 调用 PolicyVault.reject。dry-run 只做 eth_call。
func (c *LiveChain) Reject(ctx context.Context, requestID string) (ExecResult, error) {
	return c.ownerAction(ctx, requestID, "reject", "rejected", "dry_run_rejected", PackReject)
}

func (c *LiveChain) ownerAction(ctx context.Context, requestID, method, liveStatus, dryStatus string, pack func(string) ([]byte, error)) (ExecResult, error) {
	data, err := pack(requestID)
	if err != nil {
		return ExecResult{}, err
	}
	if c.mode != "live" {
		res, err := c.simulate(ctx, method, data, dryStatus)
		res.RequestID = strings.TrimSpace(requestID)
		return res, err
	}
	if c.ownerKey == nil {
		return ExecResult{}, ErrOwnerKeyRequired
	}
	if err := c.guardMainnet(); err != nil {
		return ExecResult{}, err
	}
	from := crypto.PubkeyToAddress(c.ownerKey.PublicKey)
	tx, err := c.send(ctx, from, c.ownerKey, data)
	if err != nil {
		return ExecResult{}, c.withRevert(ctx, from, data, err)
	}
	if _, err := c.wait(ctx, tx.Hash()); err != nil {
		return ExecResult{}, c.withRevert(ctx, from, data, err)
	}
	return ExecResult{Status: liveStatus, TxHash: tx.Hash().Hex(), RequestID: strings.TrimSpace(requestID), Calldata: "0x" + fmt.Sprintf("%x", data)}, nil
}

func (c *LiveChain) guardMainnet() error {
	if c.chainID.Cmp(big.NewInt(5042)) == 0 && os.Getenv("CONFIRM_MAINNET") != "1" {
		return fmt.Errorf("refusing Arc mainnet without CONFIRM_MAINNET=1")
	}
	return nil
}

func (c *LiveChain) simulatePay(ctx context.Context, data []byte) (ExecResult, error) {
	res := ExecResult{Status: "dry_run", Calldata: "0x" + fmt.Sprintf("%x", data)}
	out, err := c.eth.CallContract(ctx, ethereum.CallMsg{From: c.agent, To: &c.vault, Data: data}, nil)
	if err != nil {
		res.Status = "dry_run_reverted"
		if rev := AnnotateRevert(err); RevertReason(rev) != "" {
			return res, rev
		}
		return res, fmt.Errorf("eth_call reverted: %s", err.Error())
	}
	values, err := contractABI.Unpack("pay", out)
	if err != nil || len(values) != 2 {
		res.Status = "dry_run_reverted"
		return res, fmt.Errorf("eth_call returned an unexpected pay result")
	}
	paid, _ := values[0].(bool)
	id, _ := values[1].(*big.Int)
	if paid {
		res.Status = "dry_run_paid"
	} else if id != nil {
		res.Status = "dry_run_approval"
		res.RequestID = id.String()
	}
	return res, nil
}

func (c *LiveChain) simulate(ctx context.Context, method string, data []byte, okStatus string) (ExecResult, error) {
	from := c.agent
	if method != "pay" {
		if c.ownerKey != nil {
			from = crypto.PubkeyToAddress(c.ownerKey.PublicKey)
		} else if owner, err := c.callAddress(ctx, "owner"); err == nil {
			from = owner
		}
	}
	res := ExecResult{Status: okStatus, Calldata: "0x" + fmt.Sprintf("%x", data)}
	_, err := c.eth.CallContract(ctx, ethereum.CallMsg{From: from, To: &c.vault, Data: data}, nil)
	if err != nil {
		res.Status = "dry_run_reverted"
		if rev := AnnotateRevert(err); RevertReason(rev) != "" {
			return res, rev
		}
		return res, fmt.Errorf("eth_call %s reverted: %s", method, err.Error())
	}
	return res, nil
}

func (c *LiveChain) send(ctx context.Context, from common.Address, key *ecdsa.PrivateKey, data []byte) (*types.Transaction, error) {
	nonce, err := c.eth.PendingNonceAt(ctx, from)
	if err != nil {
		return nil, err
	}
	msg := ethereum.CallMsg{From: from, To: &c.vault, Data: data}
	gas, err := c.eth.EstimateGas(ctx, msg)
	if err != nil {
		return nil, AnnotateRevert(err)
	}
	tx := types.NewTx(&types.LegacyTx{
		Nonce:    nonce,
		To:       &c.vault,
		Value:    big.NewInt(0),
		Gas:      gas + gas/5,
		GasPrice: c.gasPrice,
		Data:     data,
	})
	signed, err := types.SignTx(tx, types.LatestSignerForChainID(c.chainID), key)
	if err != nil {
		return nil, err
	}
	if err := c.eth.SendTransaction(ctx, signed); err != nil {
		return nil, err
	}
	return signed, nil
}

func (c *LiveChain) wait(ctx context.Context, hash common.Hash) (*types.Receipt, error) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		receipt, err := c.eth.TransactionReceipt(ctx, hash)
		if err == nil {
			if receipt.Status != types.ReceiptStatusSuccessful {
				return nil, fmt.Errorf("transaction reverted: %s", hash.Hex())
			}
			return receipt, nil
		}
		if !errors.Is(err, ethereum.NotFound) {
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}

func (c *LiveChain) callUint(ctx context.Context, name string) (*big.Int, error) {
	raw, err := c.call(ctx, name)
	if err != nil {
		return nil, err
	}
	values, err := contractABI.Unpack(name, raw)
	if err != nil || len(values) != 1 {
		return nil, fmt.Errorf("bad %s response", name)
	}
	n, ok := values[0].(*big.Int)
	if !ok {
		return nil, fmt.Errorf("bad %s type", name)
	}
	return n, nil
}

func (c *LiveChain) callBool(ctx context.Context, name string) (bool, error) {
	raw, err := c.call(ctx, name)
	if err != nil {
		return false, err
	}
	values, err := contractABI.Unpack(name, raw)
	if err != nil || len(values) != 1 {
		return false, fmt.Errorf("bad %s response", name)
	}
	b, ok := values[0].(bool)
	if !ok {
		return false, fmt.Errorf("bad %s type", name)
	}
	return b, nil
}

func (c *LiveChain) callAddress(ctx context.Context, name string) (common.Address, error) {
	raw, err := c.call(ctx, name)
	if err != nil {
		return common.Address{}, err
	}
	values, err := contractABI.Unpack(name, raw)
	if err != nil || len(values) != 1 {
		return common.Address{}, fmt.Errorf("bad %s response", name)
	}
	addr, ok := values[0].(common.Address)
	if !ok {
		return common.Address{}, fmt.Errorf("bad %s type", name)
	}
	return addr, nil
}

func (c *LiveChain) call(ctx context.Context, name string, args ...interface{}) ([]byte, error) {
	data, err := contractABI.Pack(name, args...)
	if err != nil {
		return nil, err
	}
	return c.eth.CallContract(ctx, ethereum.CallMsg{To: &c.vault, Data: data}, nil)
}

func (c *LiveChain) readCategory(ctx context.Context, name string) (Category, error) {
	word, err := CategoryWord(name)
	if err != nil {
		return Category{}, err
	}
	raw, err := c.call(ctx, "getCategory", word)
	if err != nil {
		return Category{}, err
	}
	values, err := contractABI.Unpack("getCategory", raw)
	if err != nil || len(values) != 1 {
		return Category{}, fmt.Errorf("bad getCategory response for %s", name)
	}
	return categoryFromABI(name, values[0])
}

func (c *LiveChain) payeeAllowed(ctx context.Context, category, payee string) (bool, error) {
	word, err := CategoryWord(category)
	if err != nil {
		return false, err
	}
	raw, err := c.call(ctx, "isPayeeAllowed", word, common.HexToAddress(payee))
	if err != nil {
		return false, err
	}
	values, err := contractABI.Unpack("isPayeeAllowed", raw)
	if err != nil || len(values) != 1 {
		return false, fmt.Errorf("bad isPayeeAllowed response")
	}
	allowed, ok := values[0].(bool)
	if !ok {
		return false, fmt.Errorf("bad isPayeeAllowed type")
	}
	return allowed, nil
}

func (c *LiveChain) pending(ctx context.Context) ([]Approval, error) {
	raw, err := c.call(ctx, "pendingRequestIds")
	if err != nil {
		return nil, err
	}
	values, err := contractABI.Unpack("pendingRequestIds", raw)
	if err != nil || len(values) != 1 {
		return nil, fmt.Errorf("bad pendingRequestIds response")
	}
	ids, ok := values[0].([]*big.Int)
	if !ok {
		return nil, fmt.Errorf("bad pendingRequestIds type")
	}
	out := make([]Approval, 0, len(ids))
	for _, id := range ids {
		raw, err := c.call(ctx, "getRequest", id)
		if err != nil {
			return nil, err
		}
		vals, err := contractABI.Unpack("getRequest", raw)
		if err != nil || len(vals) != 1 {
			return nil, fmt.Errorf("bad getRequest response")
		}
		item, err := approvalFromABI(id.String(), vals[0])
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, nil
}

func (c *LiveChain) inflows(ctx context.Context) ([]Inflow, uint64, error) {
	head, err := c.eth.BlockNumber(ctx)
	if err != nil {
		return nil, 0, err
	}
	from := c.scanFrom(head)
	sig := crypto.Keccak256Hash([]byte("Transfer(address,address,uint256)"))
	toTopic := common.BytesToHash(common.LeftPadBytes(c.vault.Bytes(), 32))
	logs, err := c.filterRange(ctx, from, head, sig, toTopic)
	if err != nil {
		return nil, head, err
	}
	out := make([]Inflow, 0, len(logs))
	for _, lg := range logs {
		if len(lg.Topics) < 3 || len(lg.Data) < 32 {
			continue
		}
		amount := new(big.Int).SetBytes(lg.Data[:32])
		out = append(out, Inflow{
			TxHash:  lg.TxHash.Hex(),
			From:    common.BytesToAddress(lg.Topics[1].Bytes()).Hex(),
			Amount:  amount,
			Block:   lg.BlockNumber,
			Source:  "chain",
			Product: ProductLocalRPC,
		})
	}
	return out, head, nil
}

func (c *LiveChain) scanFrom(head uint64) uint64 {
	from := c.fromBlock
	if from > head {
		from = head
	}
	if c.fullScan || c.lookback == 0 || head < c.lookback {
		if from == 0 {
			return head
		}
		return from
	}
	recent := head - c.lookback + 1
	if from == 0 || recent > from {
		return recent
	}
	return from
}

func (c *LiveChain) filterRange(ctx context.Context, from, head uint64, sig, toTopic common.Hash) ([]types.Log, error) {
	chunk := c.chunk
	if chunk == 0 {
		chunk = 9000
	}
	var all []types.Log
	for start := from; start <= head; {
		end := start + chunk - 1
		if end < start || end > head {
			end = head
		}
		logs, err := c.filterChunk(ctx, start, end, sig, toTopic)
		if err != nil {
			return nil, err
		}
		all = append(all, logs...)
		if end == head {
			break
		}
		start = end + 1
	}
	return all, nil
}

func (c *LiveChain) filterChunk(ctx context.Context, from, to uint64, sig, toTopic common.Hash) ([]types.Log, error) {
	logs, err := c.eth.FilterLogs(ctx, ethereum.FilterQuery{
		FromBlock: new(big.Int).SetUint64(from),
		ToBlock:   new(big.Int).SetUint64(to),
		Addresses: []common.Address{c.usdc},
		Topics:    [][]common.Hash{{sig}, nil, {toTopic}},
	})
	if err != nil && to > from && rangeTooLarge(err) {
		mid := from + (to-from)/2
		left, leftErr := c.filterChunk(ctx, from, mid, sig, toTopic)
		if leftErr != nil {
			return nil, leftErr
		}
		right, rightErr := c.filterChunk(ctx, mid+1, to, sig, toTopic)
		if rightErr != nil {
			return nil, rightErr
		}
		return append(left, right...), nil
	}
	return logs, err
}

func rangeTooLarge(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "too large") || strings.Contains(msg, "exceed") || strings.Contains(msg, "10k") || strings.Contains(msg, "10000")
}

func categoryFromABI(name string, value interface{}) (Category, error) {
	v := reflect.ValueOf(value)
	if v.Kind() == reflect.Pointer {
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return Category{}, fmt.Errorf("unexpected getCategory shape for %s", name)
	}
	cat := Category{
		Name:          name,
		Enabled:       boolField(v, "Enabled"),
		Budget:        bigField(v, "Budget"),
		PerTxCap:      bigField(v, "PerTxCap"),
		Spent:         bigField(v, "Spent"),
		Remaining:     bigField(v, "Remaining"),
		PeriodSeconds: uintField(v, "Period"),
		Payees:        map[string]bool{},
	}
	return cat, nil
}

func approvalFromABI(id string, value interface{}) (Approval, error) {
	v := reflect.ValueOf(value)
	if v.Kind() == reflect.Pointer {
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return Approval{}, fmt.Errorf("unexpected getRequest shape")
	}
	payee := addressField(v, "Payee")
	hash := hashField(v, "DecisionHash")
	return Approval{
		RequestID:    id,
		Category:     wordField(v, "Category"),
		Payee:        payee,
		Amount:       bigField(v, "Amount"),
		DecisionHash: hash,
		Reason:       reasonLabel(uintField(v, "Reason")),
		Status:       statusName(uintField(v, "Status")),
	}, nil
}

// ExplainPay 读取已广播交易的回执，区分直接支付和待审批。
func (c *LiveChain) ExplainPay(ctx context.Context, txHash string, decision common.Hash) (string, string, error) {
	hash := common.HexToHash(txHash)
	receipt, err := c.wait(ctx, hash)
	if err != nil {
		return "", "", err
	}
	paid, id, ok := decodePayReceipt(receipt, decision)
	if !ok {
		return "", "", fmt.Errorf("receipt %s had no pay event", hash.Hex())
	}
	if paid {
		return "paid", "", nil
	}
	return "approval_requested", id, nil
}

func decodePayReceipt(receipt *types.Receipt, decision common.Hash) (bool, string, bool) {
	for _, lg := range receipt.Logs {
		if lg == nil || len(lg.Topics) == 0 {
			continue
		}
		switch lg.Topics[0] {
		case contractABI.Events["AgentPaid"].ID:
			if len(lg.Topics) >= 4 && lg.Topics[3] == decision {
				return true, "", true
			}
		case contractABI.Events["ApprovalRequested"].ID:
			values, err := contractABI.Events["ApprovalRequested"].Inputs.NonIndexed().Unpack(lg.Data)
			if err != nil || len(values) < 3 {
				continue
			}
			raw, ok := values[2].([32]byte)
			if !ok || common.Hash(raw) != decision || len(lg.Topics) < 2 {
				continue
			}
			return false, new(big.Int).SetBytes(lg.Topics[1].Bytes()).String(), true
		}
	}
	return false, "", false
}

func boolField(v reflect.Value, name string) bool {
	f := v.FieldByName(name)
	if f.IsValid() && f.Kind() == reflect.Bool {
		return f.Bool()
	}
	return false
}

func bigField(v reflect.Value, name string) *big.Int {
	f := v.FieldByName(name)
	if !f.IsValid() || f.IsNil() {
		return big.NewInt(0)
	}
	n, ok := f.Interface().(*big.Int)
	if !ok || n == nil {
		return big.NewInt(0)
	}
	return n
}

func uintField(v reflect.Value, name string) uint64 {
	f := v.FieldByName(name)
	if !f.IsValid() {
		return 0
	}
	switch f.Kind() {
	case reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return f.Uint()
	default:
		return 0
	}
}

func addressField(v reflect.Value, name string) string {
	f := v.FieldByName(name)
	if !f.IsValid() {
		return ""
	}
	if addr, ok := f.Interface().(common.Address); ok {
		return addr.Hex()
	}
	return ""
}

func hashField(v reflect.Value, name string) string {
	f := v.FieldByName(name)
	if !f.IsValid() {
		return ""
	}
	if h, ok := f.Interface().([32]byte); ok {
		return common.Hash(h).Hex()
	}
	return ""
}

func wordField(v reflect.Value, name string) string {
	f := v.FieldByName(name)
	if !f.IsValid() {
		return ""
	}
	b, ok := f.Interface().([32]byte)
	if !ok {
		return ""
	}
	return strings.TrimRight(string(b[:]), "\x00")
}

func reasonLabel(v uint64) string {
	switch v {
	case 1:
		return ReasonOverTxCap
	case 2:
		return ReasonOverBudget
	case 0:
		return ""
	default:
		return fmt.Sprintf("%d", v)
	}
}

func statusName(v uint64) string {
	switch v {
	case 1:
		return "pending"
	case 2:
		return "approved"
	case 3:
		return "rejected"
	default:
		return "none"
	}
}
