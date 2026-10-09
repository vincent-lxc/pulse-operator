package models

import (
	"strings"
	"testing"

	"github.com/digitalwayhk/core/pkg/persistence/database/oltp"
	"gorm.io/gorm"
)

func TestFindBillReadsProductionColumnsWhenShadowColumnsAreEmpty(t *testing.T) {
	if err := EnsureStorage(); err != nil {
		t.Fatal(err)
	}
	row := NewBill()
	row.Code = "bill-register-pulseoperator-top"
	row.Domain = "pulseoperator.top"
	row.State = "failed_merchant"
	row.Mode = "mainnet"
	row.VaultTx = "0xvault"
	if _, err := InsertBill(row); err != nil {
		t.Fatal(err)
	}
	db := billTestDB(t)
	attempts := `[{"n":2,"key":"bill-bill-register-pulseoperator-top-a2","signed":true,"valid_before":1791454980,"checkout":"6ac741526bcab4fcd1cee1da","nonce":"0xabc"}]`
	x402 := `{"x402Version":2,"accepts":[{"scheme":"auth-capture"}]}`
	if err := db.Exec(`UPDATE bill SET merchant_attempts=?, latency_ms=?, x402_required=?, risk_notes=?, confidence=?, evidence=?, cctp_message_hash='', forward_fee_units='' WHERE code=?`,
		attempts, 1855, x402, "Low risk", "0.9", `{"vault_chain":"5042"}`, row.Code).Error; err != nil {
		t.Fatal(err)
	}
	addBillColumn(t, db, `ALTER TABLE bill ADD COLUMN merchantAttempts TEXT`)
	addBillColumn(t, db, `ALTER TABLE bill ADD COLUMN x402Required TEXT`)
	addBillColumn(t, db, `ALTER TABLE bill ADD COLUMN riskNotes TEXT`)
	addBillColumn(t, db, `ALTER TABLE bill ADD COLUMN latencyMS INTEGER`)
	addBillColumn(t, db, `ALTER TABLE bill ADD COLUMN cctp_message TEXT`)
	addBillColumn(t, db, `ALTER TABLE bill ADD COLUMN forward_fee TEXT`)
	if err := db.Exec(`UPDATE bill SET merchantAttempts='', x402Required='', riskNotes='', latencyMS=0, cctp_message=?, forward_fee=? WHERE code=?`,
		"0xmessage", "54585", row.Code).Error; err != nil {
		t.Fatal(err)
	}

	got, err := FindBill(row.Code)
	if err != nil || got == nil {
		t.Fatal(err)
	}
	if got.MerchantAttempts != attempts || got.LatencyMS != 1855 || got.X402Required != x402 || got.RiskNotes != "Low risk" || got.Confidence != "0.9" || got.Evidence != `{"vault_chain":"5042"}` {
		t.Fatalf("canonical load attempts=%s latency=%d x402=%s risk=%s conf=%s evidence=%s err=%v", got.MerchantAttempts, got.LatencyMS, got.X402Required, got.RiskNotes, got.Confidence, got.Evidence, err)
	}
	if got.CCTPMessageHash != "0xmessage" || got.ForwardFeeUnits != "54585" {
		t.Fatalf("legacy columns message=%s fee=%s", got.CCTPMessageHash, got.ForwardFeeUnits)
	}
	listed, err := ListBills()
	if err != nil {
		t.Fatal(err)
	}
	var found *Bill
	for _, item := range listed {
		if item != nil && item.Code == row.Code {
			found = item
		}
	}
	if found == nil || found.MerchantAttempts != attempts || found.LatencyMS != 1855 || found.CCTPMessageHash != "0xmessage" {
		t.Fatalf("list %+v", found)
	}
}

func TestFindBillIgnoresUnscannableShadowColumn(t *testing.T) {
	if err := EnsureStorage(); err != nil {
		t.Fatal(err)
	}
	row := NewBill()
	row.Code = "bill-shadow-text-latency"
	row.State = "failed_merchant"
	if _, err := InsertBill(row); err != nil {
		t.Fatal(err)
	}
	db := billTestDB(t)
	_ = db.Exec(`ALTER TABLE bill DROP COLUMN latencyMS`).Error
	_ = db.Exec(`ALTER TABLE bill DROP COLUMN latencyMs`).Error
	addBillColumn(t, db, `ALTER TABLE bill ADD COLUMN latencyMs TEXT`)
	if err := db.Exec(`UPDATE bill SET latency_ms=1855, latencyMs='' WHERE code=?`, row.Code).Error; err != nil {
		t.Fatal(err)
	}
	got, err := FindBill(row.Code)
	if err != nil || got == nil || got.LatencyMS != 1855 {
		t.Fatalf("got %+v err %v", got, err)
	}
}

func addBillColumn(t *testing.T, db *gorm.DB, stmt string) {
	t.Helper()
	if err := db.Exec(stmt).Error; err != nil && !strings.Contains(err.Error(), "duplicate column") {
		t.Fatal(err)
	}
}

func billTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	sqlite, ok := getDataAction().(*oltp.Sqlite)
	if !ok {
		t.Fatal("sqlite")
	}
	raw, err := sqlite.GetModelDB(NewBill())
	if err != nil {
		t.Fatal(err)
	}
	db, ok := raw.(*gorm.DB)
	if !ok || db == nil {
		t.Fatal("db")
	}
	return db
}
