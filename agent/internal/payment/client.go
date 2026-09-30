package payment

import (
	"context"
	"fmt"
	"math/big"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ethereum/go-ethereum/rpc"
)

const vaultABI = `[
 {"type":"function","name":"pay","inputs":[{"type":"bytes32"},{"type":"address"},{"type":"uint256"},{"type":"bytes32"}],"outputs":[{"type":"bool"},{"type":"uint256"}]},
 {"type":"function","name":"decisionUsed","inputs":[{"type":"bytes32"}],"outputs":[{"type":"bool"}],"stateMutability":"view"},
 {"type":"event","name":"AgentPaid","inputs":[{"name":"category","type":"bytes32","indexed":true},{"name":"payee","type":"address","indexed":true},{"name":"amount","type":"uint256"},{"name":"epoch","type":"uint64"},{"name":"decisionHash","type":"bytes32","indexed":true}]},
 {"type":"event","name":"ApprovalRequested","inputs":[{"name":"requestId","type":"uint256","indexed":true},{"name":"category","type":"bytes32","indexed":true},{"name":"payee","type":"address","indexed":true},{"name":"amount","type":"uint256"},{"name":"reason","type":"uint8"},{"name":"decisionHash","type":"bytes32"}]}
]`

var contractABI = mustABI()

func mustABI() abi.ABI {
	a, err := abi.JSON(strings.NewReader(vaultABI))
	if err != nil {
		panic(err)
	}
	return a
}

func Calldata(in Intent) ([]byte, error) {
	n, ok := new(big.Int).SetString(in.Amount, 10)
	if !ok || n.Sign() <= 0 {
		return nil, fmt.Errorf("invalid amount units")
	}
	return contractABI.Pack("pay", in.Category, in.Payee, n, [32]byte(in.DecisionHash))
}

type Result struct {
	Status       string      `json:"status"` // paid | approval_requested | dry_run
	DecisionHash common.Hash `json:"decision_hash"`
	TxHash       common.Hash `json:"tx_hash,omitempty"`
	RequestID    string      `json:"request_id,omitempty"`
	Recovered    bool        `json:"recovered"`
	Calldata     string      `json:"calldata,omitempty"`
}

type Backend interface {
	Recover(context.Context, Intent) (*Result, error)
	Send(context.Context, Intent) (common.Hash, error)
	Wait(context.Context, Intent, common.Hash) (*Result, error)
}

// LocalClient deliberately has no key/signer and only sends from an unlocked
// local Anvil account. Arc support here is ABI encoding; public-chain sending
// is unavailable until separately implemented and reviewed.
type LocalClient struct {
	rpc       *rpc.Client
	eth       *ethclient.Client
	fromBlock uint64
}

func LocalEndpoint(endpoint string) bool {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "http" || u.User != nil || u.RawQuery != "" {
		return false
	}
	host := u.Hostname()
	ip := net.ParseIP(host)
	return (host == "127.0.0.1" || host == "::1") && ip != nil && ip.IsLoopback() && u.Port() != ""
}

func DialLocal(ctx context.Context, endpoint string, fromBlock uint64) (*LocalClient, error) {
	if !LocalEndpoint(endpoint) {
		return nil, fmt.Errorf("send-local requires numeric loopback HTTP RPC with explicit port")
	}
	// Disable proxy/redirection: do not allow an HTTP redirect to turn a loopback
	// request into a public-chain write. API: go-ethereum rpc.WithHTTPClient.
	raw, err := rpc.DialOptions(ctx, endpoint, rpc.WithHTTPClient(localHTTPClient()))
	if err != nil {
		return nil, err
	}
	c := &LocalClient{rpc: raw, eth: ethclient.NewClient(raw), fromBlock: fromBlock}
	chain, err := c.eth.ChainID(ctx)
	if err != nil {
		raw.Close()
		return nil, fmt.Errorf("local RPC chain lookup: %w", err)
	}
	if chain.Cmp(big.NewInt(31337)) != 0 {
		raw.Close()
		return nil, fmt.Errorf("send-local requires chain 31337, got %s", chain)
	}
	return c, nil
}
func (c *LocalClient) Close() { c.rpc.Close() }

func (c *LocalClient) check(ctx context.Context, in Intent) error {
	chain, err := c.eth.ChainID(ctx)
	if err != nil {
		return err
	}
	if chain.String() != in.ChainID || chain.Cmp(big.NewInt(31337)) != 0 {
		return fmt.Errorf("local chain mismatch")
	}
	code, err := c.eth.CodeAt(ctx, in.Vault, nil)
	if err != nil {
		return err
	}
	if len(code) == 0 {
		return fmt.Errorf("vault has no contract code")
	}
	return nil
}
func (c *LocalClient) Recover(ctx context.Context, in Intent) (*Result, error) {
	if err := c.check(ctx, in); err != nil {
		return nil, err
	}
	data, err := contractABI.Pack("decisionUsed", [32]byte(in.DecisionHash))
	if err != nil {
		return nil, err
	}
	out, err := c.eth.CallContract(ctx, ethereum.CallMsg{To: &in.Vault, Data: data}, nil)
	if err != nil {
		return nil, err
	}
	values, err := contractABI.Unpack("decisionUsed", out)
	if err != nil {
		return nil, err
	}
	if len(values) != 1 {
		return nil, fmt.Errorf("invalid decisionUsed response")
	}
	if !values[0].(bool) {
		return nil, nil
	}
	// Recovery uses the authoritative event, not a local completed flag. This
	// works after losing the journal or dying between mining and journal fsync.
	logs, err := c.eth.FilterLogs(ctx, ethereum.FilterQuery{FromBlock: new(big.Int).SetUint64(c.fromBlock), Addresses: []common.Address{in.Vault}, Topics: [][]common.Hash{{contractABI.Events["AgentPaid"].ID, contractABI.Events["ApprovalRequested"].ID}}})
	if err != nil {
		return nil, err
	}
	for _, log := range logs {
		result, matched, err := decodeEvent(in, log)
		if err != nil {
			return nil, err
		}
		if matched {
			result.Recovered = true
			return result, nil
		}
	}
	return nil, fmt.Errorf("decision consumed but event unavailable; refusing resend")
}

// eth_sendTransaction uses Anvil's unlocked development accounts only. No
// PRIVATE_KEY variable or wallet file is inspected.
// https://pkg.go.dev/github.com/ethereum/go-ethereum@v1.14.13/rpc#Client.CallContext
func (c *LocalClient) Send(ctx context.Context, in Intent) (common.Hash, error) {
	if err := c.check(ctx, in); err != nil {
		return common.Hash{}, err
	}
	data, err := Calldata(in)
	if err != nil {
		return common.Hash{}, err
	}
	msg := ethereum.CallMsg{From: in.Agent, To: &in.Vault, Data: data}
	gas, err := c.eth.EstimateGas(ctx, msg)
	if err != nil {
		return common.Hash{}, err
	}
	args := map[string]any{"from": in.Agent, "to": in.Vault, "data": hexutil.Bytes(data), "gas": hexutil.Uint64(gas + gas/5)}
	var hash common.Hash
	if err := c.rpc.CallContext(ctx, &hash, "eth_sendTransaction", args); err != nil {
		return common.Hash{}, err
	}
	return hash, nil
}
func (c *LocalClient) Wait(ctx context.Context, in Intent, hash common.Hash) (*Result, error) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		receipt, err := c.eth.TransactionReceipt(ctx, hash)
		if err == nil {
			if receipt.Status != types.ReceiptStatusSuccessful {
				return nil, fmt.Errorf("local payment transaction reverted: %s", hash.Hex())
			}
			for _, log := range receipt.Logs {
				result, matched, err := decodeEvent(in, *log)
				if err != nil {
					return nil, err
				}
				if matched {
					return result, nil
				}
			}
			return nil, fmt.Errorf("successful transaction missing payment event")
		}
		if err != ethereum.NotFound {
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}

func decodeEvent(in Intent, log types.Log) (*Result, bool, error) {
	if log.Address != in.Vault || len(log.Topics) != 4 {
		return nil, false, nil
	}
	r := &Result{DecisionHash: in.DecisionHash, TxHash: log.TxHash}
	var category, payee common.Hash
	var amount *big.Int
	switch log.Topics[0] {
	case contractABI.Events["AgentPaid"].ID:
		if log.Topics[3] != in.DecisionHash {
			return nil, false, nil
		}
		values, err := contractABI.Events["AgentPaid"].Inputs.NonIndexed().Unpack(log.Data)
		if err != nil {
			return nil, false, err
		}
		category, payee = log.Topics[1], log.Topics[2]
		amount = values[0].(*big.Int)
		r.Status = "paid"
	case contractABI.Events["ApprovalRequested"].ID:
		values, err := contractABI.Events["ApprovalRequested"].Inputs.NonIndexed().Unpack(log.Data)
		if err != nil {
			return nil, false, err
		}
		if common.Hash(values[2].([32]byte)) != in.DecisionHash {
			return nil, false, nil
		}
		category, payee = log.Topics[2], log.Topics[3]
		amount = values[0].(*big.Int)
		r.Status = "approval_requested"
		r.RequestID = new(big.Int).SetBytes(log.Topics[1][:]).String()
	default:
		return nil, false, nil
	}
	if category != common.Hash(in.Category) || common.BytesToAddress(payee[12:]) != in.Payee || amount.String() != in.Amount {
		return nil, false, fmt.Errorf("payment_id already used with different terms")
	}
	return r, true, nil
}
