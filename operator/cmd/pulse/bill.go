package main

import (
	"context"
	"flag"
	"fmt"
	"strings"

	"github.com/vincent-lxc/pulse-operator/operator/business"
	"github.com/vincent-lxc/pulse-operator/operator/models"
	"github.com/vincent-lxc/pulse-operator/operator/treasury"
	"github.com/zeromicro/go-zero/core/logx"
)

func runBill(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: pulse bill add|run|list|export|approve|reopen|close|return-float")
	}
	logx.Disable()
	switch args[0] {
	case "add":
		return billAdd(args[1:])
	case "run":
		return billRun(args[1:])
	case "list":
		return billList()
	case "export":
		return billExport(args[1:])
	case "approve":
		return billApprove(args[1:])
	case "reopen":
		return billReopen(args[1:])
	case "close":
		return billClose(args[1:])
	case "return-float":
		return billReturn(args[1:])
	default:
		return fmt.Errorf("unknown bill command %s", args[0])
	}
}

func billAdd(args []string) error {
	vendor := "porkbun"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		vendor = args[0]
		args = args[1:]
	}
	fs := flag.NewFlagSet("bill add", flag.ContinueOnError)
	domain := fs.String("domain", "", "domain name")
	kind := fs.String("kind", "domain_register", "domain_register or domain_renew")
	years := fs.Int("years", 1, "must be 1; Porkbun charges the minimum term")
	if err := fs.Parse(args); err != nil {
		return err
	}
	row, err := business.AddBill(vendor, *domain, *kind, *years)
	if err != nil {
		return err
	}
	fmt.Printf("bill %s domain=%s kind=%s quote_cents=%d state=%s\n", row.Code, row.Domain, row.Kind, row.QuoteCents, row.State)
	return nil
}

func billRun(args []string) error {
	fs := flag.NewFlagSet("bill run", flag.ContinueOnError)
	configPath := fs.String("config", "config/dry-run.yaml", "operator config")
	id := fs.String("id", "", "bill id or domain")
	yes := fs.Bool("yes", false, "confirm this one bill")
	auto := fs.Bool("auto", false, "confirm every bill inside the spend caps")
	real := fs.Bool("i-understand-real-money", false, "required for mainnet")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := treasury.LoadConfig(*configPath)
	if err != nil {
		return err
	}
	var ids []string
	if strings.TrimSpace(*id) != "" {
		ids = []string{*id}
	}
	rows, err := business.RunBills(context.Background(), cfg, ids, business.BillFlags{
		UnderstandRealMoney: *real, Yes: *yes, Auto: *auto,
	})
	if err != nil {
		return err
	}
	for _, row := range rows {
		fmt.Printf("bill %s state=%s action=%s hash=%s rationale=%s model=%s vault=%s order=%s\n",
			row.Code, row.State, row.Action, row.DecisionHash, oneLine(row.Rationale), row.ModelID, row.VaultTx, row.PorkbunOrderID)
	}
	return nil
}

func billApprove(args []string) error {
	fs := flag.NewFlagSet("bill approve", flag.ContinueOnError)
	configPath := fs.String("config", "config/dry-run.yaml", "operator config")
	id := fs.String("id", "", "bill id")
	yes := fs.Bool("yes", false, "confirm this one bill")
	real := fs.Bool("i-understand-real-money", false, "required for mainnet")
	override := fs.Bool("override-cap", false, "pay this bill above max_bill or max_spend_per_run")
	why := fs.String("override-reason", "", "required with --override-cap; stored in the audit and the decision rationale")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*id) == "" {
		return fmt.Errorf("bill approve requires --id")
	}
	cfg, err := treasury.LoadConfig(*configPath)
	if err != nil {
		return err
	}
	row, err := business.ApproveBill(context.Background(), cfg, *id, business.BillFlags{
		UnderstandRealMoney: *real, Yes: *yes, OverrideCap: *override, OverrideReason: *why,
	})
	if row != nil {
		fmt.Printf("bill %s state=%s action=%s reason=%s hash=%s planner=%s vault=%s\n",
			row.Code, row.State, row.Action, row.ReasonCode, row.DecisionHash, row.Planner, row.VaultTx)
	}
	return err
}

func billReopen(args []string) error {
	return billPark(args, "bill reopen", business.ReopenBill)
}

func billClose(args []string) error {
	return billPark(args, "bill close", business.CloseBill)
}

func billPark(args []string, name string, fn func(string, string) (*models.Bill, error)) error {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	configPath := fs.String("config", "config/dry-run.yaml", "operator config")
	id := fs.String("id", "", "bill id")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*id) == "" {
		return fmt.Errorf("%s requires --id", name)
	}
	cfg, err := treasury.LoadConfig(*configPath)
	if err != nil {
		return err
	}
	file := cfg.BillsFile
	if file == "" {
		file = business.DefaultBillsFile
	}
	row, err := fn(*id, file)
	if err != nil {
		return err
	}
	fmt.Printf("bill %s state=%s\n", row.Code, row.State)
	return nil
}

func billList() error {
	if err := business.LoadDefaultBills(); err != nil {
		return err
	}
	rows, err := models.ListBills()
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		fmt.Println("no bills")
		return nil
	}
	for _, row := range rows {
		fmt.Printf("bill %s domain=%s state=%s quote_cents=%d hash=%s model=%s rationale=%s\n",
			row.Code, row.Domain, row.State, row.QuoteCents, row.DecisionHash, row.ModelID, oneLine(row.Rationale))
	}
	return nil
}

func billExport(args []string) error {
	fs := flag.NewFlagSet("bill export", flag.ContinueOnError)
	id := fs.String("id", "", "bill id")
	if err := fs.Parse(args); err != nil {
		return err
	}
	raw, text, err := business.ExportBill(*id)
	if err != nil {
		return err
	}
	fmt.Println(text)
	fmt.Println(raw)
	return nil
}

func billReturn(args []string) error {
	fs := flag.NewFlagSet("bill return-float", flag.ContinueOnError)
	configPath := fs.String("config", "config/dry-run.yaml", "operator config")
	amount := fs.String("amount", "", "USDC to return to the vault")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := treasury.LoadConfig(*configPath)
	if err != nil {
		return err
	}
	if cfg.Mode != "dry-run" && cfg.Mode != "" {
		return fmt.Errorf("return-float prints the transfer and does not send it; move the USDC with an explicit wallet transfer after you have read the plan")
	}
	plan, err := business.ReturnFloatPlan(cfg, *amount)
	if err != nil {
		return err
	}
	fmt.Println(plan)
	return nil
}

func oneLine(s string) string {
	if len(s) > 80 {
		return s[:80]
	}
	return s
}
