// 本文件把 LLM 规划收束到硬规则允许的动作里。模型不能放宽上限。
package treasury

import (
	"context"
	"strings"
)

// AllowedActions 返回硬规则已经允许的动作。pay 只在硬规则本身是 pay 时出现。
func AllowedActions(hard string) []string {
	switch hard {
	case ActionPay:
		return []string{ActionPay, ActionDefer, ActionEscalate, ActionReject}
	case ActionDefer, ActionEscalate:
		return []string{ActionDefer, ActionEscalate, ActionReject}
	default:
		return []string{hard}
	}
}

// ApplyPlan 用模型输出调整决策。无效、超时或出错时，原本会付款的单改成不提交的 escalate。
func ApplyPlan(d Decision, out PlanOutput, callErr error) Decision {
	hard := d.Action
	hardSubmit := d.Submit
	d.Planner = out.Source
	d.ModelID = out.ModelID
	d.PromptHash = out.PromptHash
	d.RiskNotes = out.RiskNotes
	d.Confidence = out.Confidence
	d.LatencyMS = out.LatencyMS
	d.PlannerRaw = out.Raw
	if callErr != nil || !knownAction(out.Action) || strings.TrimSpace(out.Rationale) == "" {
		d.Planner = "error"
		if d.ModelID == "" {
			d.ModelID = "planner-error"
		}
		d.PlannerAction = ActionEscalate
		d.Rationale = "planner_error"
		if callErr != nil && d.RiskNotes == "" {
			d.RiskNotes = oneLine(callErr.Error())
		}
		d.Disagree = hard != ActionEscalate
		if hard == ActionPay {
			d.Action = ActionEscalate
			d.Submit = false
			d.ReasonCode = "planner_fail_closed"
			d.Reason = "planner failed closed; payment was not submitted"
		}
		return sealNotes(d)
	}
	d.PlannerAction = out.Action
	d.Rationale = out.Rationale
	if !containsAction(AllowedActions(hard), out.Action) {
		d.Disagree = true
		d.SoftNote = joinNote(d.SoftNote, "llm_disagreed:"+out.Action)
		return sealNotes(d)
	}
	// 规则没拒绝时，模型的 reject 不能把账单关死。改成不提交的 escalate，人可以再看。
	if out.Action == ActionReject && hard != ActionReject {
		d.Disagree = true
		d.Action = ActionEscalate
		d.Submit = false
		d.ReasonCode = "planner_reject"
		d.Reason = "model rejected a bill the rules did not reject"
		d.SoftNote = joinNote(d.SoftNote, "llm_chose:reject")
		return sealNotes(d)
	}
	if out.Action != hard {
		d.Disagree = true
		d.SoftNote = joinNote(d.SoftNote, "llm_chose:"+out.Action)
	}
	d.Action = out.Action
	switch out.Action {
	case ActionPay:
		d.Submit = hardSubmit && hard == ActionPay
	case ActionEscalate:
		d.Submit = hard == ActionEscalate && hardSubmit
	default:
		d.Submit = false
	}
	return sealNotes(d)
}

// sealNotes 在算 decisionHash 之前把分歧写进 RiskNotes。哈希之后不能再追加。
func sealNotes(d Decision) Decision {
	if d.SoftNote != "" {
		d.RiskNotes = joinNote(d.RiskNotes, d.SoftNote)
	}
	if d.Disagree && !strings.Contains(d.RiskNotes, "llm_disagreed") {
		d.RiskNotes = joinNote(d.RiskNotes, "llm_disagreed")
	}
	return d
}

func planDecision(ctx context.Context, plan PlanFunc, d Decision, snap Snapshot, recent []Decision) Decision {
	if plan == nil {
		return ApplyPlan(d, PlanOutput{
			Action: d.Action, Rationale: d.Reason, ModelID: "rules", Source: "rules", Confidence: "1",
		}, nil)
	}
	out, err := plan(ctx, PlanRequest{Decision: d, Snapshot: snap, Recent: append([]Decision(nil), recent...)})
	return ApplyPlan(d, out, err)
}

func knownAction(action string) bool {
	switch action {
	case ActionPay, ActionDefer, ActionEscalate, ActionReject:
		return true
	default:
		return false
	}
}

func containsAction(list []string, action string) bool {
	for _, item := range list {
		if item == action {
			return true
		}
	}
	return false
}

func joinNote(cur, add string) string {
	if cur == "" {
		return add
	}
	return cur + "; " + add
}

func oneLine(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > 240 {
		return s[:240]
	}
	return s
}
