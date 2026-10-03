package treasury

import (
	"path/filepath"
	"testing"
)

func TestLoadCSVLedger(t *testing.T) {
	rows, err := LoadLedger(filepath.Join(moduleRoot(t), "testdata", "ledger.csv"))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].ID != "csv-item" || FormatUSDC(rows[0].Amount) != "1.250000" {
		t.Fatalf("%+v", rows)
	}
}
