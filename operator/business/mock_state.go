// 本文件让 mock 模式沿用上一轮的余额和预算，避免每次从夹具重扫并重复划转。
package business

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/vincent-lxc/pulse-operator/operator/treasury"
)

type mockStateFile struct {
	Balance    string                  `json:"balance"`
	Categories map[string]mockStateCat `json:"categories"`
	Pending    []mockPending           `json:"pending"`
}

type mockStateCat struct {
	Spent     string `json:"spent"`
	Remaining string `json:"remaining"`
}

type mockPending struct {
	RequestID    string `json:"requestID"`
	Category     string `json:"category"`
	Payee        string `json:"payee"`
	Amount       string `json:"amount"`
	DecisionHash string `json:"decisionHash"`
	Status       string `json:"status"`
}

func mockStatePath(cfg treasury.Config) string {
	dir := filepath.Dir(cfg.AuditLog)
	if dir == "" || dir == "." {
		dir = "data"
	}
	return filepath.Join(dir, "mock-state.json")
}

func overlayMockState(path string, snap treasury.Snapshot) (treasury.Snapshot, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return snap, nil
		}
		return snap, err
	}
	var file mockStateFile
	if err := json.Unmarshal(b, &file); err != nil {
		return snap, err
	}
	if file.Balance != "" {
		balance, err := treasury.ParseUSDCAllowZero(file.Balance)
		if err != nil {
			return snap, err
		}
		snap.Balance = balance
	}
	for name, saved := range file.Categories {
		cat, ok := snap.Categories[name]
		if !ok {
			continue
		}
		spent, err := treasury.ParseUSDCAllowZero(saved.Spent)
		if err != nil {
			return snap, err
		}
		remaining, err := treasury.ParseUSDCAllowZero(saved.Remaining)
		if err != nil {
			return snap, err
		}
		cat.Spent = spent
		cat.Remaining = remaining
		snap.Categories[name] = cat
	}
	if file.Pending != nil {
		snap.Pending = nil
		for _, item := range file.Pending {
			amount, err := treasury.ParseUSDCAllowZero(item.Amount)
			if err != nil {
				return snap, err
			}
			snap.Pending = append(snap.Pending, treasury.Approval{
				RequestID: item.RequestID, Category: item.Category, Payee: item.Payee,
				Amount: amount, DecisionHash: item.DecisionHash, Status: item.Status,
			})
		}
	}
	return snap, nil
}

func saveMockState(path string, report treasury.Report) error {
	if path == "" {
		return nil
	}
	file := mockStateFile{
		Balance:    treasury.FormatUSDC(report.Balance),
		Categories: map[string]mockStateCat{},
	}
	for name, cat := range report.Categories {
		file.Categories[name] = mockStateCat{
			Spent:     treasury.FormatUSDC(cat.Spent),
			Remaining: treasury.FormatUSDC(cat.Remaining),
		}
	}
	file.Pending = pendingFromReport(report)
	if file.Pending == nil {
		if prev, err := os.ReadFile(path); err == nil {
			var old mockStateFile
			if json.Unmarshal(prev, &old) == nil {
				file.Pending = old.Pending
			}
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	raw, err := json.Marshal(file)
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(raw, '\n'), 0o644)
}

func pendingFromReport(report treasury.Report) []mockPending {
	var out []mockPending
	for _, d := range report.Decisions {
		if d.Outcome != "simulated_approval" || d.RequestID == "" {
			continue
		}
		out = append(out, mockPending{
			RequestID: d.RequestID, Category: d.Category, Payee: d.Payee,
			Amount: treasury.FormatUSDC(d.Amount), DecisionHash: d.DecisionHash, Status: "pending",
		})
	}
	return out
}

func saveMockSnapshot(path string, snap treasury.Snapshot) error {
	file := mockStateFile{
		Balance:    treasury.FormatUSDC(snap.Balance),
		Categories: map[string]mockStateCat{},
		Pending:    []mockPending{},
	}
	for name, cat := range snap.Categories {
		file.Categories[name] = mockStateCat{
			Spent:     treasury.FormatUSDC(cat.Spent),
			Remaining: treasury.FormatUSDC(cat.Remaining),
		}
	}
	for _, item := range snap.Pending {
		if item.Status != "pending" {
			continue
		}
		file.Pending = append(file.Pending, mockPending{
			RequestID: item.RequestID, Category: item.Category, Payee: item.Payee,
			Amount: treasury.FormatUSDC(item.Amount), DecisionHash: item.DecisionHash, Status: item.Status,
		})
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	raw, err := json.Marshal(file)
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(raw, '\n'), 0o644)
}
