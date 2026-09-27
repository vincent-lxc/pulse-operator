package outcome

import "testing"

func TestClassify(t *testing.T) {
	cases := []struct {
		kind string
		pnl  float64
		want Label
	}{
		{KindPnL, 1.5, LabelPositive},
		{KindPnL, 0.0001, LabelPositive},
		{KindPnL, -0.4, LabelNegative},
		{KindPnL, 0, LabelNeutral},
		{KindGateBlock, 0, LabelNegative},
		{KindGateBlock, 99, LabelNegative}, // PnL ignored when gated
	}
	for _, tc := range cases {
		got, err := Classify(tc.kind, tc.pnl)
		if err != nil {
			t.Fatalf("%s %v: %v", tc.kind, tc.pnl, err)
		}
		if got != tc.want {
			t.Fatalf("%s pnl=%v: got %s want %s", tc.kind, tc.pnl, got, tc.want)
		}
	}
}

func TestClassifyUnknownKind(t *testing.T) {
	if _, err := Classify("surprise", 0); err == nil {
		t.Fatal("expected error")
	}
}

func TestStoreWriteLabels(t *testing.T) {
	s, err := NewStore(t.TempDir() + "/outcomes.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	o := &Outcome{RunID: "r1", Kind: KindPnL, PnL: 2.0, Reason: "sim"}
	if err := s.Write(o); err != nil {
		t.Fatal(err)
	}
	if o.Label != LabelPositive {
		t.Fatalf("label %s", o.Label)
	}
	got, err := s.Lookup("r1")
	if err != nil || got == nil || got.Label != LabelPositive {
		t.Fatalf("lookup %+v %v", got, err)
	}
	if err := s.Write(&Outcome{RunID: "r1", Kind: KindPnL, PnL: 1}); err == nil {
		t.Fatal("duplicate run should fail")
	}
}
