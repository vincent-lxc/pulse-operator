// 本文件编码 CCTP V2 的 burn，并解析 Iris 的消息。
package procurement

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
)

// BurnRequest 是一笔 Arc 到 Base 的 burn。
type BurnRequest struct {
	Amount               *big.Int
	DestinationDomain    uint32
	MintRecipient        common.Address
	BurnToken            common.Address
	DestinationCaller    common.Hash
	MaxFee               *big.Int
	MinFinalityThreshold uint32
	HookData             []byte
}

// Message 是 Iris /v2/messages 里的一条。
type Message struct {
	Message       string
	EventNonce    string
	Attestation   string
	Status        string
	ForwardTxHash string
}

// EncodeDepositForBurnWithHook 编码带转发 hook 的 burn。
func EncodeDepositForBurnWithHook(req BurnRequest) ([]byte, error) {
	return packBurn("depositForBurnWithHook", req, true)
}

// EncodeDepositForBurn 编码不带 hook 的标准 burn。
func EncodeDepositForBurn(req BurnRequest) ([]byte, error) {
	return packBurn("depositForBurn", req, false)
}

// EncodeApprove 编码 USDC.approve(spender, amount)。
func EncodeApprove(spender common.Address, amount *big.Int) ([]byte, error) {
	args := abi.Arguments{{Type: mustABI("address")}, {Type: mustABI("uint256")}}
	packed, err := args.Pack(spender, unitsOf(amount))
	if err != nil {
		return nil, err
	}
	return append(selector("approve(address,uint256)"), packed...), nil
}

// EncodeReceiveMessage 编码 MessageTransmitterV2.receiveMessage。
func EncodeReceiveMessage(message, attestation []byte) ([]byte, error) {
	args := abi.Arguments{{Type: mustABI("bytes")}, {Type: mustABI("bytes")}}
	packed, err := args.Pack(message, attestation)
	if err != nil {
		return nil, err
	}
	return append(selector("receiveMessage(bytes,bytes)"), packed...), nil
}

// ParseIrisMessages 读取转发交易、nonce 和 attestation。
func ParseIrisMessages(raw []byte) (Message, error) {
	var body struct {
		Messages []struct {
			Message       string `json:"message"`
			EventNonce    string `json:"eventNonce"`
			CCTPNonce     string `json:"cctpNonce"`
			Nonce         string `json:"nonce"`
			Attestation   string `json:"attestation"`
			Status        string `json:"status"`
			ForwardTxHash string `json:"forwardTxHash"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return Message{}, fmt.Errorf("iris messages: %w", err)
	}
	if len(body.Messages) == 0 {
		return Message{}, fmt.Errorf("iris has no message yet")
	}
	row := body.Messages[0]
	nonce := row.EventNonce
	if nonce == "" {
		nonce = row.CCTPNonce
	}
	if nonce == "" {
		nonce = row.Nonce
	}
	return Message{
		Message: row.Message, EventNonce: nonce, Attestation: row.Attestation,
		Status: row.Status, ForwardTxHash: row.ForwardTxHash,
	}, nil
}

// FeeURL 是 Iris 的费用查询。
func FeeURL(iris string, source, dest uint32, forward bool) string {
	base := strings.TrimRight(iris, "/")
	q := "false"
	if forward {
		q = "true"
	}
	return fmt.Sprintf("%s/v2/burn/USDC/fees/%d/%d?forward=%s", base, source, dest, q)
}

// MessagesURL 用源域和 burn 交易查询 Iris。
func MessagesURL(iris string, source uint32, txHash string) string {
	return fmt.Sprintf("%s/v2/messages/%d?transactionHash=%s", strings.TrimRight(iris, "/"), source, txHash)
}

// AddressToBytes32 把地址左填充成 bytes32。
func AddressToBytes32(addr common.Address) common.Hash {
	var out common.Hash
	copy(out[12:], addr.Bytes())
	return out
}

// HookBytes 是 Forwarding Service 的 32 字节 hook。
func HookBytes() []byte {
	raw := strings.TrimPrefix(ForwardHook, "0x")
	b, err := hex.DecodeString(raw)
	if err != nil || len(b) != 32 {
		panic("forward hook")
	}
	return b
}

func packBurn(name string, req BurnRequest, hook bool) ([]byte, error) {
	types := []abi.Type{
		mustABI("uint256"), mustABI("uint32"), mustABI("bytes32"), mustABI("address"),
		mustABI("bytes32"), mustABI("uint256"), mustABI("uint32"),
	}
	values := []any{
		unitsOf(req.Amount), req.DestinationDomain, AddressToBytes32(req.MintRecipient), req.BurnToken,
		req.DestinationCaller, unitsOf(req.MaxFee), req.MinFinalityThreshold,
	}
	sig := name + "(uint256,uint32,bytes32,address,bytes32,uint256,uint32"
	if hook {
		types = append(types, mustABI("bytes"))
		hookData := req.HookData
		if len(hookData) == 0 {
			hookData = HookBytes()
		}
		values = append(values, hookData)
		sig += ",bytes"
	}
	sig += ")"
	args := make(abi.Arguments, len(types))
	for i, t := range types {
		args[i].Type = t
	}
	packed, err := args.Pack(values...)
	if err != nil {
		return nil, err
	}
	return append(selector(sig), packed...), nil
}

func selector(sig string) []byte {
	return mustABISelector(sig)
}
