package risk

import (
	"fmt"
	"strings"
	"time"

	"github.com/vincent-lxc/pulse-on-monad/agent/internal/policy"
)

// Intent is what the planner wants. The engine never invents a trade.
type Intent struct {
	Symbol   string
	Action   string // buy | sell | hold
	SizeUSD  float64
	Rule     string
	Reason   string
}

// Verdict is fail-closed: reject unless every hard check passes.
type Verdict struct {
	Allow  bool
	Clip   bool
	Reason string
	Size   float64
}

// Engine is code-only hard risk. Feedback must not loosen its limits.
type Engine struct {
	Limits     policy.RiskLimits
	LastFillAt map[string]time.Time
	DailyLoss  float64
}

func New(limits policy.RiskLimits) *Engine {
	return &Engine{
		Limits:     limits,
		LastFillAt: map[string]time.Time{},
	}
}

func (e *Engine) Check(in Intent, now time.Time) Verdict {
	if in.Action == "" || in.Action == "hold" {
		return Verdict{Allow: true, Reason: "hold", Size: 0}
	}
	if !allowlisted(e.Limits.Allowlist, in.Symbol) {
		return Verdict{Allow: false, Reason: "symbol not on allowlist"}
	}
	if e.Limits.MaxDailyLossUSD > 0 && e.DailyLoss >= e.Limits.MaxDailyLossUSD {
		return Verdict{Allow: false, Reason: "daily loss limit"}
	}
	if e.Limits.CooldownSeconds > 0 {
		if last, ok := e.LastFillAt[in.Symbol]; ok {
			if now.Sub(last) < time.Duration(e.Limits.CooldownSeconds)*time.Second {
				return Verdict{Allow: false, Reason: "cooldown"}
			}
		}
	}
	size := in.SizeUSD
	if size <= 0 {
		return Verdict{Allow: false, Reason: "non-positive size"}
	}
	if e.Limits.MaxPositionUSD > 0 && size > e.Limits.MaxPositionUSD {
		// Fail-closed: do not silently invent a smaller ticket.
		return Verdict{Allow: false, Reason: fmt.Sprintf("size %.2f exceeds max_position %.2f", size, e.Limits.MaxPositionUSD)}
	}
	return Verdict{Allow: true, Reason: "allowlist+max_pos", Size: size}
}

func allowlisted(list []string, symbol string) bool {
	want := strings.ToUpper(symbol)
	for _, s := range list {
		if strings.ToUpper(s) == want {
			return true
		}
	}
	return false
}
