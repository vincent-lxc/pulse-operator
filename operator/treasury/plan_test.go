package treasury

import (
	"errors"
	"strings"
	"testing"
)

func TestApplyPlanCannotRaiseADeferToPay(t *testing.T) {
	hard := Decision{Action: ActionDefer, Submit: false, Reason: "cooldown", ReasonCode: ReasonCooldown}
	out := ApplyPlan(hard, PlanOutput{Action: ActionPay, Rationale: "looks fine", Source: "gateway", ModelID: "openai/gpt-5.4-nano"}, nil)
	if out.Action != ActionDefer || out.Submit || !out.Disagree || !strings.Contains(out.SoftNote, "llm_disagreed:pay") {
		t.Fatalf("%+v", out)
	}
}

func TestApplyPlanFailClosedDoesNotSubmitPay(t *testing.T) {
	hard := Decision{Action: ActionPay, Submit: true, Reason: "within", ReasonCode: ReasonWithinPolicy}
	out := ApplyPlan(hard, PlanOutput{}, errors.New("timeout"))
	if out.Action != ActionEscalate || out.Submit || out.ReasonCode != "planner_fail_closed" {
		t.Fatalf("%+v", out)
	}
}

func TestApplyPlanMayOnlyNarrowPay(t *testing.T) {
	hard := Decision{Action: ActionPay, Submit: true, ReasonCode: ReasonWithinPolicy}
	out := ApplyPlan(hard, PlanOutput{Action: ActionDefer, Rationale: "wait for the renewal window", Source: "gateway", ModelID: "m"}, nil)
	if out.Action != ActionDefer || out.Submit || !out.Disagree {
		t.Fatalf("%+v", out)
	}
}

func TestRationaleChangesDecisionHash(t *testing.T) {
	base := Canonical{V: 2, AgentID: "pulse-operator", ChainID: "5042", Vault: "0x1111111111111111111111111111111111111111", PayableID: "b1", Action: ActionPay, Category: "domains", Payee: "0x2222222222222222222222222222222222222222", AmountUnits: "1", ReasonCode: ReasonWithinPolicy, Planner: "gateway", ModelID: "openai/gpt-5.4-nano", PlannerAction: ActionPay, Rationale: "price is inside the cap", Confidence: "0.8"}
	a, err := DecisionHash(base)
	if err != nil {
		t.Fatal(err)
	}
	base.Rationale = "different reason"
	b, err := DecisionHash(base)
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatal("rationale must be inside the hash")
	}
	body, err := CanonicalBytes(base)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "different reason") || strings.Contains(string(body), "outcome") {
		t.Fatalf("body %s", body)
	}
}
