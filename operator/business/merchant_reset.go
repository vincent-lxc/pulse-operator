package business

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"

	"github.com/vincent-lxc/pulse-operator/operator/models"
	"github.com/vincent-lxc/pulse-operator/operator/procurement"
	"github.com/vincent-lxc/pulse-operator/operator/treasury"
)

// MerchantReset 在条件都成立时，给已经签过的尝试再开一次新的尝试。
// expectedBalance 只用于签名时没有记下 Porkbun 余额的旧尝试。已经记下余额时拒绝这个参数。
// 它不签名，也不碰金库和 burn。命令只在 CLI 里，管理接口没有这一条。
func MerchantReset(ctx context.Context, cfg treasury.Config, id string, yes bool, expectedBalance *int64) (*models.Bill, error) {
	return merchantResetAt(ctx, cfg, id, yes, expectedBalance, time.Now())
}

func merchantResetAt(ctx context.Context, cfg treasury.Config, id string, yes bool, expectedBalance *int64, now time.Time) (*models.Bill, error) {
	if !yes {
		return nil, fmt.Errorf("bill merchant-reset requires --yes")
	}
	if err := models.EnsureStorage(); err != nil {
		return nil, err
	}
	bill, err := models.FindBill(strings.TrimSpace(id))
	if err != nil {
		return nil, err
	}
	if bill == nil {
		return nil, fmt.Errorf("bill %s was not found", strings.TrimSpace(id))
	}
	if terminal(bill.State) {
		return nil, fmt.Errorf("bill %s is already %s", bill.Code, bill.State)
	}
	attempts, err := parseMerchantAttempts(bill.MerchantAttempts)
	if err != nil {
		return nil, err
	}
	if len(attempts) == 0 || !attempts[len(attempts)-1].Signed {
		return nil, fmt.Errorf("current attempt is not signed")
	}
	last := attempts[len(attempts)-1]
	if last.ValidBefore <= 0 || now.Unix() <= last.ValidBefore {
		return nil, fmt.Errorf("authorization validBefore has not passed")
	}
	payer, err := resetPayer(cfg, last)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(last.Nonce) == "" {
		return nil, fmt.Errorf("authorization nonce is missing")
	}
	used, err := procurement.AuthorizationState(ctx, baseRPC(cfg), resetUSDC(cfg), payer, last.Nonce)
	if err != nil {
		return nil, fmt.Errorf("authorizationState: %w", err)
	}
	if used {
		return nil, fmt.Errorf("USDC authorization was already used")
	}
	compare := last.BalanceCents
	fromFlag := false
	if expectedBalance != nil {
		if last.BalanceKnown {
			return nil, fmt.Errorf("--expected-balance-cents is only allowed when the attempt has no recorded balance")
		}
		if *expectedBalance < 0 {
			return nil, fmt.Errorf("--expected-balance-cents must be a non-negative integer")
		}
		compare = *expectedBalance
		fromFlag = true
	} else if !last.BalanceKnown {
		return nil, fmt.Errorf("account balance was not recorded at signing")
	}
	client, err := porkbunClient(cfg)
	if err != nil {
		return nil, err
	}
	balance, err := client.AccountBalance(ctx)
	if err != nil {
		return nil, fmt.Errorf("account balance: %w", err)
	}
	if balance != compare {
		if fromFlag {
			return nil, fmt.Errorf("account balance does not match --expected-balance-cents")
		}
		return nil, fmt.Errorf("account balance changed since signing")
	}
	owned, err := client.AccountHasDomain(ctx, bill.Domain)
	if err != nil {
		return nil, fmt.Errorf("domain list: %w", err)
	}
	if owned {
		return nil, fmt.Errorf("domain is already in the Porkbun account")
	}
	vault, burn, mint := bill.VaultTx, bill.CCTPBurnTx, bill.BaseMintTx
	fromN := last.N
	nextN := last.N + 1
	last.Outcome = "reset"
	if fromFlag {
		n := compare
		last.ExpectedBalanceCents = &n
		last.BalanceFromFlag = true
		last.BalanceCents = n
		last.BalanceKnown = true
		last.Reason = publicText("merchant-reset: authorization unused, operator-supplied balance matched, domain not in the account")
	} else {
		last.Reason = publicText("merchant-reset: authorization unused, balance unchanged, domain not in the account")
	}
	attempts[len(attempts)-1] = last
	attempts = append(attempts, merchantAttempt{
		N: nextN, Key: merchantAttemptKey(bill.Code, nextN),
		At: now.UTC().Format(time.RFC3339), Outcome: "started",
		Reason: "opened by merchant-reset",
	})
	bill.PorkbunCheckoutID = ""
	if err := writeMerchantAttempts(bill, attempts); err != nil {
		return nil, err
	}
	if bill.VaultTx != vault || bill.CCTPBurnTx != burn || bill.BaseMintTx != mint {
		return nil, fmt.Errorf("merchant-reset must not change vault or burn")
	}
	if err := models.SaveBill(bill); err != nil {
		return nil, err
	}
	if err := auditMerchantReset(cfg, bill, fromN, nextN, last.Key, merchantAttemptKey(bill.Code, nextN), compare, fromFlag); err != nil {
		return bill, err
	}
	return bill, nil
}

func resetPayer(cfg treasury.Config, last merchantAttempt) (string, error) {
	payer := strings.TrimSpace(last.Payer)
	configured := configuredPayer(cfg)
	if payer == "" {
		if configured == (common.Address{}) {
			return "", fmt.Errorf("authorization payer is missing")
		}
		return configured.Hex(), nil
	}
	if !common.IsHexAddress(payer) || common.HexToAddress(payer) == (common.Address{}) {
		return "", fmt.Errorf("authorization payer is missing")
	}
	if configured != (common.Address{}) && configured != common.HexToAddress(payer) {
		return "", fmt.Errorf("authorization payer does not match the configured payer")
	}
	return common.HexToAddress(payer).Hex(), nil
}

func baseRPC(cfg treasury.Config) string {
	env := cfg.Base.RPCEnv
	if env == "" {
		env = "BASE_RPC_URL"
	}
	return os.Getenv(env)
}

func resetUSDC(cfg treasury.Config) string {
	if common.IsHexAddress(cfg.Base.USDC) {
		return cfg.Base.USDC
	}
	if cfg.Mode == "mainnet" {
		return procurement.BaseUSDC
	}
	return procurement.BaseSepoliaUSDC
}

func auditMerchantReset(cfg treasury.Config, bill *models.Bill, fromN, toN int, fromKey, toKey string, balance int64, fromFlag bool) error {
	if strings.TrimSpace(cfg.AuditLog) == "" || bill == nil {
		return nil
	}
	log, err := treasury.OpenAudit(cfg.AuditLog)
	if err != nil {
		return err
	}
	payload := map[string]any{
		"action": "merchant_reset", "bill": bill.Code, "domain": bill.Domain,
		"from_attempt": fromN, "to_attempt": toN, "from_key": fromKey, "to_key": toKey,
		"balance_cents": balance, "authorization_unused": true,
	}
	if fromFlag {
		payload["expected_balance_cents"] = balance
		payload["balance_from_flag"] = true
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return log.Append(treasury.AuditEvent{
		RunID: bill.Code, Kind: "merchant_reset", DecisionHash: bill.DecisionHash,
		Outcome: bill.State, Payload: raw,
	})
}
