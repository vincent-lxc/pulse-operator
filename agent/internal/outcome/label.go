package outcome

import "fmt"

// Label is the closed-loop class written back onto a run.
type Label string

const (
	LabelPositive Label = "positive"
	LabelNegative Label = "negative"
	LabelNeutral  Label = "neutral"
)

const (
	KindPnL       = "pnl"
	KindGateBlock = "gate_block"
)

// Outcome is keyed by run_id. Kind is "pnl" or "gate_block".
type Outcome struct {
	RunID      string  `json:"run_id"`
	Kind       string  `json:"kind"`
	PnL        float64 `json:"pnl,omitempty"`
	Gate       string  `json:"gate,omitempty"` // "risk" | "jev" | ""
	Reason     string  `json:"reason"`
	Label      Label   `json:"label"`
	RecordedAt int64   `json:"recorded_at"`
}

// Labeling rules (also documented in docs/audit-schema.md):
//
//   - kind=gate_block          → negative  (hard risk or Jev blocked the action)
//   - kind=pnl and pnl > 0     → positive
//   - kind=pnl and pnl < 0     → negative
//   - kind=pnl and pnl == 0    → neutral   (hold / flat; no weight update)
//
// Neutral is reserved for zero-PnL fills and no-ops. A blocked trade is never
// neutral: a gate is a failed attempt and trains the policy away from that rule.
func Classify(kind string, pnl float64) (Label, error) {
	switch kind {
	case KindGateBlock:
		return LabelNegative, nil
	case KindPnL:
		switch {
		case pnl > 0:
			return LabelPositive, nil
		case pnl < 0:
			return LabelNegative, nil
		default:
			return LabelNeutral, nil
		}
	default:
		return "", fmt.Errorf("outcome: unknown kind %q", kind)
	}
}

// MustClassify is Classify that panics on unknown kind (tests / demo only).
func MustClassify(kind string, pnl float64) Label {
	l, err := Classify(kind, pnl)
	if err != nil {
		panic(err)
	}
	return l
}
