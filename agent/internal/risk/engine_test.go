package risk

import (
	"testing"
	"time"

	"github.com/vincent-lxc/pulse-on-monad/agent/internal/policy"
)

func TestRejectUnknownSymbol(t *testing.T) {
	e := New(policy.DefaultLimits())
	v := e.Check(Intent{Symbol: "DOGE", Action: "buy", SizeUSD: 10, Rule: "momentum"}, time.Now())
	if v.Allow {
		t.Fatal("DOGE should be rejected")
	}
}

func TestRejectOverMaxPosition(t *testing.T) {
	e := New(policy.DefaultLimits())
	v := e.Check(Intent{Symbol: "BTC", Action: "buy", SizeUSD: 5000, Rule: "momentum"}, time.Now())
	if v.Allow {
		t.Fatal("over-max should reject, not clip")
	}
}

func TestHoldAlwaysAllowed(t *testing.T) {
	e := New(policy.DefaultLimits())
	v := e.Check(Intent{Symbol: "BTC", Action: "hold", SizeUSD: 0, Rule: "conservative"}, time.Now())
	if !v.Allow {
		t.Fatal(v.Reason)
	}
}
