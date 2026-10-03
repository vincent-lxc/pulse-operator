package treasury

import (
	"strings"
	"testing"
)

func TestDecisionHashIsStableAndExcludesOutcome(t *testing.T) {
	c := Canonical{
		V: 1, AgentID: "pulse-operator", ChainID: "5042002",
		Vault:     "0x4face6592ba1adf83e35b01ccd93d8704d647c01",
		PayableID: "cloud-oct", Action: ActionPay, Category: "infra",
		Payee:       "0x1111111111111111111111111111111111111111",
		AmountUnits: "2000000", ReasonCode: ReasonWithinPolicy,
	}
	a, err := DecisionHash(c)
	if err != nil {
		t.Fatal(err)
	}
	b, err := DecisionHash(c)
	if err != nil {
		t.Fatal(err)
	}
	if a != b || !strings.HasPrefix(a, "0x") || len(a) != 66 {
		t.Fatalf("hash %s", a)
	}
	body, err := CanonicalBytes(c)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "tx") || strings.Contains(string(body), "outcome") {
		t.Fatalf("canonical leaked execution fields: %s", body)
	}
}

func TestAnnotateDropsLLMOverride(t *testing.T) {
	d := Decision{Action: ActionDefer, ReasonCode: ReasonCooldown}
	out := Annotate(d, Advice{Action: ActionPay, Note: "looks fine"}, nil)
	if out.Action != ActionDefer || !strings.Contains(out.SoftNote, "llm_ignored") {
		t.Fatalf("%+v", out)
	}
}
