package policy

import (
	"testing"

	"github.com/vincent-lxc/pulse-on-monad/agent/internal/outcome"
)

func TestUpdateWeightPositiveNegativeNeutral(t *testing.T) {
	w := 0.50
	pos := UpdateWeight(w, outcome.LabelPositive)
	if pos <= w {
		t.Fatalf("positive should raise weight: %v → %v", w, pos)
	}
	neg := UpdateWeight(w, outcome.LabelNegative)
	if neg >= w {
		t.Fatalf("negative should lower weight: %v → %v", w, neg)
	}
	neu := UpdateWeight(w, outcome.LabelNeutral)
	if neu != w {
		t.Fatalf("neutral must be a no-op: %v → %v", w, neu)
	}
}

func TestUpdateWeightFloor(t *testing.T) {
	w := Floor
	for i := 0; i < 20; i++ {
		w = UpdateWeight(w, outcome.LabelNegative)
	}
	if w < Floor {
		t.Fatalf("weight fell below floor: %v", w)
	}
}

func TestHardRiskNeverLoosened(t *testing.T) {
	cur := DefaultLimits()
	loose := RiskLimits{
		MaxPositionUSD:  1_000_000,
		MaxDailyLossUSD: 9_999,
		CooldownSeconds: 1,
		Allowlist:       []string{"BTC", "ETH", "DOGE", "XYZ"},
	}
	got := cur.ClampProposed(loose)
	if got.MaxPositionUSD != cur.MaxPositionUSD {
		t.Fatalf("max position loosened: %v", got.MaxPositionUSD)
	}
	if got.MaxDailyLossUSD != cur.MaxDailyLossUSD {
		t.Fatalf("daily loss loosened: %v", got.MaxDailyLossUSD)
	}
	if got.CooldownSeconds != cur.CooldownSeconds {
		t.Fatalf("cooldown loosened: %v", got.CooldownSeconds)
	}
	for _, s := range got.Allowlist {
		if s == "DOGE" || s == "XYZ" {
			t.Fatalf("allowlist grew via feedback: %v", got.Allowlist)
		}
	}
}

func TestHardRiskCanTighten(t *testing.T) {
	cur := DefaultLimits()
	tight := RiskLimits{
		MaxPositionUSD:  250,
		MaxDailyLossUSD: 10,
		CooldownSeconds: 120,
		Allowlist:       []string{"BTC"},
	}
	got := cur.ClampProposed(tight)
	if got.MaxPositionUSD != 250 || got.MaxDailyLossUSD != 10 || got.CooldownSeconds != 120 {
		t.Fatalf("expected tighter limits, got %+v", got)
	}
	if len(got.Allowlist) != 1 || got.Allowlist[0] != "BTC" {
		t.Fatalf("allowlist should shrink to BTC, got %v", got.Allowlist)
	}
}

func TestApplyOutcomeDoesNotTouchRiskUnlessAsked(t *testing.T) {
	st := DefaultState()
	beforePos := st.Risk.MaxPositionUSD
	beforeLoss := st.Risk.MaxDailyLossUSD
	beforeCD := st.Risk.CooldownSeconds
	st.ApplyOutcome("momentum", outcome.LabelPositive, nil)
	if st.Risk.MaxPositionUSD != beforePos || st.Risk.MaxDailyLossUSD != beforeLoss || st.Risk.CooldownSeconds != beforeCD {
		t.Fatalf("risk mutated without tighten: %+v", st.Risk)
	}
	if st.Weights["momentum"] <= 0.50 {
		t.Fatalf("momentum should have grown, got %v", st.Weights["momentum"])
	}
	loose := RiskLimits{MaxPositionUSD: 5000, CooldownSeconds: 0}
	st.ApplyOutcome("momentum", outcome.LabelNegative, &loose)
	if st.Risk.MaxPositionUSD != beforePos {
		t.Fatalf("feedback loosened max position to %v", st.Risk.MaxPositionUSD)
	}
}
