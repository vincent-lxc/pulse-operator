// 本文件是账单 CLI 用的导入、执行和导出。dry-run 不拨号。
package business

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/vincent-lxc/pulse-operator/operator/models"
	"github.com/vincent-lxc/pulse-operator/operator/procurement"
	"github.com/vincent-lxc/pulse-operator/operator/treasury"
	"gopkg.in/yaml.v3"
)

// BillFlags 是命令行带上的确认。零值不会确认主网，也不会放宽上限。
type BillFlags struct {
	UnderstandRealMoney bool
	Yes                 bool
	Auto                bool
	// OverrideCap 只放宽 max_bill 和 max_spend_per_run。月度限额和每日笔数不能放宽。
	OverrideCap bool
	// OverrideReason 随 OverrideCap 写入审计和决策理由，空字符串无效。
	OverrideReason string
}

// DefaultBillsFile 是命令行账单账本。go run 的二进制目录每次都变，所以账本放在当前工作目录。
const DefaultBillsFile = "data/bills.yaml"

// AddBill 记录一张尚未付款的账单。
func AddBill(vendor, domain, kind string, years int) (*models.Bill, error) {
	if err := models.EnsureStorage(); err != nil {
		return nil, err
	}
	domain = strings.ToLower(strings.TrimSpace(domain))
	if vendor == "" {
		vendor = "porkbun"
	}
	if vendor != "porkbun" || !strings.Contains(domain, ".") {
		return nil, fmt.Errorf("usage: bill add porkbun --domain name.tld")
	}
	if kind == "" {
		kind = "domain_register"
	}
	if years != 1 {
		return nil, fmt.Errorf("porkbun bills use a 1 year term; omit --years or pass --years 1")
	}
	cents, _ := procurement.FixtureQuoteCents(domain, kind)
	row := models.NewBill()
	row.Code = BillCode(domain, kind)
	row.Vendor = vendor
	row.Domain = domain
	row.Kind = kind
	row.Years = 1
	row.CategoryCode = "domains"
	row.State = "quoted"
	row.QuoteCents = cents
	row.Currency = "USDC"
	saved, err := models.InsertBill(row)
	if err != nil {
		return nil, err
	}
	if err := upsertBillFile(DefaultBillsFile, saved); err != nil {
		return nil, err
	}
	return saved, nil
}

// RunBills 逐张推进账单。主网闸门不过时不会构造付款客户端。
func RunBills(ctx context.Context, cfg treasury.Config, ids []string, flags BillFlags) ([]*models.Bill, error) {
	if !cycleMu.TryLock() {
		return nil, models.NewBusinessError("operator cycle already running")
	}
	defer cycleMu.Unlock()
	if err := models.EnsureStorage(); err != nil {
		return nil, err
	}
	if cfg.BillsFile == "" {
		cfg.BillsFile = DefaultBillsFile
	}
	if err := importBillFile(cfg.BillsFile); err != nil {
		return nil, err
	}
	rows, err := selectBills(ids)
	if err != nil {
		return nil, err
	}
	if err := authorizeBills(ctx, cfg, flags, len(rows)); err != nil {
		return nil, err
	}
	deps, err := depsFor(ctx, cfg)
	if err != nil {
		return nil, err
	}
	var out []*models.Bill
	for _, row := range rows {
		if err := AdvanceBill(ctx, row, deps); err != nil {
			_ = upsertBillFile(cfg.BillsFile, row)
			return out, err
		}
		if err := upsertBillFile(cfg.BillsFile, row); err != nil {
			return out, err
		}
		out = append(out, row)
	}
	return out, nil
}

func selectBills(ids []string) ([]*models.Bill, error) {
	rows, err := models.ListBills()
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		var open []*models.Bill
		for _, row := range rows {
			if row.State == "quoted" || row.State == "decided" || row.State == "deferred" || row.State == "vault_paid" || row.State == "bridged" || row.State == "failed_bridge" || row.State == "failed_merchant" {
				open = append(open, row)
			}
		}
		if len(open) == 0 {
			return nil, fmt.Errorf("no open bills")
		}
		return open, nil
	}
	want := map[string]bool{}
	for _, id := range ids {
		want[id] = true
	}
	found := map[string]bool{}
	var picked []*models.Bill
	for _, row := range rows {
		if want[row.Code] || want[row.Domain] {
			picked = append(picked, row)
			found[row.Code] = true
			found[row.Domain] = true
		}
	}
	for id := range want {
		if !found[id] {
			return nil, fmt.Errorf("bill not found")
		}
	}
	return picked, nil
}

// BillCode 把类型写进编号，注册和续费可以同时存在。
func BillCode(domain, kind string) string {
	tag := "register"
	if strings.Contains(strings.ToLower(kind), "renew") {
		tag = "renew"
	}
	domain = strings.ToLower(strings.TrimSpace(domain))
	return "bill-" + tag + "-" + strings.ReplaceAll(domain, ".", "-")
}

func authorizeBills(ctx context.Context, cfg treasury.Config, flags BillFlags, count int) error {
	ready, err := porkbunReady(cfg)
	if err != nil && cfg.Mode == "mainnet" {
		return err
	}
	gate := procurement.Gate{
		Mode: cfg.Mode, Vault: cfg.Vault, Procurement: cfg.Procurement.Address,
		BaseProcurement: cfg.Procurement.BaseAddress, PorkbunKey: ready,
		ConfirmMainnetEnv: os.Getenv("CONFIRM_MAINNET") == "1",
	}
	if max, err := treasury.ParseUSDCAllowZero(cfg.MaxSpendPerRunUSDC); err == nil {
		gate.MaxSpend = max
	}
	if max, err := treasury.ParseUSDCAllowZero(cfg.MaxBillUSDC); err == nil {
		gate.MaxBill = max
	}
	if cfg.Mode == "dry-run" || cfg.Mode == "" {
		return procurement.Check(gate, procurement.Flags{BillCount: count, Yes: flags.Yes, Auto: flags.Auto, UnderstandRealMoney: flags.UnderstandRealMoney})
	}
	if err := checkLocalSigners(cfg); err != nil {
		return err
	}
	rpc := os.Getenv(cfg.Secrets.RPCEnv)
	if rpc == "" {
		return fmt.Errorf("rpc url missing: set %s", cfg.Secrets.RPCEnv)
	}
	arcID, codeOK, err := procurement.ReadChain(ctx, rpc, cfg.Vault)
	if err != nil {
		return err
	}
	gate.ArcChainID = arcID
	gate.VaultCodeOK = codeOK
	if baseRPC := os.Getenv(cfg.Base.RPCEnv); baseRPC != "" {
		baseID, _, err := procurement.ReadChain(ctx, baseRPC, "")
		if err != nil {
			return err
		}
		gate.BaseChainID = baseID
	}
	if err := checkOnChainSigners(ctx, cfg, rpc); err != nil {
		return err
	}
	return procurement.Check(gate, procurement.Flags{
		UnderstandRealMoney: flags.UnderstandRealMoney, Yes: flags.Yes, Auto: flags.Auto, BillCount: count,
	})
}

func missingKey(who, envName, filePath string) error {
	envName = strings.TrimSpace(envName)
	filePath = strings.TrimSpace(filePath)
	switch {
	case envName != "" && filePath != "":
		return fmt.Errorf("%s key missing: set %s or %s", who, envName, filePath)
	case envName != "":
		return fmt.Errorf("%s key missing: set %s", who, envName)
	case filePath != "":
		return fmt.Errorf("%s key missing: set %s", who, filePath)
	default:
		return fmt.Errorf("%s key missing", who)
	}
}

func rawKeyMode(cfg treasury.Config) bool {
	if cfg.Mode == "mainnet" {
		return true
	}
	return cfg.Executor == "" || cfg.Executor == "raw-key"
}

// checkLocalSigners 在拨号之前核对私钥和配置地址。错误里不出现密钥。
func checkLocalSigners(cfg treasury.Config) error {
	if !strictVault(cfg.Mode) {
		return nil
	}
	agentKey, err := treasury.LoadPrivateKey(cfg.Secrets.AgentKeyEnv, cfg.Secrets.AgentKeyFile)
	if err != nil {
		return err
	}
	if agentKey == nil {
		return missingKey("agent", cfg.Secrets.AgentKeyEnv, cfg.Secrets.AgentKeyFile)
	}
	agentAddr := crypto.PubkeyToAddress(agentKey.PublicKey)
	if !common.IsHexAddress(cfg.Agent) || common.HexToAddress(cfg.Agent) != agentAddr {
		return fmt.Errorf("agent key address %s does not match config agent %s", agentAddr.Hex(), cfg.Agent)
	}
	procKey, err := treasury.LoadPrivateKey(cfg.Procurement.KeyEnv, cfg.Procurement.KeyFile)
	if err != nil {
		return err
	}
	if procKey == nil {
		return missingKey("procurement", cfg.Procurement.KeyEnv, cfg.Procurement.KeyFile)
	}
	procAddr := crypto.PubkeyToAddress(procKey.PublicKey)
	if !common.IsHexAddress(cfg.Procurement.Address) || common.HexToAddress(cfg.Procurement.Address) != procAddr {
		return fmt.Errorf("procurement key address %s does not match procurement.address %s", procAddr.Hex(), cfg.Procurement.Address)
	}
	if rawKeyMode(cfg) {
		base := strings.TrimSpace(cfg.Procurement.BaseAddress)
		if base != "" && !strings.EqualFold(base, cfg.Procurement.Address) {
			return fmt.Errorf("procurement.baseAddress must be empty or equal procurement.address in raw-key mode")
		}
	}
	return nil
}

// checkOnChainSigners 只读金库 agent() 和原生余额。不发送交易。
func checkOnChainSigners(ctx context.Context, cfg treasury.Config, rpc string) error {
	if !strictVault(cfg.Mode) {
		return nil
	}
	agentKey, err := treasury.LoadPrivateKey(cfg.Secrets.AgentKeyEnv, cfg.Secrets.AgentKeyFile)
	if err != nil {
		return err
	}
	if agentKey == nil {
		return missingKey("agent", cfg.Secrets.AgentKeyEnv, cfg.Secrets.AgentKeyFile)
	}
	agentAddr := crypto.PubkeyToAddress(agentKey.PublicKey)
	onchain, err := procurement.VaultAgent(ctx, rpc, cfg.Vault)
	if err != nil {
		return err
	}
	if onchain != agentAddr {
		return fmt.Errorf("config agent %s does not match PolicyVault.agent() %s", agentAddr.Hex(), onchain.Hex())
	}
	floor, label, err := nativeGasFloor(cfg)
	if err != nil {
		return err
	}
	procAddr := common.HexToAddress(cfg.Procurement.Address)
	if err := requireNative(ctx, rpc, agentAddr, floor, label, "agent"); err != nil {
		return err
	}
	return requireNative(ctx, rpc, procAddr, floor, label, "procurement wallet")
}

func nativeGasFloor(cfg treasury.Config) (*big.Int, string, error) {
	raw := strings.TrimSpace(cfg.MinGasUSDC)
	if raw == "" {
		raw = "0.05"
	}
	units, err := treasury.ParseUSDCAllowZero(raw)
	if err != nil {
		return nil, raw, fmt.Errorf("minGasUSDC: %w", err)
	}
	// Arc 原生余额是 18 位 wei。配置里的 USDC 是 6 位小数，1 USDC = 10^12 wei。
	return new(big.Int).Mul(units, big.NewInt(1_000_000_000_000)), raw, nil
}

func requireNative(ctx context.Context, rpc string, account common.Address, floor *big.Int, label, who string) error {
	bal, err := procurement.NativeBalance(ctx, rpc, account.Hex())
	if err != nil {
		return fmt.Errorf("read %s native balance: %w", who, err)
	}
	if floor != nil && bal.Cmp(floor) < 0 {
		return fmt.Errorf("%s native balance is below minGasUSDC %s", who, label)
	}
	return nil
}

func porkbunReady(cfg treasury.Config) (bool, error) {
	key, err := treasury.LoadSecret(cfg.Porkbun.APIKeyEnv, cfg.Porkbun.APIKeyFile)
	if err != nil {
		return false, err
	}
	secret, err := treasury.LoadSecret(cfg.Porkbun.SecretEnv, cfg.Porkbun.SecretFile)
	if err != nil {
		return false, err
	}
	return key != "" && secret != "", nil
}

func depsFor(ctx context.Context, cfg treasury.Config) (billDeps, error) {
	buffer, err := treasury.ParseUSDCAllowZero(cfg.Porkbun.FeeBufferUSDC)
	if err != nil {
		return billDeps{}, err
	}
	policy, err := cfg.PolicyFrom(time.Time{})
	if err != nil {
		return billDeps{}, err
	}
	audit, err := treasury.OpenAudit(cfg.AuditLog)
	if err != nil && cfg.AuditLog != "" {
		return billDeps{}, err
	}
	payee := cfg.Procurement.Address
	if payee == "" {
		payee = "0x0000000000000000000000000000000000000001"
	}
	deps := billDeps{
		cfg: cfg, policy: policy, payee: payee, buffer: buffer, plan: planFunc(cfg), audit: audit,
		save: models.SaveBill, runSpent: big.NewInt(0), pendingSpend: big.NewInt(0),
	}
	if cfg.Mode == "dry-run" || cfg.Mode == "" {
		deps.quote = func(_ context.Context, bill *models.Bill) (int64, *big.Int, error) {
			cents, ok := procurement.FixtureQuoteCents(bill.Domain, bill.Kind)
			if !ok {
				return 0, nil, fmt.Errorf("no fixture price for %s", bill.Domain)
			}
			return cents, big.NewInt(54_050), nil
		}
		deps.facts = func(bill *models.Bill, cents int64, amount *big.Int) procurement.Facts {
			return dryFacts(bill, cents, amount, cfg, deps.runSpent)
		}
		deps.vault = func(_ context.Context, bill *models.Bill, _ *big.Int, _ string) (vaultResult, error) {
			return vaultResult{TxHash: "dry-vault-" + bill.Code, Status: "paid"}, nil
		}
		deps.burn = func(_ context.Context, bill *models.Bill, _ *big.Int, fee *big.Int) (burnResult, error) {
			return burnResult{BurnTx: "dry-burn-" + bill.Code, MintTx: "dry-mint-" + bill.Code, MessageHash: "dry-message", Nonce: "0", ForwardFee: fee.String()}, nil
		}
		deps.merchant = func(_ context.Context, bill *models.Bill) (merchantResult, error) {
			return merchantResult{OrderID: "dry-" + bill.Code, Scheme: "exact"}, nil
		}
		return deps, nil
	}
	return liveDeps(ctx, cfg, deps)
}

func dryFacts(bill *models.Bill, cents int64, amount *big.Int, cfg treasury.Config, runSpent *big.Int) procurement.Facts {
	spent, daily := billUsage(bill.Code)
	facts := procurement.Facts{
		PayeeAllowed: true, Amount: amount, QuoteCents: cents,
		MonthlySpent: spent, MonthlyLimit: cfg.Porkbun.MonthlyLimitCents,
		DailyCount: daily, DailyCap: cfg.Porkbun.DailyCap,
		ToleranceCents: cfg.Porkbun.PriceToleranceCents,
		MaxBill:        optionalUSDC(cfg.MaxBillUSDC), MaxSpend: optionalUSDC(cfg.MaxSpendPerRunUSDC),
	}
	if runSpent != nil {
		facts.RunSpent = new(big.Int).Set(runSpent)
	}
	path := cfg.VaultFixture
	if path == "" {
		return facts
	}
	snap, err := treasury.LoadFixture(path)
	if err != nil {
		return facts
	}
	facts.Balance = subFloor(snap.Balance, runSpent)
	cat, ok := snap.Categories["domains"]
	if !ok || !cat.Enabled {
		return facts
	}
	facts.CategoryEnabled = true
	facts.Remaining = subFloor(cat.Remaining, runSpent)
	facts.PerTxCap = cat.PerTxCap
	return facts
}

func subFloor(have, spent *big.Int) *big.Int {
	if have == nil {
		return nil
	}
	if spent == nil || spent.Sign() <= 0 {
		return new(big.Int).Set(have)
	}
	out := new(big.Int).Sub(have, spent)
	if out.Sign() < 0 {
		return big.NewInt(0)
	}
	return out
}

func optionalUSDC(raw string) *big.Int {
	n, err := treasury.ParseUSDCAllowZero(raw)
	if err != nil || n == nil || n.Sign() <= 0 {
		return nil
	}
	return n
}

func billUsage(skip string) (int64, int) {
	rows, err := models.ListBills()
	if err != nil {
		return 0, 0
	}
	var spent int64
	var daily int
	for _, row := range rows {
		if row.Code == skip {
			continue
		}
		switch row.State {
		case "done", "merchant_paid", "kept_as_credit":
			spent += row.QuoteCents
			daily++
		}
	}
	return spent, daily
}

type billFile struct {
	Bills []billDisk `yaml:"bills"`
}

type billDisk struct {
	ID           string `yaml:"id"`
	Vendor       string `yaml:"vendor"`
	Kind         string `yaml:"kind"`
	Domain       string `yaml:"domain"`
	Years        int    `yaml:"years"`
	QuoteCents   int64  `yaml:"quoteCents"`
	State        string `yaml:"state"`
	DecisionHash string `yaml:"decisionHash"`
	Planner      string `yaml:"planner"`
	Action       string `yaml:"action"`
	ReasonCode   string `yaml:"reasonCode"`
	Reason       string `yaml:"reason"`
	Rationale    string `yaml:"rationale"`
	ModelID      string `yaml:"modelID"`
	PromptHash   string `yaml:"promptHash"`
	VaultTx      string `yaml:"vaultTx"`
	BurnTx       string `yaml:"burnTx"`
	MintTx       string `yaml:"mintTx"`
	OrderID      string `yaml:"orderID"`
	Mode         string `yaml:"mode"`
}

func importBillFile(path string) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var file billFile
	if err := yaml.Unmarshal(raw, &file); err != nil {
		return err
	}
	for _, item := range file.Bills {
		if strings.TrimSpace(item.Domain) == "" && strings.TrimSpace(item.ID) == "" {
			continue
		}
		code := item.ID
		if code == "" {
			code = BillCode(item.Domain, item.Kind)
		}
		existing, err := models.FindBill(code)
		if err != nil {
			return err
		}
		if existing != nil {
			continue
		}
		row := models.NewBill()
		row.Code = code
		row.Vendor = item.Vendor
		if row.Vendor == "" {
			row.Vendor = "porkbun"
		}
		row.Domain = strings.ToLower(item.Domain)
		row.Kind = item.Kind
		if row.Kind == "" {
			row.Kind = "domain_register"
		}
		row.Years = 1
		row.CategoryCode = "domains"
		row.Currency = "USDC"
		row.QuoteCents = item.QuoteCents
		row.State = item.State
		if row.State == "" {
			row.State = "quoted"
		}
		row.DecisionHash = item.DecisionHash
		row.Planner = item.Planner
		row.Action = item.Action
		row.ReasonCode = item.ReasonCode
		row.Reason = item.Reason
		row.Rationale = item.Rationale
		row.ModelID = item.ModelID
		row.PromptHash = item.PromptHash
		row.VaultTx = item.VaultTx
		row.CCTPBurnTx = item.BurnTx
		row.BaseMintTx = item.MintTx
		row.PorkbunOrderID = item.OrderID
		row.Mode = item.Mode
		if _, err := models.InsertBill(row); err != nil {
			return err
		}
	}
	return nil
}

func upsertBillFile(path string, row *models.Bill) error {
	if strings.TrimSpace(path) == "" || row == nil {
		return nil
	}
	var file billFile
	if raw, err := os.ReadFile(path); err == nil {
		if err := yaml.Unmarshal(raw, &file); err != nil {
			return err
		}
	}
	disk := billDisk{
		ID: row.Code, Vendor: row.Vendor, Kind: row.Kind, Domain: row.Domain, Years: row.Years,
		QuoteCents: row.QuoteCents, State: row.State, DecisionHash: row.DecisionHash, Planner: row.Planner, Action: row.Action,
		ReasonCode: row.ReasonCode, Reason: row.Reason, Rationale: row.Rationale, ModelID: row.ModelID,
		PromptHash: row.PromptHash, VaultTx: row.VaultTx, BurnTx: row.CCTPBurnTx, MintTx: row.BaseMintTx,
		OrderID: row.PorkbunOrderID, Mode: row.Mode,
	}
	replaced := false
	for i := range file.Bills {
		if file.Bills[i].ID == row.Code {
			file.Bills[i] = disk
			replaced = true
			break
		}
	}
	if !replaced {
		file.Bills = append(file.Bills, disk)
	}
	raw, err := yaml.Marshal(file)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepathDir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, raw, 0o644)
}

func filepathDir(path string) string {
	i := strings.LastIndex(path, "/")
	if i <= 0 {
		return "."
	}
	return path[:i]
}

// LoadDefaultBills 把工作目录里的账本导入当前进程的数据库。
func LoadDefaultBills() error {
	if err := models.EnsureStorage(); err != nil {
		return err
	}
	return importBillFile(DefaultBillsFile)
}

// ExportBill 返回 JSON 和 markdown。
func ExportBill(id string) (string, string, error) {
	if err := models.EnsureStorage(); err != nil {
		return "", "", err
	}
	row, err := models.FindBill(id)
	if err != nil {
		return "", "", err
	}
	if row == nil {
		return "", "", fmt.Errorf("bill not found")
	}
	chain := ""
	if row.Evidence != "" {
		var prev map[string]any
		if json.Unmarshal([]byte(row.Evidence), &prev) == nil {
			if v, ok := prev["vault_chain"].(string); ok {
				chain = v
			}
		}
	}
	return evidence(row, chain), EvidenceMarkdown(row), nil
}

// ReturnFloatPlan 只描述把采购钱包剩余 USDC 转回金库。dry-run 不发送。
func ReturnFloatPlan(cfg treasury.Config, amount string) (string, error) {
	units, err := treasury.ParseUSDCAllowZero(amount)
	if err != nil || units == nil || units.Sign() <= 0 {
		return "", fmt.Errorf("amount must be positive USDC")
	}
	to := common.HexToAddress(cfg.Vault)
	data, err := procurement.EncodeERC20Transfer(to, units)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("return %s USDC from procurement to %s calldata %x", amount, to.Hex(), data), nil
}
