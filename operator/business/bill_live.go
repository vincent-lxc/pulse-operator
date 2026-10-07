// 本文件把测试网和主网账单接到 Porkbun、PolicyVault 和 CCTP。dry-run 不会进这里。
package business

import (
	"context"
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
		facts := dryFacts(bill, cents, amount, cfg)
		facts.CategoryEnabled = true
		facts.PayeeAllowed = true
		return facts
	}
	deps.vault = func(ctx context.Context, bill *models.Bill, amount *big.Int, hash string) (vaultResult, error) {
		chain, closeChain, err := openChain(ctx, cfg, []treasury.Payable{{
			ID: bill.Code, Category: bill.CategoryCode, Payee: deps.payee, Amount: amount, Due: time.Now().UTC(), Status: "pending",
		}})
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
		if strings.Contains(status, "approval") || (res.RequestID != "" && !strings.Contains(status, "paid")) {
			status = "approval"
		}
		return vaultResult{TxHash: res.TxHash, RequestID: res.RequestID, Status: status}, err
	}
	deps.burn = func(ctx context.Context, bill *models.Bill, cost, maxFee *big.Int) (burnResult, error) {
		return liveBurn(ctx, cfg, bill, cost, maxFee)
	}
	deps.merchant = func(ctx context.Context, bill *models.Bill) (merchantResult, error) {
		return liveMerchant(ctx, cfg, client, bill)
	}
	return deps, nil
}

func quoteForwardFee(ctx context.Context, cfg treasury.Config, needed *big.Int) (*big.Int, error) {
	iris := cfg.Circle.IrisBase
	if iris == "" {
		iris = "https://iris-api.circle.com"
	}
	raw, err := httpGet(ctx, procurement.FeeURL(iris, cfg.CCTP.SourceDomain, cfg.CCTP.DestDomain, cfg.ForwardCCTP()))
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

func liveBurn(ctx context.Context, cfg treasury.Config, bill *models.Bill, cost, maxFee *big.Int) (burnResult, error) {
	if bill.CCTPBurnTx != "" {
		msg, err := pollIris(ctx, cfg, bill.CCTPBurnTx)
		if err != nil {
			return burnResult{BurnTx: bill.CCTPBurnTx}, err
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
	wait, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	msg, err := pollIris(wait, cfg, burnTx)
	if err != nil {
		return burnResult{BurnTx: burnTx, ForwardFee: feeNow.String(), CircleTxID: circleID}, err
	}
	mint := msg.ForwardTxHash
	if mint == "" && !cfg.ForwardCCTP() {
		mint, err = receiveOnBase(ctx, cfg, msg)
		if err != nil {
			return burnResult{BurnTx: burnTx, MessageHash: msg.Message, Nonce: msg.EventNonce, ForwardFee: feeNow.String(), CircleTxID: circleID}, err
		}
	}
	return burnResult{BurnTx: burnTx, MintTx: mint, MessageHash: msg.Message, Nonce: msg.EventNonce, ForwardFee: feeNow.String(), CircleTxID: circleID}, nil
}

func liveMerchant(ctx context.Context, cfg treasury.Config, client *procurement.Porkbun, bill *models.Bill) (merchantResult, error) {
	network := "eip155:84532"
	asset := procurement.BaseSepoliaUSDC
	if cfg.Mode == "mainnet" {
		network = "eip155:8453"
		asset = procurement.BaseUSDC
	}
	sign, err := merchantSigner(cfg)
	if err != nil {
		return merchantResult{}, err
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

func merchantSigner(cfg treasury.Config) (func(procurement.Accept, time.Time) (procurement.Payment, error), error) {
	key, err := treasury.LoadPrivateKey(cfg.Procurement.KeyEnv, cfg.Procurement.KeyFile)
	if err != nil {
		return nil, err
	}
	if key != nil {
		return func(item procurement.Accept, now time.Time) (procurement.Payment, error) {
			return procurement.SignPayment(key, item, now)
		}, nil
	}
	wallets, err := walletsClient(cfg)
	if err != nil {
		return nil, err
	}
	walletID, err := treasury.LoadSecret(cfg.Procurement.WalletIDEnv, cfg.Procurement.WalletIDFile)
	if err != nil || walletID == "" {
		return nil, fmt.Errorf("procurement signer missing: set %s or %s", cfg.Procurement.KeyEnv, cfg.Procurement.WalletIDEnv)
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
		sig, err := wallets.SignTypedData(context.Background(), walletID, "", cfg.Circle.Blockchain, typed)
		if err != nil {
			return procurement.Payment{}, err
		}
		return procurement.FinishPayment(item, payer, auth, sig, extra), nil
	}, nil
}

func sendProcurement(ctx context.Context, cfg treasury.Config, to common.Address, data []byte, abiSig string, params []string, idem string) (string, string, error) {
	key, err := treasury.LoadPrivateKey(cfg.Procurement.KeyEnv, cfg.Procurement.KeyFile)
	if err != nil {
		return "", "", err
	}
	if key != nil {
		chainID, ok := new(big.Int).SetString(cfg.ChainID, 10)
		if !ok {
			return "", "", fmt.Errorf("invalid chain id")
		}
		tx, err := procurement.SendCall(ctx, os.Getenv(cfg.Secrets.RPCEnv), chainID, key, to, data)
		return tx, "", err
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

func pollIris(ctx context.Context, cfg treasury.Config, burnTx string) (procurement.Message, error) {
	iris := cfg.Circle.IrisBase
	if iris == "" {
		iris = "https://iris-api.circle.com"
	}
	url := procurement.MessagesURL(iris, cfg.CCTP.SourceDomain, burnTx)
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	var last error
	for {
		raw, err := httpGet(ctx, url)
		if err == nil {
			msg, err := procurement.ParseIrisMessages(raw)
			if err == nil && (msg.ForwardTxHash != "" || strings.EqualFold(msg.Status, "complete")) {
				return msg, nil
			}
			last = err
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
