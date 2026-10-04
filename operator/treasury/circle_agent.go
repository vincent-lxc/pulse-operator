// 本文件通过 Circle Agent Wallet CLI 执行合约写入。
// 命令形如：circle wallet execute "pay(bytes32,address,uint256,bytes32)" ... --contract --address --chain
package treasury

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"strings"

	"github.com/ethereum/go-ethereum/common"
)

// CommandRunner 执行外部命令。测试用替身，live 用 exec。
type CommandRunner func(ctx context.Context, name string, args []string) ([]byte, error)

// ExecCommand 是 live 模式的命令运行器。
func ExecCommand(ctx context.Context, name string, args []string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	return buf.Bytes(), err
}

type agentChain struct {
	observe    Chain
	run        CommandRunner
	cli        string
	vault      string
	blockchain string
	payAddress string
	owner      string
}

// NewAgentChain 用 `circle wallet execute` 发送 pay 和 sweep。
func NewAgentChain(observe Chain, run CommandRunner, cli, vault, blockchain, payAddress, ownerAddress string) Chain {
	if run == nil {
		run = ExecCommand
	}
	if cli == "" {
		cli = "circle"
	}
	return agentChain{observe: observe, run: run, cli: cli, vault: vault, blockchain: blockchain, payAddress: payAddress, owner: ownerAddress}
}

func (c agentChain) Observe(ctx context.Context) (Snapshot, error) { return c.observe.Observe(ctx) }

func (c agentChain) Pay(ctx context.Context, call PayCall) (ExecResult, error) {
	if c.payAddress == "" {
		return ExecResult{Product: ProductAgent}, fmt.Errorf("set CIRCLE_AGENT_ADDRESS to the agent wallet that may call pay")
	}
	params, err := PayArguments(call)
	if err != nil {
		return ExecResult{Product: ProductAgent}, err
	}
	tx, err := c.execute(ctx, c.payAddress, "pay(bytes32,address,uint256,bytes32)", params)
	if err != nil {
		return ExecResult{Product: ProductAgent}, err
	}
	return explain(ctx, c.observe, tx, call.DecisionHash, ProductAgent, false), nil
}

func (c agentChain) Sweep(ctx context.Context, call SweepCall) (ExecResult, error) {
	if c.owner == "" {
		return ExecResult{Product: ProductAgent}, ErrOwnerKeyRequired
	}
	tx, err := c.execute(ctx, c.owner, "sweepToReserve(uint256,bytes32)", SweepArguments(call))
	if err != nil {
		return ExecResult{Product: ProductAgent}, err
	}
	return explain(ctx, c.observe, tx, call.DecisionHash, ProductAgent, true), nil
}

// Approve 用 owner 地址调用 circle wallet execute approve。
func (c agentChain) Approve(ctx context.Context, requestID string) (ExecResult, error) {
	return c.ownerExecute(ctx, requestID, "approve(uint256)", "approved")
}

// Reject 用 owner 地址调用 circle wallet execute reject。
func (c agentChain) Reject(ctx context.Context, requestID string) (ExecResult, error) {
	return c.ownerExecute(ctx, requestID, "reject(uint256)", "rejected")
}

func (c agentChain) ownerExecute(ctx context.Context, requestID, signature, status string) (ExecResult, error) {
	if c.owner == "" {
		return ExecResult{Product: ProductAgent}, ErrOwnerKeyRequired
	}
	if _, err := ParseRequestID(requestID); err != nil {
		return ExecResult{Product: ProductAgent}, err
	}
	tx, err := c.execute(ctx, c.owner, signature, []string{strings.TrimSpace(requestID)})
	if err != nil {
		return ExecResult{Product: ProductAgent}, err
	}
	return ExecResult{Status: status, TxHash: tx, RequestID: strings.TrimSpace(requestID), Product: ProductAgent}, nil
}

func (c agentChain) execute(ctx context.Context, from, signature string, params []string) (string, error) {
	args := []string{"wallet", "execute", signature}
	args = append(args, params...)
	args = append(args, "--contract", c.vault, "--address", from, "--chain", c.blockchain, "--output", "json")
	out, err := c.run(ctx, c.cli, args)
	hash := txHashFromOutput(out)
	if err != nil {
		if hash == "" {
			return "", fmt.Errorf("circle CLI: %w: %s", err, strings.TrimSpace(string(out)))
		}
	}
	if hash == "" {
		return "", fmt.Errorf("circle CLI returned no transaction hash: %s", strings.TrimSpace(string(out)))
	}
	return hash, nil
}

var txHashPattern = regexp.MustCompile(`0x[0-9a-fA-F]{64}`)

func txHashFromOutput(out []byte) string {
	var body struct {
		TxHash          string `json:"txHash"`
		TransactionHash string `json:"transactionHash"`
		Data            struct {
			TxHash          string `json:"txHash"`
			TransactionHash string `json:"transactionHash"`
		} `json:"data"`
	}
	if json.Unmarshal(out, &body) == nil {
		for _, candidate := range []string{body.TxHash, body.TransactionHash, body.Data.TxHash, body.Data.TransactionHash} {
			if txHashPattern.MatchString(candidate) {
				return common.HexToHash(candidate).Hex()
			}
		}
	}
	found := txHashPattern.Find(out)
	if found == nil {
		return ""
	}
	return common.HexToHash(string(found)).Hex()
}
