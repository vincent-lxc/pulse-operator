// 本文件加载 dry-run 使用的金库快照，测试和演示都不访问网络。
package treasury

import (
	"math/big"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

type fixtureFile struct {
	BalanceUSDC string            `yaml:"balance_usdc"`
	Paused      bool              `yaml:"paused"`
	Reserve     string            `yaml:"reserve"`
	Categories  []fixtureCategory `yaml:"categories"`
	Inflows     []fixtureInflow   `yaml:"inflows"`
}

type fixtureCategory struct {
	Name          string   `yaml:"name"`
	BudgetUSDC    string   `yaml:"budget_usdc"`
	PerTxCapUSDC  string   `yaml:"per_tx_cap_usdc"`
	SpentUSDC     string   `yaml:"spent_usdc"`
	PeriodSeconds uint64   `yaml:"period_seconds"`
	Payees        []string `yaml:"payees"`
}

type fixtureInflow struct {
	TxHash     string `yaml:"tx_hash"`
	From       string `yaml:"from"`
	AmountUSDC string `yaml:"amount_usdc"`
	Block      uint64 `yaml:"block"`
}

// LoadFixture 读取金库 YAML 快照。
func LoadFixture(path string) (Snapshot, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Snapshot{}, err
	}
	var file fixtureFile
	if err := yaml.Unmarshal(b, &file); err != nil {
		return Snapshot{}, err
	}
	balance, err := ParseUSDC(file.BalanceUSDC)
	if err != nil {
		return Snapshot{}, err
	}
	snap := Snapshot{
		Balance:    balance,
		Paused:     file.Paused,
		Reserve:    NormalizeAddress(file.Reserve),
		Categories: map[string]Category{},
	}
	for _, c := range file.Categories {
		budget, err := ParseUSDC(c.BudgetUSDC)
		if err != nil {
			return Snapshot{}, err
		}
		cap, err := ParseUSDC(c.PerTxCapUSDC)
		if err != nil {
			return Snapshot{}, err
		}
		spent, err := ParseUSDCAllowZero(emptyZero(c.SpentUSDC))
		if err != nil {
			return Snapshot{}, err
		}
		remaining := new(big.Int).Sub(budget, spent)
		if remaining.Sign() < 0 {
			remaining = big.NewInt(0)
		}
		payees := map[string]bool{}
		for _, p := range c.Payees {
			payees[NormalizeAddress(p)] = true
		}
		snap.Categories[c.Name] = Category{
			Name:          c.Name,
			Enabled:       true,
			Budget:        budget,
			PerTxCap:      cap,
			Spent:         spent,
			Remaining:     remaining,
			PeriodSeconds: c.PeriodSeconds,
			Payees:        payees,
		}
	}
	for _, in := range file.Inflows {
		amount, err := ParseUSDC(in.AmountUSDC)
		if err != nil {
			return Snapshot{}, err
		}
		snap.Inflows = append(snap.Inflows, Inflow{
			TxHash: strings.ToLower(in.TxHash),
			From:   NormalizeAddress(in.From),
			Amount: amount,
			Block:  in.Block,
			Source: "chain",
		})
	}
	return snap, nil
}

func emptyZero(s string) string {
	if strings.TrimSpace(s) == "" {
		return "0"
	}
	return s
}
