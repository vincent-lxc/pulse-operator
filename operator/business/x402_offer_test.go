package business

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/crypto"

	"github.com/vincent-lxc/pulse-operator/operator/models"
	"github.com/vincent-lxc/pulse-operator/operator/procurement"
	"github.com/vincent-lxc/pulse-operator/operator/treasury"
)

func TestMerchantFailureKeepsOfferAndReason(t *testing.T) {
	if err := models.EnsureStorage(); err != nil {
		t.Fatal(err)
	}
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PROCUREMENT_PRIVATE_KEY", hex.EncodeToString(crypto.FromECDSA(key)))
	payer := crypto.PubkeyToAddress(key.PublicKey).Hex()
	leak := "0x" + strings.Repeat("ab", 32)
	cases := []struct {
		name     string
		accepts  string
		wantCode string
	}{
		{
			name:     "escrow",
			accepts:  `{"scheme":"auth-capture","network":"eip155:8453","amount":"8750000","asset":"` + procurement.BaseUSDC + `","payTo":"0x3333333333333333333333333333333333333333","maxTimeoutSeconds":600,"extra":{"name":"USDC","version":"2","assetTransferMethod":"eip3009","authCaptureEscrow":"0x4444444444444444444444444444444444444444","captureAuthorizer":"0x2222222222222222222222222222222222222222","leak":"` + leak + `"}}`,
			wantCode: "x402_unsupported_escrow",
		},
		{
			name:     "rejected",
			accepts:  `{"scheme":"exact","network":"eip155:8453","amount":"1","asset":"` + procurement.BaseUSDC + `","payTo":"0x3333333333333333333333333333333333333333","maxTimeoutSeconds":600,"extra":{"name":"USDC","version":"2","leak":"` + leak + `"}}`,
			wantCode: "x402_rejected",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var posts, signatures, vaults, burns int
			var keys []string
			header := base64.StdEncoding.EncodeToString([]byte(`{"x402Version":2,"accepts":[` + tc.accepts + `]}`))
			pork := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				posts++
				keys = append(keys, r.Header.Get("Idempotency-Key"))
				if r.Header.Get("PAYMENT-SIGNATURE") != "" {
					signatures++
				}
				w.Header().Set("PAYMENT-REQUIRED", header)
				w.WriteHeader(http.StatusPaymentRequired)
				_, _ = io.WriteString(w, `{"status":"ERROR","code":"PAYMENT_REQUIRED"}`)
			}))
			defer pork.Close()
			base := httptest.NewServer(scriptedChain("0x2105", map[string]string{
				selectorHex("balanceOf(address)"): word(big.NewInt(20_000_000)),
			}, "0x0", &rpcLog{}))
			defer base.Close()
			t.Setenv("BASE_RPC_URL", base.URL)
			tag := strings.ReplaceAll(t.Name(), "/", "-")
			bill := models.NewBill()
			bill.Code = "bill-x402-" + tag
			bill.Domain = tag + ".xyz"
			bill.Vendor = "porkbun"
			bill.Kind = "domain_register"
			bill.CategoryCode = "domains"
			bill.Years = 1
			bill.QuoteCents = 875
			bill.State = "bridged"
			bill.Action = treasury.ActionPay
			bill.ReasonCode = "within_policy"
			bill.DecisionHash = "0x" + strings.Repeat("11", 32)
			bill.VaultTx = "0x" + strings.Repeat("aa", 32)
			bill.BaseMintTx = "0x" + strings.Repeat("bb", 32)
			if _, err := models.InsertBill(bill); err != nil {
				t.Fatal(err)
			}
			audit, err := treasury.OpenAudit(t.TempDir() + "/audit.jsonl")
			if err != nil {
				t.Fatal(err)
			}
			deps := generousDeps(t)
			deps.audit = audit
			deps.cfg.Mode = "mainnet"
			deps.cfg.Base = treasury.BaseChainConfig{RPCEnv: "BASE_RPC_URL", USDC: procurement.BaseUSDC}
			deps.cfg.Procurement = treasury.ProcurementConfig{KeyEnv: "PROCUREMENT_PRIVATE_KEY", Address: payer}
			deps.vault = func(context.Context, *models.Bill, *big.Int, string) (vaultResult, error) {
				vaults++
				return vaultResult{}, nil
			}
			deps.burn = func(context.Context, *models.Bill, *big.Int, *big.Int) (burnResult, error) {
				burns++
				return burnResult{}, nil
			}
			client := &procurement.Porkbun{BaseURL: pork.URL, APIKey: "pk", Secret: "ps", MinInterval: -1}
			deps.merchant = func(ctx context.Context, row *models.Bill) (merchantResult, error) {
				return liveMerchant(ctx, deps.cfg, client, row)
			}
			err = AdvanceBill(context.Background(), bill, deps)
			if bill.State != "failed_merchant" || bill.ReasonCode != tc.wantCode || bill.PorkbunCheckoutID != "" || vaults != 0 || burns != 0 || signatures != 0 || posts != 1 {
				t.Fatalf("state=%s code=%s checkout=%s posts=%d sig=%d vaults=%d burns=%d err=%v", bill.State, bill.ReasonCode, bill.PorkbunCheckoutID, posts, signatures, vaults, burns, err)
			}
			if err == nil {
				t.Fatal("expected merchant error")
			}
			if strings.Contains(bill.X402Required, leak) || strings.Contains(bill.X402Required, strings.TrimPrefix(leak, "0x")) || !strings.Contains(bill.X402Required, "eip155:8453") {
				t.Fatalf("stored offer %s", bill.X402Required)
			}
			exported, markdown, err := ExportBill(bill.Code)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(exported, leak) || !strings.Contains(exported, "x402_required") || !strings.Contains(markdown, "reason_code: "+tc.wantCode) {
				t.Fatalf("export %s\n%s", exported, markdown)
			}
			raw, err := os.ReadFile(audit.Path())
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(raw), leak) || !strings.Contains(string(raw), "eip155:8453") {
				t.Fatalf("audit %s", raw)
			}
			if err = AdvanceBill(context.Background(), bill, deps); bill.ReasonCode != tc.wantCode || vaults != 0 || burns != 0 || posts != 2 || signatures != 0 {
				t.Fatalf("retry code=%s posts=%d sig=%d vaults=%d burns=%d err=%v", bill.ReasonCode, posts, signatures, vaults, burns, err)
			}
			wantKey := merchantAttemptKey(bill.Code, 1)
			if len(keys) != 2 || keys[0] != wantKey || keys[1] != wantKey {
				t.Fatalf("keys %v", keys)
			}
		})
	}
}
