// 本文件把 Vercel AI Gateway 接成金库循环的规划函数。规则模式返回空，循环就按硬规则原样记账。
package business

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/vincent-lxc/pulse-operator/operator/planner"
	"github.com/vincent-lxc/pulse-operator/operator/treasury"
)

func planFunc(cfg treasury.Config) treasury.PlanFunc {
	if cfg.Planner.Driver != "gateway" {
		return nil
	}
	return func(ctx context.Context, req treasury.PlanRequest) (treasury.PlanOutput, error) {
		return decide(ctx, cfg, payableInput(req))
	}
}

func decide(ctx context.Context, cfg treasury.Config, in planner.PublicInput) (treasury.PlanOutput, error) {
	text, hash, err := planner.Prompt(in)
	if err != nil {
		return treasury.PlanOutput{}, err
	}
	key := os.Getenv(cfg.Planner.APIKeyEnv)
	out, err := (planner.Gateway{
		BaseURL: cfg.Planner.BaseURL,
		APIKey:  key,
		Model:   cfg.Planner.Model,
	}).Decide(ctx, text, hash)
	if err != nil {
		return treasury.PlanOutput{ModelID: cfg.Planner.Model, PromptHash: hash, Source: "gateway"}, err
	}
	notes := out.RiskNotes
	if out.Action == treasury.ActionPay {
		deny, note, jerr := (planner.Jev{
			Endpoint: cfg.Planner.JevURL,
			APIKey:   os.Getenv(cfg.Planner.JevKeyEnv),
			Model:    cfg.Planner.JevModel,
		}).Review(ctx, text)
		if jerr != nil {
			notes = joinNote(notes, "jev_transport")
		} else if deny {
			out.Action = treasury.ActionEscalate
			notes = joinNote(notes, note)
		} else if note != "" && note != "jev_skipped" {
			notes = joinNote(notes, note)
		}
	}
	return treasury.PlanOutput{
		Action: out.Action, Rationale: out.Rationale, RiskNotes: notes, Confidence: out.Confidence,
		ModelID: out.ModelID, PromptHash: hash, Raw: out.Raw, LatencyMS: out.LatencyMS, Source: "gateway",
	}, nil
}

func payableInput(req treasury.PlanRequest) planner.PublicInput {
	cats := map[string]any{}
	for name, cat := range req.Snapshot.Categories {
		cats[name] = map[string]any{
			"enabled": cat.Enabled, "remaining": treasury.FormatUSDC(cat.Remaining),
			"per_tx_cap": treasury.FormatUSDC(cat.PerTxCap), "budget": treasury.FormatUSDC(cat.Budget),
		}
	}
	history := make([]map[string]any, 0, len(req.Recent))
	for _, d := range req.Recent {
		if len(history) >= 8 {
			break
		}
		history = append(history, map[string]any{
			"action": d.Action, "category": d.Category, "amount": treasury.FormatUSDC(d.Amount),
			"reason_code": d.ReasonCode, "rationale": d.Rationale,
		})
	}
	return planner.PublicInput{
		Kind: "payable",
		Bill: map[string]any{
			"id": req.Decision.PayableID, "category": req.Decision.Category,
			"payee": req.Decision.Payee, "amount": treasury.FormatUSDC(req.Decision.Amount),
		},
		Vault: map[string]any{
			"balance": treasury.FormatUSDC(req.Snapshot.Balance),
			"paused":  req.Snapshot.Paused, "pending": len(req.Snapshot.Pending),
			"categories": cats,
		},
		History: history, Allowed: treasury.AllowedActions(req.Decision.Action),
		HardAction: req.Decision.Action, HardReason: req.Decision.Reason,
	}
}

func joinNote(cur, add string) string {
	if strings.TrimSpace(add) == "" {
		return cur
	}
	if cur == "" {
		return add
	}
	return cur + "; " + add
}

func rulesOutput(action, reason string) treasury.PlanOutput {
	if strings.TrimSpace(reason) == "" {
		reason = action
	}
	return treasury.PlanOutput{Action: action, Rationale: reason, ModelID: "rules", Source: "rules", Confidence: "1"}
}

func applyPlanner(ctx context.Context, cfg treasury.Config, plan treasury.PlanFunc, d treasury.Decision, in planner.PublicInput) treasury.Decision {
	if plan == nil && cfg.Planner.Driver != "gateway" {
		return treasury.ApplyPlan(d, rulesOutput(d.Action, d.Reason), nil)
	}
	var (
		out treasury.PlanOutput
		err error
	)
	if plan != nil {
		out, err = plan(ctx, treasury.PlanRequest{Decision: d})
	} else {
		out, err = decide(ctx, cfg, in)
	}
	if err != nil && out.PromptHash == "" {
		if _, hash, hashErr := planner.Prompt(in); hashErr == nil {
			out.PromptHash = hash
		}
	}
	return treasury.ApplyPlan(d, out, err)
}

func plannerError(err error) string {
	if err == nil {
		return ""
	}
	return fmt.Sprint(err)
}
