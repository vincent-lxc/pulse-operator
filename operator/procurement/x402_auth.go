// 本文件按 x402 auth-capture EVM 规范计算 PaymentInfo.salt 和 signatureNonce。
package procurement

import (
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

type paymentInfo struct {
	Operator            common.Address
	Payer               common.Address
	Receiver            common.Address
	Token               common.Address
	MaxAmount           *big.Int
	PreApprovalExpiry   int64
	AuthorizationExpiry int64
	RefundExpiry        int64
	MinFeeBps           int64
	MaxFeeBps           int64
	FeeReceiver         common.Address
	Salt                *big.Int
}

func paymentSalt(receiverAuthorizer, policy string, saltNonce common.Hash) (*big.Int, bool, error) {
	recvZero := receiverAuthorizer == "" || common.HexToAddress(receiverAuthorizer) == (common.Address{})
	policyZero := policy == "" || common.HexToAddress(policy) == (common.Address{})
	if recvZero && policyZero {
		return new(big.Int).SetBytes(saltNonce.Bytes()), false, nil
	}
	typehash := crypto.Keccak256Hash([]byte("x402AuthCaptureSaltBinding(address receiverAuthorizer,address policy,uint256 saltNonce)"))
	packed, err := abiEncode(typehash, common.HexToAddress(receiverAuthorizer), common.HexToAddress(policy), new(big.Int).SetBytes(saltNonce.Bytes()))
	if err != nil {
		return nil, false, err
	}
	return new(big.Int).SetBytes(crypto.Keccak256(packed)), true, nil
}

// paymentInfoTypehash 是 v1.0.0 与 v1.1.0 AuthCaptureEscrow.PAYMENT_INFO_TYPEHASH。
// 两边的 getHash 都是 keccak256(abi.encode(chainid, address(this), keccak256(abi.encode(typehash, paymentInfo))))。
// ERC-3009 nonce 用 payer 置零后的 getHash，见 TokenCollector._getHashPayerAgnostic。
const paymentInfoType = "PaymentInfo(address operator,address payer,address receiver,address token,uint120 maxAmount,uint48 preApprovalExpiry,uint48 authorizationExpiry,uint48 refundExpiry,uint16 minFeeBps,uint16 maxFeeBps,address feeReceiver,uint256 salt)"

func signatureNonce(chainID int64, escrow common.Address, info paymentInfo) (common.Hash, error) {
	typehash := crypto.Keccak256Hash([]byte(paymentInfoType))
	packed, err := abiEncode(
		typehash,
		info.Operator,
		info.Payer,
		info.Receiver,
		info.Token,
		info.MaxAmount,
		big.NewInt(info.PreApprovalExpiry),
		big.NewInt(info.AuthorizationExpiry),
		big.NewInt(info.RefundExpiry),
		big.NewInt(info.MinFeeBps),
		big.NewInt(info.MaxFeeBps),
		info.FeeReceiver,
		info.Salt,
	)
	if err != nil {
		return common.Hash{}, err
	}
	payerAgnostic := crypto.Keccak256Hash(packed)
	outer, err := abiEncode(big.NewInt(chainID), escrow, payerAgnostic)
	if err != nil {
		return common.Hash{}, err
	}
	return crypto.Keccak256Hash(outer), nil
}

func abiEncode(values ...any) ([]byte, error) {
	args := make(abi.Arguments, len(values))
	for i, v := range values {
		switch v.(type) {
		case common.Address:
			args[i].Type = mustABI("address")
		case common.Hash:
			args[i].Type = mustABI("bytes32")
		case *big.Int:
			args[i].Type = mustABI("uint256")
		default:
			return nil, fmt.Errorf("abi encode type %T", v)
		}
	}
	return args.Pack(values...)
}

func mustABI(kind string) abi.Type {
	t, err := abi.NewType(kind, "", nil)
	if err != nil {
		panic(err)
	}
	return t
}
