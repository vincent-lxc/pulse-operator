package procurement

import (
	"math/big"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

func TestBurnCalldataUsesForwardHook(t *testing.T) {
	recipient := common.HexToAddress("0x2222222222222222222222222222222222222222")
	data, err := EncodeDepositForBurnWithHook(BurnRequest{
		Amount: big.NewInt(8_750_000), DestinationDomain: BaseDomain,
		MintRecipient: recipient, BurnToken: common.HexToAddress(ArcUSDC),
		MaxFee: big.NewInt(54050), MinFinalityThreshold: 1000,
	})
	if err != nil {
		t.Fatal(err)
	}
	want := crypto.Keccak256([]byte("depositForBurnWithHook(uint256,uint32,bytes32,address,bytes32,uint256,uint32,bytes)"))[:4]
	if len(data) < 4 || string(data[:4]) != string(want) {
		t.Fatalf("selector %x", data[:4])
	}
	hook := HookBytes()
	if !strings.Contains(string(data), string(hook)) && !containsBytes(data, hook) {
		t.Fatal("hook data missing")
	}
	plain, err := EncodeDepositForBurn(BurnRequest{
		Amount: big.NewInt(1), DestinationDomain: 6, MintRecipient: recipient,
		BurnToken: common.HexToAddress(ArcUSDC), MaxFee: big.NewInt(1), MinFinalityThreshold: 2000,
	})
	if err != nil {
		t.Fatal(err)
	}
	plainSel := crypto.Keccak256([]byte("depositForBurn(uint256,uint32,bytes32,address,bytes32,uint256,uint32)"))[:4]
	if string(plain[:4]) != string(plainSel) {
		t.Fatalf("plain selector %x", plain[:4])
	}
}

func TestParseIrisForwardMessage(t *testing.T) {
	raw := []byte(`{"messages":[{"message":"0xabc","eventNonce":"7","attestation":"0xatt","status":"complete","forwardTxHash":"0xmint"}]}`)
	msg, err := ParseIrisMessages(raw)
	if err != nil {
		t.Fatal(err)
	}
	if msg.ForwardTxHash != "0xmint" || msg.EventNonce != "7" || msg.Status != "complete" {
		t.Fatalf("%+v", msg)
	}
	if _, err := ParseIrisMessages([]byte(`{"messages":[]}`)); err == nil {
		t.Fatal("empty iris should fail")
	}
}

func TestFeeURL(t *testing.T) {
	got := FeeURL("https://iris-api-sandbox.circle.com", 26, 6, true)
	if got != "https://iris-api-sandbox.circle.com/v2/burn/USDC/fees/26/6?forward=true" {
		t.Fatal(got)
	}
}

func containsBytes(hay, needle []byte) bool {
	if len(needle) == 0 || len(hay) < len(needle) {
		return false
	}
	for i := 0; i+len(needle) <= len(hay); i++ {
		ok := true
		for j := range needle {
			if hay[i+j] != needle[j] {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}
	return false
}
