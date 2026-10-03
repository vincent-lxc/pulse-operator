// 本文件把账本、core 模型和 treasury 循环接在一起。
// 模型负责落库，treasury 负责硬规则和链调用。
package business

import (
	"context"
	"fmt"
	"io"
	"math/big"
	"os"
	"time"

	"github.com/ethereum/go-ethereum/common"

	"github.com/vincent-lxc/pulse-operator/operator/models"
	"github.com/vincent-lxc/pulse-operator/operator/treasury"
)

// RunFile 读取配置并执行一轮循环，把决策打印到 out。
func RunFile(ctx context.Context, path string, out io.Writer) (treasury.Report, error) {
	cfg, err := treasury.LoadConfig(path)
	if err != nil {
		return treasury.Report{}, err
	}
	report, err := Run(ctx, cfg)
	if err != nil {
		return treasury.Report{}, err
	}
	if out != nil {
		if err := WriteReport(out, report); err != nil {
			return report, err
		}
	}
	return report, nil
}

// Run 执行一轮：导入账本、观察、决策、执行、落库。
func Run(ctx context.Context, cfg treasury.Config) (treasury.Report, error) {
	if err := models.EnsureStorage(); err != nil {
		return treasury.Report{}, err
	}
	policy, err := cfg.PolicyFrom(time.Time{})
	if err != nil {
		return treasury.Report{}, err
	}
	imported, err := treasury.LoadLedger(cfg.Ledger)
	if err != nil {
		return treasury.Report{}, err
	}
	for _, p := range imported {
		if err := models.UpsertPayable(p.ID, p.Category, p.Payee, treasury.FormatUSDC(p.Amount), p.Due.Format(time.RFC3339), p.Memo); err != nil {
			return treasury.Report{}, err
		}
	}
	rows, err := models.ListPayables()
	if err != nil {
		return treasury.Report{}, err
	}
	payables := make([]treasury.Payable, 0, len(rows))
	for _, row := range rows {
		amount, err := treasury.ParseUSDCAllowZero(trimUnits(row.AmountUnits))
		if err != nil {
			return treasury.Report{}, fmt.Errorf("payable %s amount: %w", row.Code, err)
		}
		due, err := time.Parse(time.RFC3339, row.DueAt)
		if err != nil {
			return treasury.Report{}, err
		}
		payables = append(payables, treasury.Payable{
			ID:       row.Code,
			Category: row.CategoryCode,
			Payee:    row.Payee,
			Amount:   amount,
			Due:      due,
			Memo:     row.Memo,
			Status:   row.State,
		})
	}
	lastPaid, err := loadLastPaid()
	if err != nil {
		return treasury.Report{}, err
	}
	manual, manualCodes, err := unreconciledManual()
	if err != nil {
		return treasury.Report{}, err
	}
	chain, closeChain, err := openChain(ctx, cfg, payables)
	if err != nil {
		return treasury.Report{}, err
	}
	defer closeChain()
	audit, err := treasury.OpenAudit(cfg.AuditLog)
	if err != nil {
		return treasury.Report{}, err
	}
	report, err := treasury.Run(ctx, treasury.LoopInput{
		Policy:   policy,
		Payables: payables,
		Chain:    chain,
		Advisor:  advisor(cfg),
		Notifier: notifier(cfg),
		Audit:    audit,
		LastPaid: lastPaid,
		Manual:   manual,
	})
	if err != nil {
		return treasury.Report{}, err
	}
	if err := persist(report, manualCodes); err != nil {
		return report, err
	}
	return report, nil
}

func openChain(ctx context.Context, cfg treasury.Config, payables []treasury.Payable) (treasury.Chain, func(), error) {
	if cfg.ChainDriver == "mock" {
		snap, err := treasury.LoadFixture(cfg.VaultFixture)
		if err != nil {
			return nil, nil, err
		}
		return treasury.NewMockChain(snap), func() {}, nil
	}
	rpc := os.Getenv(cfg.Secrets.RPCEnv)
	if rpc == "" {
		return nil, nil, fmt.Errorf("rpc url missing: set %s", cfg.Secrets.RPCEnv)
	}
	chainID, ok := new(big.Int).SetString(cfg.ChainID, 10)
	if !ok {
		return nil, nil, fmt.Errorf("invalid chain id")
	}
	categories := map[string]struct{}{}
	payees := map[string][]string{}
	for _, p := range payables {
		categories[p.Category] = struct{}{}
		payees[p.Category] = append(payees[p.Category], p.Payee)
	}
	names := make([]string, 0, len(categories))
	for name := range categories {
		names = append(names, name)
	}
	ak, err := treasury.LoadPrivateKey(cfg.Secrets.AgentKeyEnv, cfg.Secrets.AgentKeyFile)
	if err != nil {
		return nil, nil, err
	}
	okey, err := treasury.LoadPrivateKey(cfg.Secrets.OwnerKeyEnv, cfg.Secrets.OwnerKeyFile)
	if err != nil {
		return nil, nil, err
	}
	client, err := treasury.DialLive(ctx, treasury.LiveOptions{
		RPC:        rpc,
		Mode:       cfg.Mode,
		ChainID:    chainID,
		Vault:      common.HexToAddress(cfg.Vault),
		Agent:      common.HexToAddress(cfg.Agent),
		USDC:       common.HexToAddress(cfg.USDC),
		Categories: names,
		Payees:     payees,
		FromBlock:  cfg.FromBlock,
		AgentKey:   ak,
		OwnerKey:   okey,
		GasGwei:    cfg.Gas.MaxFeePerGasGwei,
	})
	if err != nil {
		return nil, nil, err
	}
	return client, client.Close, nil
}

func advisor(cfg treasury.Config) treasury.Advisor {
	if !cfg.LLM.Enabled {
		return treasury.NopAdvisor{}
	}
	url := ""
	if cfg.LLM.URLEnv != "" {
		url = os.Getenv(cfg.LLM.URLEnv)
	}
	return stubAdvisor{url: url}
}

type stubAdvisor struct{ url string }

func (s stubAdvisor) Advise(context.Context, treasury.Decision) (treasury.Advice, error) {
	if s.url == "" {
		return treasury.Advice{}, fmt.Errorf("llm url is empty")
	}
	return treasury.Advice{Note: "llm configured; stub does not override hard limits"}, nil
}

func notifier(cfg treasury.Config) treasury.Notifier {
	if cfg.Notify.Driver == "telegram" {
		return treasury.TelegramNotifier{}
	}
	return &treasury.LogNotifier{}
}

func loadLastPaid() (map[string]time.Time, error) {
	rows, err := models.ListPayees()
	if err != nil {
		return nil, err
	}
	out := map[string]time.Time{}
	for _, row := range rows {
		if row.LastPaidAt == "" {
			continue
		}
		ts, err := time.Parse(time.RFC3339, row.LastPaidAt)
		if err != nil {
			continue
		}
		out[row.Address] = ts
	}
	return out, nil
}

func unreconciledManual() ([]treasury.Inflow, []string, error) {
	rows, err := models.ListRevenues()
	if err != nil {
		return nil, nil, err
	}
	var inflows []treasury.Inflow
	var codes []string
	for _, row := range rows {
		if row.Source != "http" || row.Reconciled {
			continue
		}
		amount, err := treasury.ParseUSDC(trimUnits(row.AmountUnits))
		if err != nil {
			return nil, nil, err
		}
		inflows = append(inflows, treasury.Inflow{
			TxHash: row.Ref,
			From:   row.FromAddress,
			Amount: amount,
			Source: "http",
		})
		codes = append(codes, row.Code)
	}
	return inflows, codes, nil
}

func persist(report treasury.Report, manualCodes []string) error {
	for name, cat := range report.Categories {
		if err := models.SaveSpendCategory(name, name, treasury.FormatUSDC(cat.Budget), treasury.FormatUSDC(cat.PerTxCap), treasury.FormatUSDC(cat.Spent), treasury.FormatUSDC(cat.Remaining), int64(cat.PeriodSeconds), cat.Enabled); err != nil {
			return err
		}
		for addr, allowed := range cat.Payees {
			last := ""
			if ts, ok := report.PayeePaidAt[addr]; ok && !ts.IsZero() {
				last = ts.UTC().Format(time.RFC3339)
			}
			if err := models.SavePayee(name, addr, allowed, last); err != nil {
				return err
			}
		}
	}
	for _, in := range report.Inflows {
		source := in.Source
		if source == "" {
			source = "chain"
		}
		if _, err := models.InsertRevenue(source, in.TxHash, in.From, treasury.FormatUSDC(in.Amount), report.ObservedAt.Format(time.RFC3339), ""); err != nil {
			return err
		}
	}
	for _, code := range manualCodes {
		if err := models.MarkRevenueReconciled(code); err != nil {
			return err
		}
	}
	for _, d := range report.Decisions {
		amount := treasury.FormatUSDC(d.Amount)
		row := models.NewDecisionRecord()
		row.Code = d.DecisionHash
		row.RunID = report.RunID
		row.PayableCode = d.PayableID
		row.Action = d.Action
		row.ReasonCode = d.ReasonCode
		row.Reason = d.Reason
		row.CategoryCode = d.Category
		row.Payee = d.Payee
		row.AmountUnits = amount
		row.TxHash = d.TxHash
		row.Outcome = d.Outcome
		row.RequestID = d.RequestID
		row.SoftNote = d.SoftNote
		if err := models.InsertDecision(row); err != nil {
			return err
		}
		if d.PayableID == "" || d.PayableID == "cycle" {
			continue
		}
		state := payableState(d)
		if state == "" {
			continue
		}
		if err := models.UpdatePayableState(d.PayableID, state); err != nil {
			return err
		}
		if d.Action == treasury.ActionEscalate {
			code := d.DecisionHash
			req := d.RequestID
			if req == "" {
				req = "local"
			}
			if err := models.SaveApproval(code, req, d.Category, d.Payee, amount, d.DecisionHash, "pending", d.ReasonCode); err != nil {
				return err
			}
		}
	}
	cycle := models.NewCycleSnapshot()
	cycle.Code = report.RunID
	cycle.BalanceUnits = treasury.FormatUSDC(report.Balance)
	cycle.ObligationUnits = treasury.FormatUSDC(report.Liquidity.Obligations)
	cycle.ReserveFloorUnits = treasury.FormatUSDC(report.Liquidity.ReserveFloor)
	cycle.ReserveTargetUnits = treasury.FormatUSDC(report.Liquidity.ReserveTarget)
	cycle.SurplusUnits = treasury.FormatUSDC(report.Liquidity.Surplus)
	cycle.ObservedAt = report.ObservedAt.Format(time.RFC3339)
	return models.InsertCycle(cycle)
}

func payableState(d treasury.Decision) string {
	switch d.Action {
	case treasury.ActionPay:
		if d.Outcome == "simulated_paid" || d.Outcome == "paid" || d.Outcome == "dry_run_paid" {
			return "paid"
		}
	case treasury.ActionDefer:
		return "deferred"
	case treasury.ActionEscalate:
		if d.Outcome == "error" || d.Outcome == "agent_key_required" {
			return ""
		}
		return "escalated"
	}
	return ""
}

func trimUnits(s string) string {
	if s == "" {
		return "0"
	}
	return s
}
