// 本文件按 Circle 的公式把 Iris 报价换成 maxFee 和 burn 金额。
package procurement

import (
	"encoding/json"
	"fmt"
	"math"
	"math/big"
)

// FeeQuote 是一条 Iris /v2/burn/USDC/fees 记录。金额是 6 位小数的 USDC 最小单位。
type FeeQuote struct {
	FinalityThreshold uint32
	MinimumFee        float64
	ForwardLow        *big.Int
	ForwardMed        *big.Int
	ForwardHigh       *big.Int
}

type feeWire struct {
	FinalityThreshold uint32  `json:"finalityThreshold"`
	MinimumFee        float64 `json:"minimumFee"`
	ForwardFee        struct {
		Low  *big.Int `json:"low"`
		Med  *big.Int `json:"med"`
		High *big.Int `json:"high"`
	} `json:"forwardFee"`
}

// ParseFeeQuotes 解析 Iris 的费用数组。
func ParseFeeQuotes(raw []byte) ([]FeeQuote, error) {
	var rows []feeWire
	if err := json.Unmarshal(raw, &rows); err != nil {
		return nil, fmt.Errorf("cctp fee quote: %w", err)
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("cctp fee quote is empty")
	}
	out := make([]FeeQuote, 0, len(rows))
	for _, row := range rows {
		out = append(out, FeeQuote{
			FinalityThreshold: row.FinalityThreshold,
			MinimumFee:        row.MinimumFee,
			ForwardLow:        units(row.ForwardFee.Low),
			ForwardMed:        units(row.ForwardFee.Med),
			ForwardHigh:       units(row.ForwardFee.High),
		})
	}
	return out, nil
}

// SelectQuote 优先取指定最终性（1000 是 Fast），否则取阈值最低的一条。
func SelectQuote(quotes []FeeQuote, prefer uint32) (FeeQuote, error) {
	if len(quotes) == 0 {
		return FeeQuote{}, fmt.Errorf("no cctp fee quote")
	}
	best := quotes[0]
	found := false
	for _, q := range quotes {
		if q.FinalityThreshold == prefer {
			return q, nil
		}
		if !found || q.FinalityThreshold < best.FinalityThreshold {
			best = q
			found = true
		}
	}
	return best, nil
}

// Amounts 计算 maxFee 和需要 burn 的总额。
// protocolFee = needed * round(minimumFeeBps*100) / 1_000_000，与 Circle 示例一致。
// maxFee = forwardFee(med|high) + protocolFee。burn = needed + maxFee。
func Amounts(needed *big.Int, q FeeQuote, level string) (maxFee, burn *big.Int, err error) {
	needed = units(needed)
	forward := q.ForwardMed
	if level == "high" {
		forward = q.ForwardHigh
	}
	if level == "low" {
		forward = q.ForwardLow
	}
	forward = units(forward)
	protocol := protocolFee(needed, q.MinimumFee)
	maxFee = new(big.Int).Add(forward, protocol)
	burn = new(big.Int).Add(needed, maxFee)
	return maxFee, burn, nil
}

func protocolFee(amount *big.Int, minimumFeeBps float64) *big.Int {
	if amount == nil || amount.Sign() <= 0 || minimumFeeBps <= 0 {
		return big.NewInt(0)
	}
	scaled := int64(math.Round(minimumFeeBps * 100))
	if scaled <= 0 {
		return big.NewInt(0)
	}
	fee := new(big.Int).Mul(amount, big.NewInt(scaled))
	return fee.Quo(fee, big.NewInt(1_000_000))
}

func units(v *big.Int) *big.Int {
	if v == nil {
		return big.NewInt(0)
	}
	return new(big.Int).Set(v)
}

// CentsToUSDC 把美元分换成 6 位 USDC。1 分 = 10000 最小单位。
func CentsToUSDC(cents int64) *big.Int {
	if cents < 0 {
		cents = 0
	}
	return big.NewInt(cents * 10000)
}
