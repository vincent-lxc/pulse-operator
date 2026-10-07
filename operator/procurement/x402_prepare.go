// 本文件把 x402 条款收成 EIP-712 JSON，供本地私钥或 Circle typed-data 签名。
package procurement

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/math"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/signer/core/apitypes"
)

// PrepareTypedData 返回 Circle `data` 字段要的 JSON，以及放进 PAYMENT-SIGNATURE 的授权字段。
func PrepareTypedData(payer common.Address, item Accept, now time.Time) (string, map[string]any, map[string]any, error) {
	name, version, err := tokenDomain(item)
	if err != nil {
		return "", nil, nil, err
	}
	chainID, err := chainFromNetwork(item.Network)
	if err != nil {
		return "", nil, nil, err
	}
	var (
		primary string
		auth    map[string]any
		extra   map[string]any
		asset   = item.Asset
	)
	switch strings.ToLower(item.Scheme) {
	case "exact":
		primary = "TransferWithAuthorization"
		nonce := crypto.Keccak256Hash([]byte(fmt.Sprintf("pulse-exact-%s-%d", item.Amount, now.UnixNano())))
		auth = map[string]any{
			"from": payer.Hex(), "to": common.HexToAddress(item.PayTo).Hex(), "value": item.Amount,
			"validAfter": "0", "validBefore": fmt.Sprintf("%d", now.Unix()+item.MaxTimeoutSeconds), "nonce": nonce.Hex(),
		}
	case "auth-capture":
		primary = "ReceiveWithAuthorization"
		auth, extra, err = authCaptureParts(payer, item, now)
		if err != nil {
			return "", nil, nil, err
		}
	default:
		return "", nil, nil, fmt.Errorf("scheme %s", item.Scheme)
	}
	td := apitypes.TypedData{
		Types: apitypes.Types{
			"EIP712Domain": {
				{Name: "name", Type: "string"},
				{Name: "version", Type: "string"},
				{Name: "chainId", Type: "uint256"},
				{Name: "verifyingContract", Type: "address"},
			},
			primary: {
				{Name: "from", Type: "address"},
				{Name: "to", Type: "address"},
				{Name: "value", Type: "uint256"},
				{Name: "validAfter", Type: "uint256"},
				{Name: "validBefore", Type: "uint256"},
				{Name: "nonce", Type: "bytes32"},
			},
		},
		PrimaryType: primary,
		Domain: apitypes.TypedDataDomain{
			Name: name, Version: version, ChainId: math.NewHexOrDecimal256(chainID),
			VerifyingContract: common.HexToAddress(asset).Hex(),
		},
		Message: auth,
	}
	raw, err := json.Marshal(td)
	if err != nil {
		return "", nil, nil, err
	}
	return string(raw), auth, extra, nil
}

func authCaptureParts(payer common.Address, item Accept, now time.Time) (map[string]any, map[string]any, error) {
	method := strings.ToLower(extraString(item, "assetTransferMethod"))
	if method == "" {
		method = "eip3009"
	}
	if method != "eip3009" {
		return nil, nil, fmt.Errorf("auth-capture assetTransferMethod %s is not implemented", method)
	}
	escrow := extraString(item, "authCaptureEscrow")
	if escrow == "" {
		escrow = AuthCaptureEscrowV11
	}
	if !sameAddr(escrow, AuthCaptureEscrowV11) {
		return nil, nil, fmt.Errorf("auth-capture escrow %s is not the v1.1 deployment", escrow)
	}
	operator := extraString(item, "captureAuthorizer")
	if !common.IsHexAddress(operator) {
		return nil, nil, fmt.Errorf("auth-capture captureAuthorizer is missing")
	}
	chainID, err := chainFromNetwork(item.Network)
	if err != nil {
		return nil, nil, err
	}
	saltNonce := crypto.Keccak256Hash([]byte(fmt.Sprintf("pulse-auth-%s-%d", payer.Hex(), now.UnixNano())))
	salt, bound, err := paymentSalt(extraString(item, "receiverAuthorizer"), extraString(item, "policy"), saltNonce)
	if err != nil {
		return nil, nil, err
	}
	pre := now.Unix() + item.MaxTimeoutSeconds
	info := paymentInfo{
		Operator: common.HexToAddress(operator), Receiver: common.HexToAddress(item.PayTo),
		Token: common.HexToAddress(item.Asset), MaxAmount: mustInt(item.Amount),
		PreApprovalExpiry: pre, AuthorizationExpiry: extraInt(item, "captureDeadline", pre),
		RefundExpiry: extraInt(item, "refundDeadline", pre), MinFeeBps: extraInt(item, "minFeeBps", 0),
		MaxFeeBps: extraInt(item, "maxFeeBps", 0), FeeReceiver: common.HexToAddress(extraString(item, "feeRecipient")),
		Salt: salt,
	}
	if info.AuthorizationExpiry < info.PreApprovalExpiry || info.RefundExpiry < info.AuthorizationExpiry {
		return nil, nil, fmt.Errorf("auth-capture expiry order is invalid")
	}
	nonce, err := signatureNonce(chainID, common.HexToAddress(escrow), info)
	if err != nil {
		return nil, nil, err
	}
	auth := map[string]any{
		"from": payer.Hex(), "to": common.HexToAddress(EIP3009CollectorV11).Hex(), "value": item.Amount,
		"validAfter": "0", "validBefore": fmt.Sprintf("%d", pre), "nonce": nonce.Hex(),
	}
	extra := map[string]any{"salt": common.BytesToHash(salt.Bytes()).Hex()}
	if bound {
		extra["saltNonce"] = saltNonce.Hex()
	}
	return auth, extra, nil
}

// FinishPayment 用外部签名拼出 PAYMENT-SIGNATURE。恢复出的地址必须等于付款人。
func FinishPayment(item Accept, payer common.Address, auth map[string]any, sigHex string, extra map[string]any) (Payment, error) {
	if err := VerifyTypedSignature(item, payer, auth, sigHex); err != nil {
		return Payment{}, err
	}
	return packPayment(item, payer, map[string]any{
		"signature": sigHex, "authorization": auth,
	}, extra)
}
