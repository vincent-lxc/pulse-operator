// 本文件把一轮结果打印成可录屏阅读的文本。
package business

import (
	"fmt"
	"io"

	"github.com/vincent-lxc/pulse-operator/operator/treasury"
)

// WriteReport 按固定列打印观察、流动性和每条决策。
func WriteReport(out io.Writer, report treasury.Report) error {
	balance := report.OpeningBalance
	if balance == nil {
		balance = report.Balance
	}
	if _, err := fmt.Fprintf(out, "run %s balance %s USDC inflows %d\n", report.RunID, treasury.FormatUSDC(balance), len(report.Inflows)); err != nil {
		return err
	}
	for _, in := range report.Inflows {
		if _, err := fmt.Fprintf(out, "revenue source=%s circle=%s amount=%s from=%s ref=%s\n", in.Source, in.Product, treasury.FormatUSDC(in.Amount), in.From, in.TxHash); err != nil {
			return err
		}
	}
	liq := report.OpeningLiquidity
	if liq.Balance == nil {
		liq = report.Liquidity
	}
	if _, err := fmt.Fprintf(out, "liquidity balance=%s obligations=%s floor=%s target=%s surplus=%s\n",
		treasury.FormatUSDC(liq.Balance), treasury.FormatUSDC(liq.Obligations), treasury.FormatUSDC(liq.ReserveFloor), treasury.FormatUSDC(liq.ReserveTarget), treasury.FormatUSDC(liq.Surplus)); err != nil {
		return err
	}
	for _, d := range report.Decisions {
		line := fmt.Sprintf("decision action=%s payable=%s amount=%s reason=%s outcome=%s circle=%s hash=%s tx=%s",
			d.Action, d.PayableID, treasury.FormatUSDC(d.Amount), d.ReasonCode, d.Outcome, d.Product, d.DecisionHash, d.TxHash)
		if d.CircleTxID != "" {
			line += " circle_tx=" + d.CircleTxID
		}
		if d.CircleState != "" {
			line += " circle_state=" + d.CircleState
		}
		if _, err := fmt.Fprintf(out, "%s\n", line); err != nil {
			return err
		}
	}
	for _, n := range report.Notices {
		if _, err := fmt.Fprintf(out, "notice kind=%s text=%s\n", n.Kind, n.Text); err != nil {
			return err
		}
	}
	return nil
}
