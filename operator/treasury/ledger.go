// 本文件从 YAML 或 CSV 导入应付账本。
package treasury

import (
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type ledgerFile struct {
	Payables []ledgerRow `yaml:"payables"`
}

type ledgerRow struct {
	ID         string `yaml:"id"`
	Category   string `yaml:"category"`
	Payee      string `yaml:"payee"`
	AmountUSDC string `yaml:"amount_usdc"`
	Due        string `yaml:"due"`
	Memo       string `yaml:"memo"`
}

// LoadLedger 按扩展名读取 YAML 或 CSV 账本。
func LoadLedger(path string) ([]Payable, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if strings.HasSuffix(strings.ToLower(path), ".csv") {
		return parseCSV(string(b))
	}
	return parseYAML(b)
}

func parseYAML(b []byte) ([]Payable, error) {
	var file ledgerFile
	if err := yaml.Unmarshal(b, &file); err != nil {
		return nil, err
	}
	out := make([]Payable, 0, len(file.Payables))
	for _, row := range file.Payables {
		p, err := row.payable()
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

func parseCSV(text string) ([]Payable, error) {
	r := csv.NewReader(strings.NewReader(text))
	r.TrimLeadingSpace = true
	header, err := r.Read()
	if err != nil {
		return nil, err
	}
	idx := map[string]int{}
	for i, h := range header {
		idx[strings.ToLower(strings.TrimSpace(h))] = i
	}
	for _, name := range []string{"id", "category", "payee", "amount_usdc", "due"} {
		if _, ok := idx[name]; !ok {
			return nil, fmt.Errorf("csv missing column %s", name)
		}
	}
	var out []Payable
	for {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		row := ledgerRow{
			ID:         rec[idx["id"]],
			Category:   rec[idx["category"]],
			Payee:      rec[idx["payee"]],
			AmountUSDC: rec[idx["amount_usdc"]],
			Due:        rec[idx["due"]],
		}
		if i, ok := idx["memo"]; ok && i < len(rec) {
			row.Memo = rec[i]
		}
		p, err := row.payable()
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

func (row ledgerRow) payable() (Payable, error) {
	if strings.TrimSpace(row.ID) == "" {
		return Payable{}, fmt.Errorf("payable id is required")
	}
	amount, err := ParseUSDC(row.AmountUSDC)
	if err != nil {
		return Payable{}, fmt.Errorf("payable %s: %w", row.ID, err)
	}
	due, err := time.Parse(time.RFC3339, strings.TrimSpace(row.Due))
	if err != nil {
		return Payable{}, fmt.Errorf("payable %s due: %w", row.ID, err)
	}
	if !strings.HasPrefix(strings.TrimSpace(row.Payee), "0x") {
		return Payable{}, fmt.Errorf("payable %s payee must be an address", row.ID)
	}
	return Payable{
		ID:       strings.TrimSpace(row.ID),
		Category: strings.TrimSpace(row.Category),
		Payee:    NormalizeAddress(row.Payee),
		Amount:   amount,
		Due:      due.UTC(),
		Memo:     strings.TrimSpace(row.Memo),
		Status:   "pending",
	}, nil
}
