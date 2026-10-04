// 本文件把账本、core 模型和 treasury 循环接在一起。
// 模型负责落库，treasury 负责硬规则和链调用。
package business

import (
	"context"
	"fmt"
	"io"
	"math/big"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common"

	"github.com/vincent-lxc/pulse-operator/operator/models"
	"github.com/vincent-lxc/pulse-operator/operator/treasury"
)

// cycleMu 保证定时循环、runonce 和 approve 不会同时发交易。
var cycleMu sync.Mutex

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
	if !cycleMu.TryLock() {
		return treasury.Report{}, models.NewBusinessError("operator cycle already running")
	}
	defer cycleMu.Unlock()
	return runLocked(ctx, cfg)
}

func runLocked(ctx context.Context, cfg treasury.Config) (treasury.Report, error) {
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
	confirmed, confirmedCodes, err := confirmCCTP(ctx, cfg)
	if err != nil {
		return treasury.Report{}, err
	}
	manual = append(manual, confirmed...)
	manualCodes = append(manualCodes, confirmedCodes...)
	chain, closeChain, err := openChain(ctx, cfg, payables)
	if err != nil {
		return treasury.Report{}, err
	}
	defer closeChain()
	audit, err := treasury.OpenAudit(cfg.AuditLog)
	if err != nil {
		return treasury.Report{}, err
	}
	note, err := notifier(cfg)
	if err != nil {
		return treasury.Report{}, err
	}
	_, limitNote := treasury.SpendingLimits(cfg.Circle.Blockchain)
	if cfg.Executor != "circle-agent" {
		limitNote = "developer-controlled wallets and raw keys do not use Circle agent spending policies; those policies are mainnet agent wallets only"
	}
	report, err := treasury.Run(ctx, treasury.LoopInput{
		Policy:        policy,
		Payables:      payables,
		Chain:         chain,
		Advisor:       advisor(cfg),
		Notifier:      note,
		Audit:         audit,
		LastPaid:      lastPaid,
		Manual:        manual,
		CircleProduct: cfg.ExecutorProduct(),
		LimitNote:     limitNote,
	})
	if err != nil {
		return treasury.Report{}, err
	}
	if err := persist(cfg, report, manualCodes); err != nil {
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
		snap, err = overlayMockState(mockStatePath(cfg), snap)
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
	fromBlock := cfg.FromBlock
	if cursor, err := treasury.LoadScanCursor(cfg.ScanCursor, cfg.Vault); err != nil {
		return nil, nil, err
	} else if cursor > 0 && cursor+1 > fromBlock {
		fromBlock = cursor + 1
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
		FromBlock:  fromBlock,
		AgentKey:   ak,
		OwnerKey:   okey,
		GasGwei:    cfg.Gas.MaxFeePerGasGwei,
		Lookback:   cfg.LogLookback,
		Chunk:      cfg.LogChunk,
		FullScan:   cfg.FullLogScan,
	})
	if err != nil {
		return nil, nil, err
	}
	return routeExecutor(cfg, client, client.Close)
}

func routeExecutor(cfg treasury.Config, base treasury.Chain, closeFn func()) (treasury.Chain, func(), error) {
	product := cfg.ExecutorProduct()
	if cfg.Mode != "live" || cfg.Executor == "raw-key" || cfg.Executor == "" {
		return treasury.TagChain(base, product), closeFn, nil
	}
	fee := circleFee(cfg)
	switch cfg.Executor {
	case "circle-wallets":
		client, err := walletsClient(cfg)
		if err != nil {
			closeFn()
			return nil, nil, err
		}
		payWallet, err := treasury.LoadSecret(cfg.Circle.WalletIDEnv, cfg.Circle.WalletIDFile)
		if err != nil {
			closeFn()
			return nil, nil, err
		}
		ownerWallet, err := treasury.LoadSecret(cfg.Circle.OwnerWalletIDEnv, cfg.Circle.OwnerWalletIDFile)
		if err != nil {
			closeFn()
			return nil, nil, err
		}
		return treasury.NewWalletsChain(base, client, cfg.Vault, cfg.Circle.Blockchain, payWallet, ownerWallet, fee), closeFn, nil
	case "circle-agent":
		payAddress, err := treasury.LoadSecret(cfg.Circle.AgentAddressEnv, "")
		if err != nil {
			closeFn()
			return nil, nil, err
		}
		ownerAddress, err := treasury.LoadSecret(cfg.Circle.OwnerAddressEnv, "")
		if err != nil {
			closeFn()
			return nil, nil, err
		}
		return treasury.NewAgentChain(base, nil, cfg.Circle.CLI, cfg.Vault, cfg.Circle.Blockchain, payAddress, ownerAddress), closeFn, nil
	default:
		return treasury.TagChain(base, product), closeFn, nil
	}
}

func walletsClient(cfg treasury.Config) (*treasury.WalletsClient, error) {
	apiKey, err := treasury.LoadSecret(cfg.Circle.APIKeyEnv, cfg.Circle.APIKeyFile)
	if err != nil {
		return nil, err
	}
	secretRaw, err := treasury.LoadSecret(cfg.Circle.EntitySecretEnv, cfg.Circle.EntitySecretFile)
	if err != nil {
		return nil, err
	}
	if apiKey == "" || secretRaw == "" {
		return nil, fmt.Errorf("circle wallets live mode needs %s and %s", cfg.Circle.APIKeyEnv, cfg.Circle.EntitySecretEnv)
	}
	secret, err := treasury.ParseEntitySecret(secretRaw)
	if err != nil {
		return nil, err
	}
	return &treasury.WalletsClient{BaseURL: cfg.Circle.APIBase, APIKey: apiKey, EntitySecret: secret}, nil
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

func circleFee(cfg treasury.Config) treasury.CircleFee {
	if cfg.Circle.ExplicitGas {
		return treasury.CircleFee{
			Explicit:    true,
			MaxFee:      strconv.FormatInt(cfg.Gas.MaxFeePerGasGwei, 10),
			PriorityFee: strconv.FormatInt(cfg.Gas.MaxPriorityFeePerGasGwei, 10),
			GasLimit:    strconv.FormatUint(cfg.Gas.GasLimit, 10),
		}
	}
	return treasury.CircleFee{Level: cfg.Circle.FeeLevel}
}

func notifier(cfg treasury.Config) (treasury.Notifier, error) {
	if cfg.Notify.Driver != "telegram" {
		return &treasury.LogNotifier{}, nil
	}
	token, err := treasury.LoadSecret(cfg.Notify.TokenEnv, cfg.Notify.TokenFile)
	if err != nil {
		return nil, err
	}
	chat, err := treasury.LoadSecret(cfg.Notify.ChatEnv, cfg.Notify.ChatFile)
	if err != nil {
		return nil, err
	}
	if token == "" || chat == "" {
		return treasury.NopNotifier{}, nil
	}
	return treasury.TelegramNotifier{Token: token, ChatID: chat}, nil
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
		if row.Reconciled || (row.Source != "http" && row.CircleProduct != treasury.ProductCCTP && row.CircleProduct != treasury.ProductGateway && row.Source != treasury.ProductCCTP && row.Source != treasury.ProductGateway) {
			continue
		}
		amount, err := treasury.ParseUSDC(trimUnits(row.AmountUnits))
		if err != nil {
			return nil, nil, err
		}
		product := row.CircleProduct
		if product == "" && row.Source == "http" {
			product = treasury.ProductLocalHTTP
		}
		inflows = append(inflows, treasury.Inflow{
			TxHash:  row.Ref,
			From:    row.FromAddress,
			Amount:  amount,
			Source:  row.Source,
			Product: product,
		})
		codes = append(codes, row.Code)
	}
	return inflows, codes, nil
}

func persist(cfg treasury.Config, report treasury.Report, manualCodes []string) error {
	for name, cat := range report.Categories {
		if err := models.SaveSpendCategory(name, name, treasury.FormatUSDC(cat.Budget), treasury.FormatUSDC(cat.PerTxCap), treasury.FormatUSDC(cat.Spent), treasury.FormatUSDC(cat.Remaining), int64(cat.PeriodSeconds), cat.Enabled); err != nil {
			return err
		}
		committed := committedPayeeTimes(report)
		for addr, allowed := range cat.Payees {
			last := ""
			if ts, ok := committed[treasury.NormalizeAddress(addr)]; ok && !ts.IsZero() {
				last = ts.UTC().Format(time.RFC3339)
			}
			if err := models.SavePayee(name, addr, allowed, last); err != nil {
				return err
			}
		}
	}
	pending := map[string]bool{}
	for _, code := range manualCodes {
		pending[code] = true
	}
	for _, in := range report.Inflows {
		source := in.Source
		if source == "" {
			source = "chain"
		}
		product := in.Product
		if product == "" {
			product = source
		}
		row, err := models.InsertRevenue(source, in.TxHash, in.From, treasury.FormatUSDC(in.Amount), report.ObservedAt.Format(time.RFC3339), "", product)
		if err != nil {
			return err
		}
		// 快照里的流入已经包含在余额中。未对账的手工、CCTP、Gateway 记录由 manualCodes 稍后标记。
		if row != nil && !pending[row.Code] {
			if err := models.MarkRevenueReconciled(row.Code); err != nil {
				return err
			}
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
		row.CircleProduct = d.Product
		row.CircleTxID = d.CircleTxID
		row.CircleState = d.CircleState
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
		if d.Action == treasury.ActionEscalate && !dryRunOutcome(d.Outcome) {
			code := d.DecisionHash
			req := d.RequestID
			if req == "" {
				req = "local"
			}
			if err := models.SaveApproval(code, req, d.Category, d.Payee, amount, d.DecisionHash, "pending", d.ReasonCode, d.Product, d.CircleTxID, d.CircleState); err != nil {
				return err
			}
		}
	}
	if err := SyncChainApprovals(report.Pending); err != nil {
		return err
	}
	cycle := models.NewCycleSnapshot()
	cycle.Code = report.RunID
	cycle.BalanceUnits = treasury.FormatUSDC(report.Balance)
	cycle.ObligationUnits = treasury.FormatUSDC(report.Liquidity.Obligations)
	cycle.ReserveFloorUnits = treasury.FormatUSDC(report.Liquidity.ReserveFloor)
	cycle.ReserveTargetUnits = treasury.FormatUSDC(report.Liquidity.ReserveTarget)
	cycle.SurplusUnits = treasury.FormatUSDC(report.Liquidity.Surplus)
	cycle.ObservedAt = report.ObservedAt.Format(time.RFC3339)
	cycle.CircleProduct = cfgProduct(report)
	if err := models.InsertCycle(cycle); err != nil {
		return err
	}
	if cfg.ChainDriver == "mock" {
		if err := saveMockState(mockStatePath(cfg), report); err != nil {
			return err
		}
	}
	if cfg.ChainDriver == "rpc" && report.Block > 0 {
		if err := treasury.SaveScanCursor(cfg.ScanCursor, cfg.Vault, report.Block); err != nil {
			return err
		}
	}
	return nil
}

func committedPayeeTimes(report treasury.Report) map[string]time.Time {
	out := map[string]time.Time{}
	for _, d := range report.Decisions {
		switch d.Outcome {
		case "paid", "simulated_paid", "circle_confirmed":
			if ts, ok := report.PayeePaidAt[d.Payee]; ok {
				out[treasury.NormalizeAddress(d.Payee)] = ts
			}
		}
	}
	return out
}

func dryRunOutcome(outcome string) bool {
	return len(outcome) >= 7 && outcome[:7] == "dry_run"
}

func cfgProduct(report treasury.Report) string {
	for _, d := range report.Decisions {
		if d.Product != "" {
			return d.Product
		}
	}
	return ""
}

func payableState(d treasury.Decision) string {
	switch d.Action {
	case treasury.ActionPay:
		if d.Outcome == "simulated_paid" || d.Outcome == "paid" || d.Outcome == "circle_confirmed" {
			return "paid"
		}
	case treasury.ActionDefer:
		return "deferred"
	case treasury.ActionEscalate:
		if d.Outcome == "error" || d.Outcome == "agent_key_required" || dryRunOutcome(d.Outcome) {
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
