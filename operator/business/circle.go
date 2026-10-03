// 本文件把 CCTP 确认和 Gateway 通知写成未对账收入。下一轮循环按交易哈希去重后计入余额。
package business

import (
	"context"
	"time"

	"github.com/vincent-lxc/pulse-operator/operator/models"
	"github.com/vincent-lxc/pulse-operator/operator/treasury"
)

func confirmCCTP(ctx context.Context, cfg treasury.Config) ([]treasury.Inflow, []string, error) {
	if cfg.Mode != "live" || !cfg.Circle.CCTP.Enabled || len(cfg.Circle.CCTP.Burns) == 0 {
		return nil, nil, nil
	}
	client := treasury.CCTPClient{BaseURL: cfg.Circle.IrisBase}
	var inflows []treasury.Inflow
	var codes []string
	for _, burn := range cfg.Circle.CCTP.Burns {
		in, err := client.FetchCCTP(ctx, burn.SourceDomain, burn.TxHash, cfg.Vault)
		if err != nil {
			return nil, nil, err
		}
		row, err := rememberInflow(in, "cctp v2 iris")
		if err != nil {
			return nil, nil, err
		}
		if row.Reconciled {
			continue
		}
		inflows = append(inflows, in)
		codes = append(codes, row.Code)
	}
	return inflows, codes, nil
}

// IngestCCTP 按源域和 burn 交易向 Iris 确认一笔铸到金库的 USDC。
func IngestCCTP(ctx context.Context, cfg treasury.Config, sourceDomain uint32, txHash string) (treasury.Inflow, error) {
	client := treasury.CCTPClient{BaseURL: cfg.Circle.IrisBase}
	in, err := client.FetchCCTP(ctx, sourceDomain, txHash, cfg.Vault)
	if err != nil {
		return treasury.Inflow{}, err
	}
	if _, err := rememberInflow(in, "cctp v2 iris"); err != nil {
		return treasury.Inflow{}, err
	}
	return in, nil
}

// IngestGateway 解析 Gateway 通知。live 模式要求 ECDSA 签名；dry-run 允许无签名的本地演示。
func IngestGateway(ctx context.Context, cfg treasury.Config, raw []byte, signature, keyID string) (treasury.Inflow, error) {
	if cfg.Mode == "live" {
		if signature == "" || keyID == "" {
			return treasury.Inflow{}, models.NewBusinessError("live gateway webhooks require X-Circle-Signature and X-Circle-Key-Id")
		}
		apiKey, err := treasury.LoadSecret(cfg.Circle.APIKeyEnv, cfg.Circle.APIKeyFile)
		if err != nil {
			return treasury.Inflow{}, err
		}
		pub, err := treasury.FetchNotificationKey(ctx, cfg.Circle.APIBase, apiKey, keyID, nil)
		if err != nil {
			return treasury.Inflow{}, err
		}
		if err := treasury.VerifyCircleSignature(string(raw), signature, pub); err != nil {
			return treasury.Inflow{}, models.NewBusinessError(err.Error())
		}
	}
	in, err := treasury.ParseGateway(raw, cfg.Vault, cfg.USDC)
	if err != nil {
		return treasury.Inflow{}, models.NewValidationError(err.Error())
	}
	if _, err := rememberInflow(in, "gateway webhook"); err != nil {
		return treasury.Inflow{}, err
	}
	return in, nil
}

func rememberInflow(in treasury.Inflow, memo string) (*models.Revenue, error) {
	return models.InsertRevenue(in.Source, in.TxHash, in.From, treasury.FormatUSDC(in.Amount), time.Now().UTC().Format(time.RFC3339), memo, in.Product)
}
