package stamp

import (
	"context"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

func TestWantLiveFromEnv(t *testing.T) {
	t.Setenv("PULSE_STAMP_LIVE", "")
	t.Setenv("PULSE_LIVE_STAMP", "")
	if WantLiveFromEnv() {
		t.Fatal("empty env should be dry-run")
	}
	t.Setenv("PULSE_STAMP_LIVE", "1")
	if !WantLiveFromEnv() {
		t.Fatal("PULSE_STAMP_LIVE=1")
	}
	t.Setenv("PULSE_STAMP_LIVE", "")
	t.Setenv("PULSE_LIVE_STAMP", "true")
	if !WantLiveFromEnv() {
		t.Fatal("legacy PULSE_LIVE_STAMP")
	}
}

func TestExplorerTxURL(t *testing.T) {
	u := ExplorerTxURL("0xabc")
	if u != "https://testnet.monadvision.com/tx/0xabc" {
		t.Fatal(u)
	}
	if ExplorerTxURL("") != "" {
		t.Fatal("empty")
	}
	if ExplorerTxURL("dead") != "https://testnet.monadvision.com/tx/0xdead" {
		t.Fatal(ExplorerTxURL("dead"))
	}
}

func TestParseStampedID(t *testing.T) {
	id := big.NewInt(42)
	lg := &types.Log{
		Topics: []common.Hash{
			StampedTopic0(),
			common.BigToHash(id),
			crypto.Keccak256Hash([]byte("hash")),
			common.BigToHash(big.NewInt(1)),
		},
	}
	got, err := ParseStampedID([]*types.Log{lg})
	if err != nil {
		t.Fatal(err)
	}
	if got.Cmp(id) != 0 {
		t.Fatalf("id %s", got)
	}
	if _, err := ParseStampedID(nil); err == nil {
		t.Fatal("expected missing event")
	}
}

func TestEncodeGetReceipt(t *testing.T) {
	data, err := EncodeGetReceipt(big.NewInt(7))
	if err != nil {
		t.Fatal(err)
	}
	sel := crypto.Keccak256([]byte("getReceipt(uint256)"))[:4]
	if common.Bytes2Hex(data[:4]) != common.Bytes2Hex(sel) {
		t.Fatalf("selector %x want %x", data[:4], sel)
	}
	if data[len(data)-1] != 7 {
		t.Fatalf("id word %x", data[4:])
	}
}

func TestLiveStampRequiresKey(t *testing.T) {
	var h [32]byte
	h[0] = 1
	cfg := Config{RPC: DefaultRPC, ChainID: DefaultChainID, Contract: common.HexToAddress(DefaultContract), DryRun: false}
	_, err := cfg.Stamp(context.Background(), Request{DecisionHash: h, Action: 1, Note: "agent=test"})
	if err == nil || !contains(err.Error(), "PRIVATE_KEY") {
		t.Fatalf("expected PRIVATE_KEY error, got %v", err)
	}
}

func TestStampedTopic0Stable(t *testing.T) {
	a := StampedTopic0()
	b := crypto.Keccak256Hash([]byte(StampedEvent))
	if a != b {
		t.Fatal(a.Hex())
	}
}

func TestUnpackReceiptRealisticBlob(t *testing.T) {
	var hash [32]byte
	for i := range hash {
		hash[i] = byte(i + 1)
	}
	stamper := common.HexToAddress("0x2e24006D3B0ad37687D71185efaFf165087aC776")
	want := OnchainReceipt{
		DecisionHash: hash,
		SymbolID:     big.NewInt(1),
		SizeHint:     big.NewInt(100),
		Action:       1,
		Note:         "agent=pulse-demo run=run-1",
		StampedAt:    1_700_000_000,
		Stamper:      stamper,
	}
	// eth_call encoding: dynamic tuple is offset(0x20) + body.
	blob := encodeGetReceiptReturn(want)

	got, err := unpackReceipt(blob)
	if err != nil {
		t.Fatal(err)
	}
	if got.DecisionHash != want.DecisionHash {
		t.Fatalf("hash %x", got.DecisionHash)
	}
	if got.SymbolID.Cmp(want.SymbolID) != 0 || got.SizeHint.Cmp(want.SizeHint) != 0 {
		t.Fatalf("ids %v %v", got.SymbolID, got.SizeHint)
	}
	if got.Action != 1 || got.Note != want.Note || got.StampedAt != want.StampedAt {
		t.Fatalf("fields %+v", got)
	}
	if got.Stamper != stamper {
		t.Fatalf("stamper %s", got.Stamper.Hex())
	}

	// Body-only (no offset) must also decode — defensive for clients that strip it.
	body := encodeReceiptTuple(want)
	got2, err := unpackReceipt(body)
	if err != nil {
		t.Fatal(err)
	}
	if got2.Note != want.Note || got2.DecisionHash != want.DecisionHash {
		t.Fatalf("body-only %+v", got2)
	}
}

func TestUnpackReceiptEmptyNote(t *testing.T) {
	var hash [32]byte
	hash[31] = 0xaa
	blob := encodeGetReceiptReturn(OnchainReceipt{
		DecisionHash: hash,
		SymbolID:     big.NewInt(0),
		SizeHint:     big.NewInt(0),
		Action:       0,
		Note:         "",
		StampedAt:    1,
		Stamper:      common.HexToAddress("0x0000000000000000000000000000000000000001"),
	})
	got, err := unpackReceipt(blob)
	if err != nil {
		t.Fatal(err)
	}
	if got.Note != "" || got.Action != 0 {
		t.Fatalf("%+v", got)
	}
}

func TestLegacyUnpackIntoInterfaceDoesNotCrashReadback(t *testing.T) {
	// Reproduce the production panic path, then prove unpackReceipt is safe
	// even if someone still calls UnpackIntoInterface.
	var hash [32]byte
	hash[0] = 0x11
	blob := encodeGetReceiptReturn(OnchainReceipt{
		DecisionHash: hash,
		SymbolID:     big.NewInt(1),
		SizeHint:     big.NewInt(100),
		Action:       1,
		Note:         "agent=pulse-demo run=run-1",
		StampedAt:    42,
		Stamper:      common.HexToAddress("0x2e24006D3B0ad37687D71185efaFf165087aC776"),
	})
	panicked := false
	func() {
		defer func() {
			if recover() != nil {
				panicked = true
			}
		}()
		var tstruct struct {
			DecisionHash [32]byte
			SymbolID     *big.Int
			SizeHint     *big.Int
			Action       uint8
			Note         string
			StampedAt    uint64
			Stamper      common.Address
		}
		_ = contractABI().UnpackIntoInterface(&tstruct, "getReceipt", blob)
	}()
	got, err := unpackReceipt(blob)
	if err != nil {
		t.Fatal(err)
	}
	if got.Note != "agent=pulse-demo run=run-1" {
		t.Fatalf("unpackReceipt failed after legacy panic=%v: %+v", panicked, got)
	}
}

func TestFillReadbackSoftErrorOnPanic(t *testing.T) {
	out := &Result{TxHash: "0x1"}
	fillReadback(context.Background(), nil, common.Address{}, common.Hash{}, [32]byte{}, out)
	if out.ReadbackErr == "" {
		t.Fatal("expected ReadbackErr after readback panic; must not crash")
	}
}

func TestUnpackReceiptNoPanicOnGarbage(t *testing.T) {
	got, err := unpackReceipt([]byte{0x01, 0x02})
	if err == nil || got != nil {
		t.Fatalf("expected error, got %+v %v", got, err)
	}
}

func TestUnpackReceiptRecoversPanic(t *testing.T) {
	// Too short for a full head but long enough to pass the len<32 guard
	// with a huge string offset that can trip the decoder.
	raw := make([]byte, 32*7)
	raw[31] = 0xff // decisionHash last byte
	// note offset word (index 4) points way past the buffer
	raw[4*32+31] = 0xff
	_, err := unpackReceipt(raw)
	if err == nil {
		t.Fatal("expected unpack error, not success")
	}
}

// encodeGetReceiptReturn is the eth_call payload for getReceipt: a dynamic
// tuple is offset(0x20) followed by the Receipt body.
func encodeGetReceiptReturn(r OnchainReceipt) []byte {
	body := encodeReceiptTuple(r)
	out := make([]byte, 32+len(body))
	out[31] = 0x20
	copy(out[32:], body)
	return out
}

// encodeReceiptTuple is a hand-rolled ABI encoding of
// (bytes32,uint256,uint256,uint8,string,uint64,address).
func encodeReceiptTuple(r OnchainReceipt) []byte {
	note := []byte(r.Note)
	head := 7 * 32
	buf := make([]byte, head)
	copy(buf[0:32], r.DecisionHash[:])
	copy(buf[32:64], common.LeftPadBytes(r.SymbolID.Bytes(), 32))
	copy(buf[64:96], common.LeftPadBytes(r.SizeHint.Bytes(), 32))
	buf[127] = r.Action
	copy(buf[128:160], common.LeftPadBytes(big.NewInt(int64(head)).Bytes(), 32)) // offset 0xe0
	copy(buf[160:192], common.LeftPadBytes(big.NewInt(int64(r.StampedAt)).Bytes(), 32))
	copy(buf[192:224], common.LeftPadBytes(r.Stamper.Bytes(), 32))

	tail := common.LeftPadBytes(big.NewInt(int64(len(note))).Bytes(), 32)
	padN := ((len(note) + 31) / 32) * 32
	padded := make([]byte, padN)
	copy(padded, note)
	return append(buf, append(tail, padded...)...)
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 ||
		func() bool {
			for i := 0; i+len(sub) <= len(s); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
			return false
		}())
}
