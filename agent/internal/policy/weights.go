package policy

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"

	"github.com/vincent-lxc/pulse-on-monad/agent/internal/outcome"
)

// Default multiplier / clamps for the PRIMARY closed loop.
const (
	Grow   = 1.05
	Shrink = 0.90
	Floor  = 0.01
	Ceil   = 10.0
	Bonus  = 0.01
)

// RiskLimits are hard constraints enforced in code. Feedback may only keep
// them the same or make them stricter — never looser.
type RiskLimits struct {
	MaxPositionUSD  float64  `json:"max_position_usd"`
	MaxDailyLossUSD float64  `json:"max_daily_loss_usd"`
	CooldownSeconds int      `json:"cooldown_seconds"`
	Allowlist       []string `json:"allowlist"`
}

// DefaultLimits are conservative paper-trading bounds.
func DefaultLimits() RiskLimits {
	return RiskLimits{
		MaxPositionUSD:  1000,
		MaxDailyLossUSD: 50,
		CooldownSeconds: 30,
		Allowlist:       []string{"BTC", "ETH"},
	}
}

// ClampProposed never relaxes a limit.
//
//   - max position / daily loss: only a strictly smaller positive value is taken
//   - cooldown: only a larger value is taken
//   - allowlist: intersection only (cannot add symbols via feedback)
func (cur RiskLimits) ClampProposed(proposed RiskLimits) RiskLimits {
	out := cur
	out.Allowlist = append([]string(nil), cur.Allowlist...)
	if proposed.MaxPositionUSD > 0 && proposed.MaxPositionUSD < cur.MaxPositionUSD {
		out.MaxPositionUSD = proposed.MaxPositionUSD
	}
	if proposed.MaxDailyLossUSD > 0 && proposed.MaxDailyLossUSD < cur.MaxDailyLossUSD {
		out.MaxDailyLossUSD = proposed.MaxDailyLossUSD
	}
	if proposed.CooldownSeconds > cur.CooldownSeconds {
		out.CooldownSeconds = proposed.CooldownSeconds
	}
	if proposed.Allowlist != nil {
		out.Allowlist = intersectSorted(cur.Allowlist, proposed.Allowlist)
	}
	return out
}

func intersectSorted(a, b []string) []string {
	set := map[string]struct{}{}
	for _, s := range b {
		set[s] = struct{}{}
	}
	var out []string
	for _, s := range a {
		if _, ok := set[s]; ok {
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

// State is persisted rule weights plus frozen/tightening risk limits.
type State struct {
	Weights map[string]float64 `json:"weights"`
	Risk    RiskLimits         `json:"risk"`
}

func DefaultState() State {
	return State{
		Weights: map[string]float64{
			"momentum":       0.50,
			"mean_reversion": 0.30,
			"conservative":   0.20,
		},
		Risk: DefaultLimits(),
	}
}

// UpdateWeight is the PRIMARY closed loop: outcomes move rule weights.
// Neutral labels are a no-op. Hard risk is not touched here.
func UpdateWeight(w float64, label outcome.Label) float64 {
	switch label {
	case outcome.LabelPositive:
		w = w*Grow + Bonus
	case outcome.LabelNegative:
		w = w * Shrink
	case outcome.LabelNeutral:
		return w
	}
	if w < Floor {
		w = Floor
	}
	if w > Ceil {
		w = Ceil
	}
	return round4(w)
}

func round4(v float64) float64 {
	return math.Round(v*10000) / 10000
}

// ApplyOutcome updates the named rule's weight. Risk limits are unchanged
// unless tighten is non-nil, in which case they are ClampProposed.
func (s *State) ApplyOutcome(rule string, label outcome.Label, tighten *RiskLimits) {
	if s.Weights == nil {
		s.Weights = map[string]float64{}
	}
	cur := s.Weights[rule]
	if cur == 0 {
		cur = 0.20
	}
	s.Weights[rule] = UpdateWeight(cur, label)
	if tighten != nil {
		s.Risk = s.Risk.ClampProposed(*tighten)
	}
}

// Store persists State as a single JSON file.
type Store struct {
	path string
}

func NewStore(path string) *Store { return &Store{path: path} }

func (s *Store) Load() (State, error) {
	b, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return DefaultState(), nil
		}
		return State{}, err
	}
	var st State
	if err := json.Unmarshal(b, &st); err != nil {
		return State{}, err
	}
	if st.Weights == nil {
		st.Weights = DefaultState().Weights
	}
	if st.Risk.MaxPositionUSD == 0 {
		st.Risk = DefaultLimits()
	}
	return st, nil
}

func (s *Store) Save(st State) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, b, 0o644)
}

func (s State) Pick(candidates []string) (string, error) {
	if len(candidates) == 0 {
		return "", fmt.Errorf("policy: no candidates")
	}
	best := candidates[0]
	bestW := s.Weights[best]
	for _, c := range candidates[1:] {
		if s.Weights[c] > bestW {
			best = c
			bestW = s.Weights[c]
		}
	}
	return best, nil
}
