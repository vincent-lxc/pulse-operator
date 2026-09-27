package stamp

import (
	"encoding/hex"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/crypto"
)

func TestSelectorMatchesKeccakOfSignature(t *testing.T) {
	want := crypto.Keccak256([]byte(StampSignature))[:4]
	got := Selector()
	if hex.EncodeToString(got) != hex.EncodeToString(want) {
		t.Fatalf("selector %x want %x", got, want)
	}
	// Sanity: 4-byte prefix of a known keccak (recomputed, not a magic).
	if len(got) != 4 {
		t.Fatal("selector must be 4 bytes")
	}
}

func TestEncodeCalldataLayout(t *testing.T) {
	var hash [32]byte
	for i := range hash {
		hash[i] = 0x11
	}
	data, err := EncodeCalldata(Request{
		DecisionHash: hash,
		SymbolID:     big.NewInt(1),
		SizeHint:     big.NewInt(100),
		Action:       1,
		Note:         "agent=pulse-demo run=run-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(data) < 4+5*32 {
		t.Fatalf("calldata too short: %d", len(data))
	}
	if hex.EncodeToString(data[:4]) != hex.EncodeToString(Selector()) {
		t.Fatalf("bad selector prefix %x", data[:4])
	}
	// word0 = decisionHash
	if hex.EncodeToString(data[4:36]) != hex.EncodeToString(hash[:]) {
		t.Fatalf("hash word %x", data[4:36])
	}
	// word1 = symbolId 1
	if !allZero(data[36:67]) || data[67] != 1 {
		t.Fatalf("symbolId word %x", data[36:68])
	}
	// word2 = sizeHint 100
	if data[99] != 100 {
		t.Fatalf("sizeHint word %x", data[68:100])
	}
	// word3 = action 1 (uint8 ABI-padded)
	if data[131] != 1 {
		t.Fatalf("action word %x", data[100:132])
	}
	// word4 = offset to dynamic string = 5 * 32 = 160 = 0xa0
	if data[163] != 0xa0 {
		t.Fatalf("string offset word %x", data[132:164])
	}
	// dynamic: length then bytes
	note := "agent=pulse-demo run=run-1"
	if int(data[195]) != len(note) {
		t.Fatalf("note length %d want %d (%x)", data[195], len(note), data[164:196])
	}
	gotNote := string(data[196 : 196+len(note)])
	if gotNote != note {
		t.Fatalf("note %q", gotNote)
	}
}

func TestEncodeRejectsZeroHashAndBadAction(t *testing.T) {
	if _, err := EncodeCalldata(Request{Action: 1, Note: "x"}); err == nil {
		t.Fatal("zero hash should fail")
	}
	var h [32]byte
	h[0] = 1
	if _, err := EncodeCalldata(Request{DecisionHash: h, Action: 3}); err == nil {
		t.Fatal("action 3 should fail")
	}
}

func TestNoteIncludesAgentID(t *testing.T) {
	n := NoteForAgent("pulse-demo", "abc")
	if n != "agent=pulse-demo run=abc" {
		t.Fatal(n)
	}
}

func allZero(b []byte) bool {
	for _, x := range b {
		if x != 0 {
			return false
		}
	}
	return true
}
