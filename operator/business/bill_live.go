// 本文件把测试网和主网账单接到 Porkbun、PolicyVault 和 CCTP。dry-run 不会进这里。
package business

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"

	"github.com/vincent-lxc/pulse-operator/operator/models"
	"github.com/vincent-lxc/pulse-operator/operator/procurement"
	"github.com/vincent-lxc/pulse-operator/operator/treasury"
)

func liveDeps(ctx context.Context, cfg treasury.Config, deps billDeps) (billDeps, error) {
	apiKey, err := treasury.LoadSecret(cfg.Porkbun.APIKeyEnv, cfg.Porkbun.APIKeyFile)
	if err != nil {
		return billDeps{}, err
	}
	secret, err := treasury.LoadSecret(cfg.Porkbun.SecretEnv, cfg.Porkbun.SecretFile)
	if err != nil {
		return billDeps{}, err
	}
	client := &procurement.Porkbun{BaseURL: cfg.Porkbun.APIBase, APIKey: apiKey, Secret: secret}
	deps.quote = func(ctx context.Context, bill *models.Bill) (int64, *big.Int, error) {
		var order procurement.Order
		var err error
		if strings.Contains(bill.Kind, "renew") {
			order, err = client.Renew(ctx, bill.Domain, 0, bill.Years, true, "", "", "")
		} else {
			order, err = client.Create(ctx, bill.Domain, 0, bill.Years, true, "", "", "")
		}
		if err != nil {
			return 0, nil, err
		}
		if order.CostCents <= 0 {
			return 0, nil, fmt.Errorf("porkbun dry-run did not return a cost")
		}
		fee, err := quoteForwardFee(ctx, cfg, procurement.CentsToUSDC(order.CostCents))
		if err != nil {
			return 0, nil, err
		}
		return order.CostCents, fee, nil
	}
	deps.facts = func(bill *models.Bill, cents int64, amount *big.Int) procurement.Facts {
		facts := dryFacts(bill, cents, amount, cfg, nil)
		observed, err := observeFacts(ctx, cfg, bill, deps.payee)
		return applyObservation(facts, bill.CategoryCode, deps.payee, observed, err, deps.pendingSpend)
	}
	deps.vault = func(ctx context.Context, bill *models.Bill, amount *big.Int, hash string) (vaultResult, error) {
		chain, closeChain, err := openChain(ctx, cfg, []treasury.Payable{{
			ID: bill.Code, Category: bill.CategoryCode, Payee: deps.payee, Amount: amount, Due: time.Now().UTC(), Status: "pending",
		}}, broadcastVault())
		if err != nil {
			return vaultResult{}, err
		}
		defer closeChain()
		callHash, err := treasury.Hash32(hash)
		if err != nil {
			return vaultResult{}, err
		}
		res, err := chain.Pay(ctx, treasury.PayCall{Category: bill.CategoryCode, Payee: deps.payee, Amount: amount, DecisionHash: callHash})
		status := res.Status
		simulated := strings.HasPrefix(status, "dry_run") || strings.HasPrefix(status, "simulated")
		if !simulated && (strings.Contains(status, "approval") || (res.RequestID != "" && !strings.Contains(status, "paid"))) {
			status = "approval"
		}
		return vaultResult{TxHash: res.TxHash, RequestID: res.RequestID, Status: status}, err
	}
	deps.burn = func(ctx context.Context, bill *models.Bill, cost, maxFee *big.Int) (burnResult, error) {
		return liveBurn(ctx, cfg, bill, cost, maxFee, func(tx string) error {
			bill.CCTPBurnTx = tx
			if deps.save == nil {
				return nil
			}
			return deps.save(bill)
		})
	}
	deps.merchant = func(ctx context.Context, bill *models.Bill) (merchantResult, error) {
		return liveMerchant(ctx, cfg, client, bill)
	}
	return deps, nil
}

func quoteForwardFee(ctx context.Context, cfg treasury.Config, needed *big.Int) (*big.Int, error) {
	raw, err := httpGet(ctx, procurement.FeeURL(cfg.IrisAPI(), cfg.CCTP.SourceDomain, cfg.CCTP.DestDomain, cfg.ForwardCCTP()))
	if err != nil {
		return nil, err
	}
	quotes, err := procurement.ParseFeeQuotes(raw)
	if err != nil {
		return nil, err
	}
	q, err := procurement.SelectQuote(quotes, 1000)
	if err != nil {
		return nil, err
	}
	maxFee, _, err := procurement.Amounts(needed, q, cfg.CCTP.FeeLevel)
	return maxFee, err
}

func observeFacts(ctx context.Context, cfg treasury.Config, bill *models.Bill, payee string) (treasury.Snapshot, error) {
	if cfg.Mode == "" || cfg.Mode == "dry-run" {
		return treasury.Snapshot{}, fmt.Errorf("dry-run does not read the chain")
	}
	chain, closeChain, err := openChain(ctx, cfg, []treasury.Payable{{
		ID: bill.Code, Category: bill.CategoryCode, Payee: payee, Due: time.Now().UTC(), Status: "pending",
	}})
	if err != nil {
		return treasury.Snapshot{}, err
	}
	defer closeChain()
	return chain.Observe(ctx)
}

// applyObservation 用链上快照覆盖报价事实。读失败时清空余额和上限，交给硬规则升级且不付款。
// pendingSpend 只含本轮已决定付款、但链上余额还没扣掉的金额。已经到账的支出留在观察值里，不再减一次。
func applyObservation(facts procurement.Facts, category, payee string, observed treasury.Snapshot, observeErr error, pendingSpend *big.Int) procurement.Facts {
	if observeErr != nil {
		facts.ObserveFailed = true
		facts.CategoryEnabled = false
		facts.PayeeAllowed = false
		facts.Balance = nil
		facts.Remaining = nil
		facts.PerTxCap = nil
		return facts
	}
	facts.ObserveFailed = false
	facts.Balance = subFloor(observed.Balance, pendingSpend)
	cat, exists := observed.Categories[category]
	if !exists {
		facts.CategoryEnabled = false
		facts.PayeeAllowed = false
		facts.Remaining = nil
		facts.PerTxCap = nil
		return facts
	}
	facts.CategoryEnabled = cat.Enabled
	facts.Remaining = subFloor(cat.Remaining, pendingSpend)
	facts.PerTxCap = cat.PerTxCap
	if len(cat.Payees) > 0 {
		facts.PayeeAllowed = cat.Payees[treasury.NormalizeAddress(payee)]
	}
	return facts
}

var pollBurnMessages = pollIris

func liveBurn(ctx context.Context, cfg treasury.Config, bill *models.Bill, cost, maxFee *big.Int, save func(string) error) (burnResult, error) {
	if bill.CCTPBurnTx != "" {
		wait, cancel := context.WithTimeout(ctx, burnWait(cfg))
		defer cancel()
		msg, err := pollBurnMessages(wait, cfg, bill.CCTPBurnTx)
		if err != nil {
			if cfg.ForwardCCTP() && awaitingForward(wait, err) {
				return burnResult{BurnTx: bill.CCTPBurnTx, ForwardFee: maxFee.String()}, fmt.Errorf("%w", errAwaitingMint)
			}
			return burnResult{BurnTx: bill.CCTPBurnTx, ForwardFee: maxFee.String()}, err
		}
		if cfg.ForwardCCTP() && strings.TrimSpace(msg.ForwardTxHash) == "" {
			return burnResult{BurnTx: bill.CCTPBurnTx, MessageHash: msg.Message, Nonce: msg.EventNonce, ForwardFee: maxFee.String()}, fmt.Errorf("%w", errAwaitingMint)
		}
		return burnResult{BurnTx: bill.CCTPBurnTx, MintTx: msg.ForwardTxHash, MessageHash: msg.Message, Nonce: msg.EventNonce, ForwardFee: maxFee.String()}, nil
	}
	feeNow, err := quoteForwardFee(ctx, cfg, cost)
	if err != nil {
		return burnResult{}, err
	}
	if feeNow.Cmp(maxFee) > 0 {
		return burnResult{}, fmt.Errorf("forward fee increased above the quoted allowance")
	}
	recipient := common.HexToAddress(cfg.Procurement.Address)
	if cfg.Procurement.BaseAddress != "" {
		recipient = common.HexToAddress(cfg.Procurement.BaseAddress)
	}
	token := common.HexToAddress(cfg.USDC)
	messenger := common.HexToAddress(cfg.CCTP.TokenMessenger)
	if messenger == (common.Address{}) {
		if cfg.Mode == "mainnet" {
			messenger = common.HexToAddress(procurement.MainnetTokenMessenger)
		} else {
			messenger = common.HexToAddress(procurement.TestnetTokenMessenger)
		}
	}
	burnTotal := new(big.Int).Add(cost, feeNow)
	req := procurement.BurnRequest{
		Amount: burnTotal, DestinationDomain: cfg.CCTP.DestDomain, MintRecipient: recipient,
		BurnToken: token, MaxFee: feeNow, MinFinalityThreshold: 1000, HookData: procurement.HookBytes(),
	}
	var data []byte
	if cfg.ForwardCCTP() {
		data, err = procurement.EncodeDepositForBurnWithHook(req)
	} else {
		data, err = procurement.EncodeDepositForBurn(req)
	}
	if err != nil {
		return burnResult{}, err
	}
	approve, err := procurement.EncodeApprove(messenger, burnTotal)
	if err != nil {
		return burnResult{}, err
	}
	_, circleID, err := sendProcurement(ctx, cfg, token, approve, "approve(address,uint256)", []string{messenger.Hex(), burnTotal.String()}, bill.DecisionHash+":approve")
	if err != nil {
		return burnResult{CircleTxID: circleID}, err
	}
	hook := "0x" + common.Bytes2Hex(req.HookData)
	burnSig := "depositForBurn(uint256,uint32,bytes32,address,bytes32,uint256,uint32)"
	params := []string{
		burnTotal.String(), fmt.Sprintf("%d", cfg.CCTP.DestDomain), procurement.AddressToBytes32(recipient).Hex(),
		token.Hex(), common.Hash{}.Hex(), feeNow.String(), "1000",
	}
	if cfg.ForwardCCTP() {
		burnSig = "depositForBurnWithHook(uint256,uint32,bytes32,address,bytes32,uint256,uint32,bytes)"
		params = append(params, hook)
	}
	burnTx, circleID, err := sendProcurement(ctx, cfg, messenger, data, burnSig, params, bill.DecisionHash+":burn")
	if err != nil {
		return burnResult{CircleTxID: circleID}, err
	}
	return commitBurn(ctx, cfg, burnTx, circleID, feeNow, save)
}

// commitBurn 先落库 burn 交易，再等 Iris。重试看到 CCTPBurnTx 时只轮询。
func commitBurn(ctx context.Context, cfg treasury.Config, burnTx, circleID string, fee *big.Int, save func(string) error) (burnResult, error) {
	if save != nil {
		if err := save(burnTx); err != nil {
			return burnResult{BurnTx: burnTx, ForwardFee: fee.String(), CircleTxID: circleID}, err
		}
	}
	wait, cancel := context.WithTimeout(ctx, burnWait(cfg))
	defer cancel()
	msg, err := pollBurnMessages(wait, cfg, burnTx)
	if err != nil {
		if cfg.ForwardCCTP() && errors.Is(wait.Err(), context.DeadlineExceeded) {
			return burnResult{BurnTx: burnTx, ForwardFee: fee.String(), CircleTxID: circleID}, fmt.Errorf("%w", errAwaitingMint)
		}
		return burnResult{BurnTx: burnTx, ForwardFee: fee.String(), CircleTxID: circleID}, err
	}
	mint := msg.ForwardTxHash
	if cfg.ForwardCCTP() && strings.TrimSpace(mint) == "" {
		return burnResult{BurnTx: burnTx, MessageHash: msg.Message, Nonce: msg.EventNonce, ForwardFee: fee.String(), CircleTxID: circleID}, fmt.Errorf("%w", errAwaitingMint)
	}
	if mint == "" && !cfg.ForwardCCTP() {
		mint, err = receiveOnBase(ctx, cfg, msg)
		if err != nil {
			return burnResult{BurnTx: burnTx, MessageHash: msg.Message, Nonce: msg.EventNonce, ForwardFee: fee.String(), CircleTxID: circleID}, err
		}
	}
	return burnResult{BurnTx: burnTx, MintTx: mint, MessageHash: msg.Message, Nonce: msg.EventNonce, ForwardFee: fee.String(), CircleTxID: circleID}, nil
}

// errAwaitingMint 表示 burn 已经记下，Base 上的 USDC 还不能付。重试只轮询，不再 burn。
var errAwaitingMint = errors.New("awaiting_mint")

func burnWait(cfg treasury.Config) time.Duration {
	if cfg.CCTP.PollTimeout > 0 {
		return cfg.CCTP.PollTimeout
	}
	return 3 * time.Minute
}

func awaitingForward(wait context.Context, err error) bool {
	if errors.Is(wait.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	return err != nil && strings.Contains(err.Error(), "forwarded mint is not ready")
}

func liveMerchant(ctx context.Context, cfg treasury.Config, client *procurement.Porkbun, bill *models.Bill) (merchantResult, error) {
	network := "eip155:84532"
	asset := procurement.BaseSepoliaUSDC
	if cfg.Mode == "mainnet" {
		network = "eip155:8453"
		asset = procurement.BaseUSDC
	}
	// 余额只在第一次签名前检查。已有 checkout 时 USDC 可能已经付给了 Porkbun，重试只轮询。
	var sign func(procurement.Accept, time.Time) (procurement.Payment, error)
	if strings.TrimSpace(bill.PorkbunCheckoutID) == "" {
		if err := requirePayerBalance(ctx, cfg, procurement.CentsToUSDC(bill.QuoteCents)); err != nil {
			return merchantResult{}, err
		}
		var err error
		sign, err = merchantSigner(cfg)
		if err != nil {
			return merchantResult{}, err
		}
	}
	order, err := procurement.Collect(ctx, client, procurement.CollectInput{
		Domain: bill.Domain, Kind: bill.Kind, CostCents: bill.QuoteCents, Years: bill.Years,
		CheckoutID: bill.PorkbunCheckoutID, Idempotency: "bill-" + bill.Code,
		Network: network, Asset: asset, Now: time.Now().UTC(), Sign: sign,
	})
	if err != nil {
		return merchantResult{CheckoutID: order.CheckoutID}, err
	}
	return merchantResult{
		OrderID: order.OrderID, CheckoutID: order.CheckoutID, KeptAsCredit: order.KeptAsCredit,
		BalanceCents: order.BalanceCents, Pending: order.Code == "PAYMENT_PENDING" || order.Code == "PAYMENT_IN_PROGRESS",
	}, nil
}

func requirePayerBalance(ctx context.Context, cfg treasury.Config, cost *big.Int) error {
	if cost == nil || cost.Sign() <= 0 {
		return fmt.Errorf("bill cost is missing")
	}
	env := cfg.Base.RPCEnv
	if env == "" {
		env = "BASE_RPC_URL"
	}
	rpc := os.Getenv(env)
	if rpc == "" {
		return fmt.Errorf("base rpc missing: set %s", env)
	}
	token := cfg.Base.USDC
	if !common.IsHexAddress(token) {
		if cfg.Mode == "mainnet" {
			token = procurement.BaseUSDC
		} else {
			token = procurement.BaseSepoliaUSDC
		}
	}
	payer := configuredPayer(cfg)
	if payer == (common.Address{}) {
		return fmt.Errorf("base payer address is missing")
	}
	bal, err := procurement.TokenBalance(ctx, rpc, token, payer.Hex())
	if err != nil {
		return fmt.Errorf("%w: Base USDC balance could not be read", errAwaitingMint)
	}
	if bal.Cmp(cost) < 0 {
		return fmt.Errorf("%w: Base USDC balance is below the bill cost", errAwaitingMint)
	}
	return nil
}

func configuredPayer(cfg treasury.Config) common.Address {
	raw := strings.TrimSpace(cfg.Procurement.BaseAddress)
	if raw == "" {
		raw = cfg.Procurement.Address
	}
	if !common.IsHexAddress(raw) {
		return common.Address{}
	}
	return common.HexToAddress(raw)
}

func merchantSigner(cfg treasury.Config) (func(procurement.Accept, time.Time) (procurement.Payment, error), error) {
	key, err := treasury.LoadPrivateKey(cfg.Procurement.KeyEnv, cfg.Procurement.KeyFile)
	if err != nil {
		return nil, err
	}
	if key == nil && cfg.Mode == "mainnet" {
		return nil, missingKey("procurement", cfg.Procurement.KeyEnv, cfg.Procurement.KeyFile)
	}
	if key != nil {
		payer := crypto.PubkeyToAddress(key.PublicKey)
		want := configuredPayer(cfg)
		if want == (common.Address{}) || payer != want {
			return nil, fmt.Errorf("procurement signer %s does not match configured payer %s", payer.Hex(), want.Hex())
		}
		return func(item procurement.Accept, now time.Time) (procurement.Payment, error) {
			return procurement.SignPayment(key, item, now)
		}, nil
	}
	if cfg.Mode == "mainnet" {
		return nil, missingKey("procurement", cfg.Procurement.KeyEnv, cfg.Procurement.KeyFile)
	}
	wallets, err := walletsClient(cfg)
	if err != nil {
		return nil, err
	}
	walletID, err := baseWalletID(cfg)
	if err != nil || walletID == "" {
		return nil, fmt.Errorf("procurement signer missing: set %s or %s", cfg.Procurement.KeyEnv, cfg.Procurement.BaseWalletIDEnv)
	}
	return func(item procurement.Accept, now time.Time) (procurement.Payment, error) {
		payer := common.HexToAddress(cfg.Procurement.BaseAddress)
		if payer == (common.Address{}) {
			payer = common.HexToAddress(cfg.Procurement.Address)
		}
		typed, auth, extra, err := procurement.PrepareTypedData(payer, item, now)
		if err != nil {
			return procurement.Payment{}, err
		}
		sig, err := wallets.SignTypedData(context.Background(), walletID, payer.Hex(), treasury.BlockchainForBase(cfg.Base.ChainID, cfg.Mode), typed)
		if err != nil {
			return procurement.Payment{}, err
		}
		return procurement.FinishPayment(item, payer, auth, sig, extra)
	}, nil
}

func baseWalletID(cfg treasury.Config) (string, error) {
	id, err := treasury.LoadSecret(cfg.Procurement.BaseWalletIDEnv, cfg.Procurement.BaseWalletIDFile)
	if err != nil || id != "" {
		return id, err
	}
	return treasury.LoadSecret(cfg.Procurement.WalletIDEnv, cfg.Procurement.WalletIDFile)
}

func sendProcurement(ctx context.Context, cfg treasury.Config, to common.Address, data []byte, abiSig string, params []string, idem string) (string, string, error) {
	key, err := treasury.LoadPrivateKey(cfg.Procurement.KeyEnv, cfg.Procurement.KeyFile)
	if err != nil {
		return "", "", err
	}
	if key == nil && cfg.Mode == "mainnet" {
		return "", "", missingKey("procurement", cfg.Procurement.KeyEnv, cfg.Procurement.KeyFile)
	}
	if key != nil {
		chainID, ok := new(big.Int).SetString(cfg.ChainID, 10)
		if !ok {
			return "", "", fmt.Errorf("invalid chain id")
		}
		tx, err := procurement.SendCall(ctx, os.Getenv(cfg.Secrets.RPCEnv), chainID, key, to, data)
		return tx, "", err
	}
	if cfg.Mode == "mainnet" {
		return "", "", missingKey("procurement", cfg.Procurement.KeyEnv, cfg.Procurement.KeyFile)
	}
	wallets, err := walletsClient(cfg)
	if err != nil {
		return "", "", err
	}
	walletID, err := treasury.LoadSecret(cfg.Procurement.WalletIDEnv, cfg.Procurement.WalletIDFile)
	if err != nil || walletID == "" {
		return "", "", fmt.Errorf("procurement wallet missing")
	}
	sum := crypto.Keccak256Hash([]byte(idem))
	tx, err := wallets.Execute(ctx, treasury.ContractExecution{
		IdempotencyKey: treasury.IdempotencyFromHash(sum),
		WalletID:       walletID,
		Blockchain:     cfg.Circle.Blockchain,
		Contract:       to.Hex(),
		Signature:      abiSig,
		Params:         params,
		Fee:            circleFee(cfg),
	})
	if err != nil {
		return tx.TxHash, tx.ID, err
	}
	return tx.TxHash, tx.ID, nil
}

func irisReady(cfg treasury.Config, msg procurement.Message) bool {
	if strings.TrimSpace(msg.ForwardTxHash) != "" {
		return true
	}
	return !cfg.ForwardCCTP() && strings.EqualFold(msg.Status, "complete")
}

func pollIris(ctx context.Context, cfg treasury.Config, burnTx string) (procurement.Message, error) {
	url := procurement.MessagesURL(cfg.IrisAPI(), cfg.CCTP.SourceDomain, burnTx)
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	var last error
	for {
		raw, err := httpGet(ctx, url)
		if err == nil {
			msg, err := procurement.ParseIrisMessages(raw)
			if err == nil && irisReady(cfg, msg) {
				return msg, nil
			}
			if err == nil && cfg.ForwardCCTP() {
				last = fmt.Errorf("forwarded mint is not ready")
			} else {
				last = err
			}
		} else {
			last = err
		}
		select {
		case <-ctx.Done():
			if last != nil {
				return procurement.Message{}, last
			}
			return procurement.Message{}, ctx.Err()
		case <-ticker.C:
		}
	}
}

func receiveOnBase(ctx context.Context, cfg treasury.Config, msg procurement.Message) (string, error) {
	if !cfg.CCTP.AllowStandard {
		return "", fmt.Errorf("standard CCTP receive is disabled")
	}
	message := common.FromHex(msg.Message)
	attestation := common.FromHex(msg.Attestation)
	data, err := procurement.EncodeReceiveMessage(message, attestation)
	if err != nil {
		return "", err
	}
	key, err := treasury.LoadPrivateKey(cfg.Procurement.KeyEnv, cfg.Procurement.KeyFile)
	if err != nil || key == nil {
		return "", fmt.Errorf("standard receive needs %s and Base ETH", cfg.Procurement.KeyEnv)
	}
	transmitter := cfg.CCTP.BaseTransmitter
	if transmitter == "" {
		if cfg.Mode == "mainnet" {
			transmitter = procurement.BaseMessageTransmitter
		} else {
			transmitter = procurement.BaseSepoliaMessageTransmitter
		}
	}
	chainID := big.NewInt(procurement.BaseSepoliaChainID)
	if cfg.Mode == "mainnet" {
		chainID = big.NewInt(procurement.BaseChainID)
	}
	return procurement.SendCall(ctx, os.Getenv(cfg.Base.RPCEnv), chainID, key, common.HexToAddress(transmitter), data)
}

func httpGet(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, fmt.Errorf("GET %s: %s", url, procurement.Redact(string(body)))
	}
	return body, nil
}
