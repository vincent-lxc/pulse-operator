package business

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"

	"github.com/vincent-lxc/pulse-operator/operator/models"
	"github.com/vincent-lxc/pulse-operator/operator/procurement"
	"github.com/vincent-lxc/pulse-operator/operator/treasury"
)

func TestSimulatedVaultDoesNotBurnOnMainnet(t *testing.T) {
	if err := models.EnsureStorage(); err != nil {
		t.Fatal(err)
	}
	cases := []vaultResult{
		{Status: "dry_run_paid"},
		{Status: "paid"},
		{Status: "simulated_paid", TxHash: "0x" + strings.Repeat("ab", 32)},
	}
	for i, res := range cases {
		bill := models.NewBill()
		bill.Code = fmt.Sprintf("bill-sim-%d-%s", i, strings.ReplaceAll(t.Name(), "/", "-"))
		bill.Domain = "pulse.xyz"
		bill.Vendor = "porkbun"
		bill.Kind = "domain_register"
		bill.CategoryCode = "domains"
		bill.State = "quoted"
		if _, err := models.InsertBill(bill); err != nil {
			t.Fatal(err)
		}
		var burns, orders int
		pending := big.NewInt(0)
		deps := generousDeps(t)
		deps.cfg.Mode = "mainnet"
		deps.pendingSpend = pending
		deps.vault = func(context.Context, *models.Bill, *big.Int, string) (vaultResult, error) {
			return res, nil
		}
		deps.burn = func(context.Context, *models.Bill, *big.Int, *big.Int) (burnResult, error) {
			burns++
			return burnResult{BurnTx: "0x" + strings.Repeat("cd", 32)}, nil
		}
		deps.merchant = func(context.Context, *models.Bill) (merchantResult, error) {
			orders++
			return merchantResult{OrderID: "should-not"}, nil
		}
		err := AdvanceBill(context.Background(), bill, deps)
		if bill.State != "failed_vault" || bill.ReasonCode != "vault_not_broadcast" || burns != 0 || orders != 0 || pending.Sign() == 0 {
			t.Fatalf("case %d state=%s code=%s burns=%d orders=%d pending=%s err=%v", i, bill.State, bill.ReasonCode, burns, orders, pending, err)
		}
		if err == nil || !strings.Contains(err.Error(), "vault_not_broadcast") || strings.Contains(err.Error(), agentHexSnippet(res.TxHash)) {
			t.Fatalf("case %d err %v", i, err)
		}
	}
}

func TestDryRunStillAcceptsSimulatedPay(t *testing.T) {
	if err := models.EnsureStorage(); err != nil {
		t.Fatal(err)
	}
	bill := models.NewBill()
	bill.Code = "bill-dry-sim-" + strings.ReplaceAll(t.Name(), "/", "-")
	bill.Domain = "pulse.xyz"
	bill.Vendor = "porkbun"
	bill.Kind = "domain_register"
	bill.CategoryCode = "domains"
	bill.State = "quoted"
	if _, err := models.InsertBill(bill); err != nil {
		t.Fatal(err)
	}
	var burns int
	deps := generousDeps(t)
	deps.vault = func(context.Context, *models.Bill, *big.Int, string) (vaultResult, error) {
		return vaultResult{TxHash: "dry-vault", Status: "dry_run_paid"}, nil
	}
	deps.burn = func(context.Context, *models.Bill, *big.Int, *big.Int) (burnResult, error) {
		burns++
		return burnResult{BurnTx: "dry-burn", MintTx: "dry-mint"}, nil
	}
	if err := AdvanceBill(context.Background(), bill, deps); err != nil {
		t.Fatal(err)
	}
	if bill.State != "done" || burns != 1 {
		t.Fatalf("state=%s burns=%d", bill.State, burns)
	}
}

func TestMainnetSignerPreflightRefusesBeforeSend(t *testing.T) {
	agentKey, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	procKey, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	agentHex := hex.EncodeToString(crypto.FromECDSA(agentKey))
	procHex := hex.EncodeToString(crypto.FromECDSA(procKey))
	agent := crypto.PubkeyToAddress(agentKey.PublicKey)
	proc := crypto.PubkeyToAddress(procKey.PublicKey)
	other := common.HexToAddress("0x4444444444444444444444444444444444444444")
	clearCircle(t)
	t.Setenv("PORKBUN_API_KEY", "pk")
	t.Setenv("PORKBUN_SECRET_API_KEY", "ps")

	dialed := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		dialed++
		http.Error(w, "should not dial", http.StatusInternalServerError)
	}))
	defer srv.Close()
	t.Setenv("ARC_RPC_URL", srv.URL)

	base := treasury.Config{
		Mode: "mainnet", ChainID: "5042", Vault: "0x2222222222222222222222222222222222222222",
		Agent: agent.Hex(), MaxSpendPerRunUSDC: "15", MaxBillUSDC: "15",
		Secrets:     treasury.SecretConfig{RPCEnv: "ARC_RPC_URL", AgentKeyEnv: "OPERATOR_PRIVATE_KEY"},
		Procurement: treasury.ProcurementConfig{Address: proc.Hex(), KeyEnv: "PROCUREMENT_PRIVATE_KEY"},
		Executor:    "raw-key",
		Porkbun:     treasury.PorkbunConfig{APIKeyEnv: "PORKBUN_API_KEY", SecretEnv: "PORKBUN_SECRET_API_KEY"},
	}

	t.Setenv("OPERATOR_PRIVATE_KEY", "")
	t.Setenv("PROCUREMENT_PRIVATE_KEY", "")
	err = authorizeBills(context.Background(), base, BillFlags{UnderstandRealMoney: true, Yes: true}, 1)
	if err == nil || !strings.Contains(err.Error(), "OPERATOR_PRIVATE_KEY") || strings.Contains(err.Error(), agentHex) {
		t.Fatalf("missing agent: %v", err)
	}

	t.Setenv("OPERATOR_PRIVATE_KEY", agentHex)
	err = authorizeBills(context.Background(), base, BillFlags{UnderstandRealMoney: true, Yes: true}, 1)
	if err == nil || !strings.Contains(err.Error(), "PROCUREMENT_PRIVATE_KEY") || strings.Contains(err.Error(), procHex) {
		t.Fatalf("missing procurement: %v", err)
	}

	t.Setenv("PROCUREMENT_PRIVATE_KEY", procHex)
	mismatchAgent := base
	mismatchAgent.Agent = other.Hex()
	err = authorizeBills(context.Background(), mismatchAgent, BillFlags{UnderstandRealMoney: true, Yes: true}, 1)
	if err == nil || !strings.Contains(err.Error(), "config agent") || strings.Contains(err.Error(), agentHex) {
		t.Fatalf("agent mismatch: %v", err)
	}

	mismatchProc := base
	mismatchProc.Procurement.Address = other.Hex()
	err = authorizeBills(context.Background(), mismatchProc, BillFlags{UnderstandRealMoney: true, Yes: true}, 1)
	if err == nil || !strings.Contains(err.Error(), "procurement.address") || strings.Contains(err.Error(), procHex) {
		t.Fatalf("procurement mismatch: %v", err)
	}

	badBase := base
	badBase.Procurement.BaseAddress = other.Hex()
	err = authorizeBills(context.Background(), badBase, BillFlags{UnderstandRealMoney: true, Yes: true}, 1)
	if err == nil || !strings.Contains(err.Error(), "baseAddress") || strings.Contains(err.Error(), procHex) {
		t.Fatalf("base address: %v", err)
	}
	if dialed != 0 {
		t.Fatalf("dialed %d times before the chain checks", dialed)
	}

	walletsClientBuilds.Store(0)
	_, err = merchantSigner(treasury.Config{
		Mode: "mainnet",
		Procurement: treasury.ProcurementConfig{
			Address: other.Hex(), KeyEnv: "PROCUREMENT_PRIVATE_KEY",
		},
	})
	if err == nil || strings.Contains(err.Error(), procHex) || walletsClientBuilds.Load() != 0 {
		t.Fatalf("signer %v builds %d", err, walletsClientBuilds.Load())
	}
	t.Setenv("PROCUREMENT_PRIVATE_KEY", "")
	_, _, err = sendProcurement(context.Background(), treasury.Config{
		Mode:        "mainnet",
		Procurement: treasury.ProcurementConfig{KeyEnv: "PROCUREMENT_PRIVATE_KEY"},
	}, other, []byte{1}, "x", nil, "idem")
	if err == nil || !strings.Contains(err.Error(), "PROCUREMENT_PRIVATE_KEY") || walletsClientBuilds.Load() != 0 {
		t.Fatalf("send %v builds %d", err, walletsClientBuilds.Load())
	}
}

func TestOnChainAgentAndGasFloor(t *testing.T) {
	agentKey, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	procKey, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	agent := crypto.PubkeyToAddress(agentKey.PublicKey)
	proc := crypto.PubkeyToAddress(procKey.PublicKey)
	t.Setenv("OPERATOR_PRIVATE_KEY", hex.EncodeToString(crypto.FromECDSA(agentKey)))
	t.Setenv("PROCUREMENT_PRIVATE_KEY", hex.EncodeToString(crypto.FromECDSA(procKey)))
	t.Setenv("PORKBUN_API_KEY", "pk")
	t.Setenv("PORKBUN_SECRET_API_KEY", "ps")
	vault := common.HexToAddress("0x2222222222222222222222222222222222222222")
	hits := &rpcLog{}
	srv := httptest.NewServer(scriptedChain("0x13b2", map[string]string{
		selectorHex("agent()"):        addressWord(common.HexToAddress("0x5555555555555555555555555555555555555555")),
		selectorHex("pendingCount()"): word(big.NewInt(0)),
	}, "0x0", hits))
	defer srv.Close()
	t.Setenv("ARC_RPC_URL", srv.URL)
	cfg := treasury.Config{
		Mode: "mainnet", ChainID: "5042", Vault: vault.Hex(), Agent: agent.Hex(),
		MaxSpendPerRunUSDC: "15", MaxBillUSDC: "15", Executor: "raw-key",
		Secrets:     treasury.SecretConfig{RPCEnv: "ARC_RPC_URL", AgentKeyEnv: "OPERATOR_PRIVATE_KEY"},
		Procurement: treasury.ProcurementConfig{Address: proc.Hex(), KeyEnv: "PROCUREMENT_PRIVATE_KEY"},
		Porkbun:     treasury.PorkbunConfig{APIKeyEnv: "PORKBUN_API_KEY", SecretEnv: "PORKBUN_SECRET_API_KEY"},
	}
	err = authorizeBills(context.Background(), cfg, BillFlags{UnderstandRealMoney: true, Yes: true}, 1)
	if err == nil || !strings.Contains(err.Error(), "PolicyVault.agent()") {
		t.Fatalf("%v methods %v", err, hits.methods())
	}
	if hits.has("eth_sendRawTransaction") {
		t.Fatal(hits.methods())
	}

	hits.reset()
	srv2 := httptest.NewServer(scriptedChain("0x13b2", map[string]string{
		selectorHex("agent()"):        addressWord(agent),
		selectorHex("pendingCount()"): word(big.NewInt(0)),
	}, "0x0", hits))
	defer srv2.Close()
	t.Setenv("ARC_RPC_URL", srv2.URL)
	err = authorizeBills(context.Background(), cfg, BillFlags{UnderstandRealMoney: true, Yes: true}, 1)
	if err == nil || !strings.Contains(err.Error(), "minGasUSDC") {
		t.Fatalf("%v", err)
	}
	if hits.has("eth_sendRawTransaction") {
		t.Fatal(hits.methods())
	}
}

func TestMainnetRawKeyBillReachesDoneWithoutCircle(t *testing.T) {
	bill, _, sends := runFakeMainnetBill(t, fakeBillOpts{tag: "done", forward: "0x" + strings.Repeat("aa", 32), baseBalance: big.NewInt(10_000_000)})
	if bill.State != "done" || bill.PorkbunOrderID != "ord-raw" || !realTxHash(bill.VaultTx) || bill.CCTPBurnTx == "" || bill.BaseMintTx == "" {
		t.Fatalf("%+v sends %d", bill, sends.n())
	}
	if walletsClientBuilds.Load() != 0 {
		t.Fatalf("circle client builds %d", walletsClientBuilds.Load())
	}
}

func TestAwaitingMintDoesNotSignOrBurnAgain(t *testing.T) {
	bill, again, sends := runFakeMainnetBill(t, fakeBillOpts{
		tag: "wait", forward: "", baseBalance: big.NewInt(10_000_000), poll: 300 * time.Millisecond, again: true,
	})
	if bill.State != "bridged" || bill.ReasonCode != "awaiting_mint" || bill.BaseMintTx != "" || bill.CCTPBurnTx == "" {
		t.Fatalf("no forward: %+v", bill)
	}
	if sends.signatures.Load() != 0 || sends.payPosts.Load() != 0 {
		t.Fatalf("signed during wait sig=%d pay=%d", sends.signatures.Load(), sends.payPosts.Load())
	}
	if again == nil || again.State != "bridged" || again.ReasonCode != "awaiting_mint" || sends.n() != sends.afterFirst || sends.signatures.Load() != 0 {
		t.Fatalf("retry %+v sends %d after %d sig=%d", again, sends.n(), sends.afterFirst, sends.signatures.Load())
	}

	low, retried, lowSends := runFakeMainnetBill(t, fakeBillOpts{
		tag: "low", forward: "0x" + strings.Repeat("bb", 32), baseBalance: big.NewInt(1), again: true,
	})
	if low.State != "bridged" || low.ReasonCode != "awaiting_mint" || low.BaseMintTx == "" {
		t.Fatalf("low balance: %+v", low)
	}
	if lowSends.signatures.Load() != 0 {
		t.Fatalf("signed with low balance")
	}
	if retried == nil || retried.ReasonCode != "awaiting_mint" || lowSends.n() != lowSends.afterFirst || lowSends.signatures.Load() != 0 {
		state := ""
		if retried != nil {
			state = retried.State
		}
		t.Fatalf("retry burned again sends %d->%d state %s", lowSends.afterFirst, lowSends.n(), state)
	}
}

type fakeBillOpts struct {
	tag         string
	forward     string
	baseBalance *big.Int
	poll        time.Duration
	again       bool
}

type sendCounter struct {
	mu         sync.Mutex
	nSends     int
	afterFirst int
	signatures *syncInt
	payPosts   *syncInt
	cfg        treasury.Config
}

type syncInt struct{ n int64 }

func (s *syncInt) Add(d int64) { s.n += d }
func (s *syncInt) Load() int64 { return s.n }

func (s *sendCounter) n() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.nSends
}

func runFakeMainnetBill(t *testing.T, opt fakeBillOpts) (*models.Bill, *models.Bill, *sendCounter) {
	t.Helper()
	if err := models.EnsureStorage(); err != nil {
		t.Fatal(err)
	}
	clearCircle(t)
	walletsClientBuilds.Store(0)
	agentKey, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	procKey, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	agent := crypto.PubkeyToAddress(agentKey.PublicKey)
	proc := crypto.PubkeyToAddress(procKey.PublicKey)
	t.Setenv("OPERATOR_PRIVATE_KEY", hex.EncodeToString(crypto.FromECDSA(agentKey)))
	t.Setenv("PROCUREMENT_PRIVATE_KEY", hex.EncodeToString(crypto.FromECDSA(procKey)))
	t.Setenv("CONFIRM_MAINNET", "1")
	t.Setenv("PORKBUN_API_KEY", "pk")
	t.Setenv("PORKBUN_SECRET_API_KEY", "ps")
	vault := common.HexToAddress("0x2222222222222222222222222222222222222222")
	counter := &sendCounter{signatures: &syncInt{}, payPosts: &syncInt{}}
	views := vaultViews(t, agent)
	arc := httptest.NewServer(arcChain(vault, views, counter))
	defer arc.Close()
	base := httptest.NewServer(scriptedChain("0x2105", map[string]string{
		selectorHex("balanceOf(address)"): word(opt.baseBalance),
	}, word(big.NewInt(0)), &rpcLog{}))
	defer base.Close()
	iris := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/fees/") {
			_, _ = io.WriteString(w, `[{"finalityThreshold":1000,"minimumFee":0,"forwardFee":{"low":54050,"med":54050,"high":54372}}]`)
			return
		}
		body := `{"messages":[{"status":"complete","message":"0x01","eventNonce":"1","attestation":"0x02"}]}`
		if opt.forward != "" {
			body = fmt.Sprintf(`{"messages":[{"status":"complete","message":"0x01","eventNonce":"1","attestation":"0x02","forwardTxHash":"%s"}]}`, opt.forward)
		}
		_, _ = io.WriteString(w, body)
	}))
	defer iris.Close()
	required := base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf(
		`{"x402Version":2,"accepts":[{"scheme":"exact","network":"eip155:8453","amount":"2040000","asset":"%s","payTo":"0x1111111111111111111111111111111111111111","maxTimeoutSeconds":300,"extra":{"name":"USDC","version":"2"}}]}`,
		procurement.BaseUSDC,
	)))
	pork := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), `"dryRun":true`) {
			_, _ = io.WriteString(w, `{"status":"SUCCESS","cost":204}`)
			return
		}
		counter.payPosts.Add(1)
		if r.Header.Get("PAYMENT-SIGNATURE") == "" {
			w.Header().Set("PAYMENT-REQUIRED", required)
			w.WriteHeader(http.StatusPaymentRequired)
			_, _ = io.WriteString(w, `{"status":"ERROR","code":"PAYMENT_REQUIRED","checkoutId":"chk-1","message":"sign"}`)
			return
		}
		counter.signatures.Add(1)
		_, _ = io.WriteString(w, `{"status":"SUCCESS","orderId":"ord-raw"}`)
	}))
	defer pork.Close()
	t.Setenv("ARC_RPC_URL", arc.URL)
	t.Setenv("BASE_RPC_URL", base.URL)
	poll := opt.poll
	if poll <= 0 {
		poll = 3 * time.Minute
	}
	cfg := treasury.Config{
		Mode: "mainnet", AgentID: "pulse-operator", ChainID: "5042",
		Vault: vault.Hex(), Agent: agent.Hex(),
		USDC:        "0x3600000000000000000000000000000000000000",
		ChainDriver: "rpc", Executor: "raw-key",
		ReserveFloorUSDC: "0", ReserveTargetUSDC: "0",
		MaxSpendPerRunUSDC: "15", MaxBillUSDC: "15",
		AuditLog:  t.TempDir() + "/audit.jsonl",
		BillsFile: t.TempDir() + "/bills.yaml",
		Gas:       treasury.GasConfig{MaxFeePerGasGwei: 50},
		Secrets:   treasury.SecretConfig{RPCEnv: "ARC_RPC_URL", AgentKeyEnv: "OPERATOR_PRIVATE_KEY", OwnerKeyEnv: "OWNER_PRIVATE_KEY"},
		Porkbun: treasury.PorkbunConfig{
			APIBase: pork.URL, APIKeyEnv: "PORKBUN_API_KEY", SecretEnv: "PORKBUN_SECRET_API_KEY",
			MonthlyLimitCents: 10000, DailyCap: 10, FeeBufferUSDC: "0.02",
		},
		Procurement: treasury.ProcurementConfig{Address: proc.Hex(), KeyEnv: "PROCUREMENT_PRIVATE_KEY"},
		Base:        treasury.BaseChainConfig{ChainID: "8453", RPCEnv: "BASE_RPC_URL", USDC: procurement.BaseUSDC},
		CCTP: treasury.BridgeConfig{
			Forward: true, FeeLevel: "med", SourceDomain: 26, DestDomain: 6,
			TokenMessenger: procurement.MainnetTokenMessenger, IrisBase: iris.URL, PollTimeout: poll,
		},
		Circle:  treasury.CircleConfig{IrisBase: iris.URL},
		Planner: treasury.PlannerConfig{Driver: "rules"},
	}
	counter.cfg = cfg
	domain := strings.ToLower(opt.tag+"-"+strings.ReplaceAll(t.Name(), "/", "-")) + ".xyz"
	row := models.NewBill()
	row.Code = BillCode(domain, "domain_register")
	row.Vendor = "porkbun"
	row.Domain = domain
	row.Kind = "domain_register"
	row.Years = 1
	row.CategoryCode = "domains"
	row.State = "quoted"
	row.Currency = "USDC"
	if _, err := models.InsertBill(row); err != nil {
		t.Fatal(err)
	}
	out, err := RunBills(context.Background(), cfg, []string{row.Code}, BillFlags{UnderstandRealMoney: true, Yes: true})
	if err != nil || len(out) != 1 {
		state := ""
		if len(out) == 1 {
			state = out[0].State + " " + out[0].ReasonCode + " " + out[0].Reason
		}
		t.Fatalf("%v state %s sends %d", err, state, counter.n())
	}
	var second *models.Bill
	if opt.again {
		counter.afterFirst = counter.n()
		retried, err := RunBills(context.Background(), cfg, []string{row.Code}, BillFlags{UnderstandRealMoney: true, Yes: true})
		if err != nil || len(retried) != 1 {
			t.Fatalf("retry %v %+v", err, retried)
		}
		second = retried[0]
	}
	return out[0], second, counter
}

func vaultViews(t *testing.T, agent common.Address) map[string]string {
	t.Helper()
	cat, err := packCategory()
	if err != nil {
		t.Fatal(err)
	}
	allowed, err := packBool(true)
	if err != nil {
		t.Fatal(err)
	}
	paused, err := packBool(false)
	if err != nil {
		t.Fatal(err)
	}
	ids, err := packUintArray(nil)
	if err != nil {
		t.Fatal(err)
	}
	return map[string]string{
		selectorHex("pendingCount()"):                  word(big.NewInt(0)),
		selectorHex("agent()"):                         addressWord(agent),
		selectorHex("balance()"):                       word(big.NewInt(100_000_000)),
		selectorHex("paused()"):                        paused,
		selectorHex("reserve()"):                       addressWord(common.HexToAddress("0x6666666666666666666666666666666666666666")),
		selectorHex("getCategory(bytes32)"):            cat,
		selectorHex("isPayeeAllowed(bytes32,address)"): allowed,
		selectorHex("pendingRequestIds()"):             ids,
		selectorHex("balanceOf(address)"):              word(big.NewInt(100_000_000)),
	}
}

func packCategory() (string, error) {
	parsed, err := abi.JSON(strings.NewReader(`[{"type":"function","name":"getCategory","stateMutability":"view","inputs":[{"name":"category","type":"bytes32"}],"outputs":[{"name":"v","type":"tuple","components":[{"name":"enabled","type":"bool"},{"name":"budget","type":"uint256"},{"name":"perTxCap","type":"uint256"},{"name":"period","type":"uint64"},{"name":"epoch","type":"uint64"},{"name":"epochStart","type":"uint64"},{"name":"epochEnd","type":"uint64"},{"name":"spent","type":"uint256"},{"name":"autoSpent","type":"uint256"},{"name":"approvedSpent","type":"uint256"},{"name":"remaining","type":"uint256"}]}]}]`))
	if err != nil {
		return "", err
	}
	type view struct {
		Enabled       bool
		Budget        *big.Int
		PerTxCap      *big.Int
		Period        uint64
		Epoch         uint64
		EpochStart    uint64
		EpochEnd      uint64
		Spent         *big.Int
		AutoSpent     *big.Int
		ApprovedSpent *big.Int
		Remaining     *big.Int
	}
	raw, err := parsed.Methods["getCategory"].Outputs.Pack(view{
		Enabled: true, Budget: big.NewInt(50_000_000), PerTxCap: big.NewInt(20_000_000),
		Period: 1209600, Remaining: big.NewInt(50_000_000),
		Spent: big.NewInt(0), AutoSpent: big.NewInt(0), ApprovedSpent: big.NewInt(0),
	})
	if err != nil {
		return "", err
	}
	return "0x" + hex.EncodeToString(raw), nil
}

func packBool(v bool) (string, error) {
	typ, err := abi.NewType("bool", "", nil)
	if err != nil {
		return "", err
	}
	raw, err := (abi.Arguments{{Type: typ}}).Pack(v)
	if err != nil {
		return "", err
	}
	return "0x" + hex.EncodeToString(raw), nil
}

func packUintArray(ids []*big.Int) (string, error) {
	typ, err := abi.NewType("uint256[]", "", nil)
	if err != nil {
		return "", err
	}
	if ids == nil {
		ids = []*big.Int{}
	}
	raw, err := (abi.Arguments{{Type: typ}}).Pack(ids)
	if err != nil {
		return "", err
	}
	return "0x" + hex.EncodeToString(raw), nil
}

func selectorHex(sig string) string {
	return hex.EncodeToString(crypto.Keccak256([]byte(sig))[:4])
}

func word(n *big.Int) string {
	if n == nil {
		n = big.NewInt(0)
	}
	return "0x" + fmt.Sprintf("%064x", n)
}

func addressWord(addr common.Address) string {
	return "0x" + fmt.Sprintf("%064x", addr.Big())
}

type rpcLog struct {
	mu   sync.Mutex
	seen []string
}

func (l *rpcLog) add(method string) {
	if l == nil {
		return
	}
	l.mu.Lock()
	l.seen = append(l.seen, method)
	l.mu.Unlock()
}

func (l *rpcLog) methods() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.seen...)
}

func (l *rpcLog) reset() {
	l.mu.Lock()
	l.seen = nil
	l.mu.Unlock()
}

func (l *rpcLog) has(method string) bool {
	for _, m := range l.methods() {
		if m == method {
			return true
		}
	}
	return false
}

func scriptedChain(chainID string, calls map[string]string, native string, hits *rpcLog) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     json.RawMessage   `json:"id"`
			Method string            `json:"method"`
			Params []json.RawMessage `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		hits.add(req.Method)
		var result any = "0x"
		switch req.Method {
		case "eth_chainId":
			result = chainID
		case "eth_getBalance":
			result = native
		case "eth_blockNumber":
			result = "0x10"
		case "eth_getLogs":
			result = []any{}
		case "eth_call":
			var call struct {
				Data  string `json:"data"`
				Input string `json:"input"`
			}
			if len(req.Params) > 0 {
				_ = json.Unmarshal(req.Params[0], &call)
			}
			data := call.Data
			if data == "" {
				data = call.Input
			}
			data = strings.TrimPrefix(strings.ToLower(data), "0x")
			if len(data) >= 8 {
				if encoded, ok := calls[data[:8]]; ok {
					result = encoded
					break
				}
			}
			writeRPC(w, req.ID, nil, "unknown call "+data)
			return
		default:
			writeRPC(w, req.ID, nil, "unexpected "+req.Method)
			return
		}
		writeRPC(w, req.ID, result, "")
	})
}

func arcChain(vault common.Address, views map[string]string, counter *sendCounter) http.Handler {
	receipts := map[string]json.RawMessage{}
	var mu sync.Mutex
	payID := hex.EncodeToString(crypto.Keccak256([]byte("pay(bytes32,address,uint256,bytes32)"))[:4])
	paidTopic := crypto.Keccak256Hash([]byte("AgentPaid(bytes32,address,uint256,uint64,bytes32)"))
	bloom := "0x" + strings.Repeat("00", 256)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     json.RawMessage   `json:"id"`
			Method string            `json:"method"`
			Params []json.RawMessage `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		switch req.Method {
		case "eth_chainId":
			writeRPC(w, req.ID, "0x13b2", "")
		case "eth_blockNumber":
			writeRPC(w, req.ID, "0x10", "")
		case "eth_getLogs":
			writeRPC(w, req.ID, []any{}, "")
		case "eth_getBalance":
			writeRPC(w, req.ID, "0xde0b6b3a7640000", "")
		case "eth_gasPrice":
			writeRPC(w, req.ID, "0x4a817c800", "")
		case "eth_getTransactionCount":
			writeRPC(w, req.ID, "0x0", "")
		case "eth_estimateGas":
			writeRPC(w, req.ID, "0x30d40", "")
		case "eth_call":
			var call struct {
				Data  string `json:"data"`
				Input string `json:"input"`
			}
			if len(req.Params) > 0 {
				_ = json.Unmarshal(req.Params[0], &call)
			}
			data := strings.TrimPrefix(strings.ToLower(call.Data+call.Input), "0x")
			if len(data) >= 8 {
				if encoded, ok := views[data[:8]]; ok {
					writeRPC(w, req.ID, encoded, "")
					return
				}
			}
			writeRPC(w, req.ID, nil, "unknown call")
		case "eth_sendRawTransaction":
			var hexTx string
			_ = json.Unmarshal(req.Params[0], &hexTx)
			var tx types.Transaction
			if err := tx.UnmarshalBinary(common.FromHex(hexTx)); err != nil {
				writeRPC(w, req.ID, nil, err.Error())
				return
			}
			counter.mu.Lock()
			counter.nSends++
			counter.mu.Unlock()
			hash := tx.Hash()
			logs := []any{}
			rawData := hex.EncodeToString(tx.Data())
			if strings.HasPrefix(rawData, payID) && len(tx.Data()) >= 32 {
				decision := common.BytesToHash(tx.Data()[len(tx.Data())-32:])
				logs = []any{map[string]any{
					"address": vault.Hex(), "data": "0x",
					"topics":          []string{paidTopic.Hex(), common.Hash{}.Hex(), common.Hash{}.Hex(), decision.Hex()},
					"transactionHash": hash.Hex(), "blockNumber": "0x1", "logIndex": "0x0",
				}}
			}
			body, _ := json.Marshal(map[string]any{
				"type": "0x0", "status": "0x1", "cumulativeGasUsed": "0x5208", "gasUsed": "0x5208",
				"logsBloom": bloom, "logs": logs, "transactionHash": hash.Hex(),
				"blockHash": "0x" + strings.Repeat("22", 32), "blockNumber": "0x1", "transactionIndex": "0x0",
			})
			mu.Lock()
			receipts[strings.ToLower(hash.Hex())] = body
			mu.Unlock()
			writeRPC(w, req.ID, hash.Hex(), "")
		case "eth_getTransactionReceipt":
			var hexHash string
			_ = json.Unmarshal(req.Params[0], &hexHash)
			mu.Lock()
			raw, ok := receipts[strings.ToLower(hexHash)]
			mu.Unlock()
			if !ok {
				writeRPC(w, req.ID, nil, "")
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(struct {
				JSONRPC string          `json:"jsonrpc"`
				ID      json.RawMessage `json:"id"`
				Result  json.RawMessage `json:"result"`
			}{"2.0", req.ID, raw})
		default:
			writeRPC(w, req.ID, nil, "unexpected "+req.Method)
		}
	})
}

func writeRPC(w http.ResponseWriter, id json.RawMessage, result any, errMsg string) {
	w.Header().Set("Content-Type", "application/json")
	if errMsg != "" {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": -32000, "message": errMsg},
		})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
}

func agentHexSnippet(raw string) string {
	raw = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(raw)), "0x")
	if len(raw) < 12 {
		return "key-material"
	}
	return raw[:12]
}

func clearCircle(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"CIRCLE_API_KEY", "CIRCLE_ENTITY_SECRET", "CIRCLE_WALLET_ID", "CIRCLE_OWNER_WALLET_ID",
		"CIRCLE_PROCUREMENT_WALLET_ID", "CIRCLE_PROCUREMENT_BASE_WALLET_ID",
		"CIRCLE_AGENT_ADDRESS", "CIRCLE_OWNER_ADDRESS",
	} {
		t.Setenv(key, "")
	}
}
