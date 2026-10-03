// 本文件用 Circle CCTP v2 的 Iris API 确认一笔要铸到 Arc 的 USDC。
// Arc 的 CCTP domain 是 26。Iris 按源域和 burn 交易哈希查询，不会按收款地址扫全网。
package treasury

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"

	"github.com/ethereum/go-ethereum/common"
)

const ArcCCTPDomain = "26"

// CCTPClient 查询 Iris。测试替换 BaseURL 和 HTTP。
type CCTPClient struct {
	BaseURL string
	HTTP    *http.Client
}

// FetchCCTP 读取一笔 burn 的 attestation。状态不是 complete 时返回错误。
func (c CCTPClient) FetchCCTP(ctx context.Context, sourceDomain uint32, txHash, vault string) (Inflow, error) {
	if strings.TrimSpace(txHash) == "" {
		return Inflow{}, fmt.Errorf("cctp burn transaction hash is required")
	}
	base := strings.TrimRight(c.BaseURL, "/")
	if base == "" {
		base = "https://iris-api-sandbox.circle.com"
	}
	url := fmt.Sprintf("%s/v2/messages/%d?transactionHash=%s", base, sourceDomain, txHash)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Inflow{}, err
	}
	httpClient := c.HTTP
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	res, err := httpClient.Do(req)
	if err != nil {
		return Inflow{}, err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return Inflow{}, err
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return Inflow{}, fmt.Errorf("cctp iris %s", strings.TrimSpace(string(body)))
	}
	return ParseCCTPMessage(body, vault, txHash)
}

type cctpMessage struct {
	Messages []struct {
		Status         string `json:"status"`
		DecodedMessage struct {
			DestinationDomain string `json:"destinationDomain"`
			Recipient         string `json:"recipient"`
			DecodedBody       struct {
				Amount        string `json:"amount"`
				MintRecipient string `json:"mintRecipient"`
			} `json:"decodedMessageBody"`
		} `json:"decodedMessage"`
	} `json:"messages"`
}

// ParseCCTPMessage 从 Iris JSON 取出铸给金库的金额。收款地址必须是 vault。
func ParseCCTPMessage(raw []byte, vault, txHash string) (Inflow, error) {
	var body cctpMessage
	if err := json.Unmarshal(raw, &body); err != nil {
		return Inflow{}, err
	}
	if len(body.Messages) == 0 {
		return Inflow{}, fmt.Errorf("cctp message not found yet")
	}
	msg := body.Messages[0]
	if !strings.EqualFold(msg.Status, "complete") {
		return Inflow{}, fmt.Errorf("cctp attestation status %s", msg.Status)
	}
	dest := strings.TrimSpace(msg.DecodedMessage.DestinationDomain)
	if dest != "" && dest != ArcCCTPDomain {
		return Inflow{}, fmt.Errorf("cctp destination domain %s is not Arc (%s)", dest, ArcCCTPDomain)
	}
	recipient := msg.DecodedMessage.DecodedBody.MintRecipient
	if recipient == "" {
		recipient = msg.DecodedMessage.Recipient
	}
	if !sameAddress(recipient, vault) {
		return Inflow{}, fmt.Errorf("cctp mint recipient is not the vault")
	}
	amount, err := parseBaseUnits(msg.DecodedMessage.DecodedBody.Amount)
	if err != nil {
		return Inflow{}, err
	}
	return Inflow{
		TxHash:  strings.ToLower(txHash),
		From:    NormalizeAddress(recipient),
		Amount:  amount,
		Source:  ProductCCTP,
		Product: ProductCCTP,
	}, nil
}

func parseBaseUnits(raw string) (*big.Int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("cctp amount is empty")
	}
	if strings.Contains(raw, ".") {
		return ParseUSDC(raw)
	}
	n, ok := new(big.Int).SetString(raw, 10)
	if !ok || n.Sign() <= 0 {
		return nil, fmt.Errorf("cctp amount is invalid")
	}
	return n, nil
}

func sameAddress(encoded, vault string) bool {
	raw := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(encoded)), "0x")
	if len(raw) < 40 {
		return false
	}
	got := common.HexToAddress("0x" + raw[len(raw)-40:]).Hex()
	return strings.EqualFold(got, common.HexToAddress(vault).Hex())
}
