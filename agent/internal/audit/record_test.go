package audit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func sampleRecord() Record {
	return Record{
		RunID:       "run-stable-1",
		AgentID:     "pulse-demo",
		TS:          1_700_000_000,
		FinalAction: "buy",
		ActionCode:  ActionBuy,
		SizeHint:    100,
		SymbolID:    1,
		Steps: []Step{
			{
				Name:       "observe",
				ModelID:    "kline",
				InputsHash: "0xaaa",
				Outputs:    MustRaw(map[string]any{"symbol": "BTC", "change_24h_pct": 2.1}),
			},
			{
				Name:       "risk",
				ModelID:    "hard-risk",
				InputsHash: "0xbbb",
				Outputs:    MustRaw(map[string]any{"verdict": "allow"}),
				Risk:       &RiskIO{Pass: true, Reason: "allowlist+max_pos"},
			},
		},
	}
}

func TestHashStability(t *testing.T) {
	a := sampleRecord()
	b := sampleRecord()
	ha, err := a.HashHex()
	if err != nil {
		t.Fatal(err)
	}
	hb, err := b.HashHex()
	if err != nil {
		t.Fatal(err)
	}
	if ha != hb {
		t.Fatalf("hash not stable:\n%s\n%s", ha, hb)
	}
	if len(ha) != 66 { // 0x + 64 hex
		t.Fatalf("expected 32-byte keccak hex, got %q", ha)
	}

	// Sealing must not change the hash (hash excludes content_hash fields).
	if err := a.Seal(); err != nil {
		t.Fatal(err)
	}
	if a.ContentHash != ha || a.DecisionHash != ha {
		t.Fatalf("seal mismatch content=%s decision=%s want %s", a.ContentHash, a.DecisionHash, ha)
	}
	ha2, err := a.HashHex()
	if err != nil {
		t.Fatal(err)
	}
	if ha2 != ha {
		t.Fatalf("seal mutated hash: %s vs %s", ha2, ha)
	}
}

func TestHashChangesWhenPayloadChanges(t *testing.T) {
	a := sampleRecord()
	b := sampleRecord()
	b.SizeHint = 101
	ha, _ := a.HashHex()
	hb, _ := b.HashHex()
	if ha == hb {
		t.Fatal("expected different hash after size_hint change")
	}
}

func TestCanonicalJSONNoHTMLEscape(t *testing.T) {
	r := sampleRecord()
	r.FinalAction = "buy&hold"
	b, err := r.CanonicalBytes()
	if err != nil {
		t.Fatal(err)
	}
	if !bytesContains(b, []byte(`"final_action":"buy&hold"`)) {
		t.Fatalf("expected raw ampersand in canonical json, got %s", b)
	}
}

func TestStoreAppendOnce(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(filepath.Join(dir, "decisions.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	r := sampleRecord()
	if err := s.Append(&r); err != nil {
		t.Fatal(err)
	}
	dup := sampleRecord()
	if err := s.Append(&dup); err == nil {
		t.Fatal("expected duplicate run_id to fail")
	}
	got, err := s.Lookup("run-stable-1")
	if err != nil || got == nil {
		t.Fatalf("lookup: %v %v", got, err)
	}
	var line Record
	raw, _ := os.ReadFile(s.Path())
	if err := json.Unmarshal(trimLastNL(raw), &line); err != nil {
		t.Fatal(err)
	}
	if line.DecisionHash != r.DecisionHash {
		t.Fatalf("persisted hash %s want %s", line.DecisionHash, r.DecisionHash)
	}
}

func bytesContains(b, sub []byte) bool {
	return len(b) >= len(sub) && (string(b) == string(sub) ||
		len(sub) == 0 ||
		(func() bool {
			for i := 0; i+len(sub) <= len(b); i++ {
				if string(b[i:i+len(sub)]) == string(sub) {
					return true
				}
			}
			return false
		})())
}

func trimLastNL(b []byte) []byte {
	if len(b) > 0 && b[len(b)-1] == '\n' {
		return b[:len(b)-1]
	}
	return b
}
