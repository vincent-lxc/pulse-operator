// 本文件把看板和 HTTP 入参限制在 USDC 十进制格式。
package business

import (
	"strings"

	"github.com/vincent-lxc/pulse-operator/operator/models"
	"github.com/vincent-lxc/pulse-operator/operator/treasury"
)

func parsePositive(amount string) (string, error) {
	n, err := treasury.ParseUSDC(strings.TrimSpace(amount))
	if err != nil {
		return "", models.NewValidationError("amount_usdc must be a positive decimal with at most 6 places")
	}
	return treasury.FormatUSDC(n), nil
}
