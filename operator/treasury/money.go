// 本文件提供 Arc USDC（6 位小数）的整数解析与展示，避免浮点进入决策。
package treasury

import (
	"fmt"
	"math/big"
	"strings"
)

const usdcDecimals = 6

// ParseUSDC 把最多 6 位小数的正数字符串转成最小单位。
func ParseUSDC(s string) (*big.Int, error) {
	return parseUSDC(s, false)
}

// ParseUSDCAllowZero 与 ParseUSDC 相同，但接受 0。用于已花费和储备下限。
func ParseUSDCAllowZero(s string) (*big.Int, error) {
	return parseUSDC(s, true)
}

func parseUSDC(s string, allowZero bool) (*big.Int, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, fmt.Errorf("amount is empty")
	}
	neg := false
	if strings.HasPrefix(s, "-") {
		neg = true
		s = strings.TrimPrefix(s, "-")
	}
	parts := strings.Split(s, ".")
	if len(parts) > 2 || parts[0] == "" && len(parts) == 1 {
		return nil, fmt.Errorf("invalid usdc amount %q", s)
	}
	whole := parts[0]
	frac := ""
	if len(parts) == 2 {
		frac = parts[1]
	}
	if whole == "" {
		whole = "0"
	}
	if !digits(whole) || !digits(frac) || len(frac) > usdcDecimals {
		return nil, fmt.Errorf("invalid usdc amount %q", s)
	}
	if len(frac) < usdcDecimals {
		frac += strings.Repeat("0", usdcDecimals-len(frac))
	}
	n, ok := new(big.Int).SetString(whole+frac, 10)
	if !ok || n.Sign() < 0 {
		return nil, fmt.Errorf("invalid usdc amount %q", s)
	}
	if n.Sign() == 0 && !allowZero {
		return nil, fmt.Errorf("amount must be positive")
	}
	if neg {
		n.Neg(n)
	}
	return n, nil
}

// FormatUSDC 把最小单位格式化为固定 6 位小数。
func FormatUSDC(units *big.Int) string {
	if units == nil {
		return "0.000000"
	}
	n := new(big.Int).Set(units)
	sign := ""
	if n.Sign() < 0 {
		sign = "-"
		n.Abs(n)
	}
	base := big.NewInt(1_000_000)
	ip := new(big.Int).Quo(n, base)
	frac := new(big.Int).Mod(n, base)
	return fmt.Sprintf("%s%s.%06d", sign, ip.String(), frac.Int64())
}

func digits(s string) bool {
	if s == "" {
		return true
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func unitsOrZero(v *big.Int) *big.Int {
	if v == nil {
		return big.NewInt(0)
	}
	return new(big.Int).Set(v)
}
