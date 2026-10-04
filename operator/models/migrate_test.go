package models

import (
	"os"
	"testing"

	"github.com/digitalwayhk/core/pkg/persistence/database/oltp"
	"gorm.io/gorm"
)

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "operator-models-sqlite")
	if err != nil {
		panic(err)
	}
	if err := os.Chdir(dir); err != nil {
		panic(err)
	}
	os.Exit(m.Run())
}

func TestEnsureStorageAddsColumnsToExistingTables(t *testing.T) {
	if err := EnsureStorage(); err != nil {
		t.Fatal(err)
	}
	sqlite, ok := getDataAction().(*oltp.Sqlite)
	if !ok {
		t.Fatal("expected sqlite")
	}
	raw, err := sqlite.GetModelDB(NewPayable())
	if err != nil {
		t.Fatal(err)
	}
	db := raw.(*gorm.DB)
	for _, stmt := range []string{
		"ALTER TABLE payable DROP COLUMN tx_hash",
		"ALTER TABLE decision_record DROP COLUMN circle_tx_id",
		"ALTER TABLE approval DROP COLUMN circle_state",
	} {
		if err := db.Exec(stmt).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := EnsureStorage(); err != nil {
		t.Fatal(err)
	}
	if err := UpdatePayableSettlement("missing-payable", "paid", "0xabc"); err != nil {
		t.Fatal(err)
	}
	row := NewDecisionRecord()
	row.Code = "migrate-decision"
	row.CircleTxID = "circle-1"
	row.CircleState = "COMPLETE"
	if err := InsertDecision(row); err != nil {
		t.Fatal(err)
	}
	got, err := FindDecision(row.Code)
	if err != nil || got == nil || got.CircleTxID != "circle-1" || got.CircleState != "COMPLETE" {
		t.Fatalf("decision %+v %v", got, err)
	}
	if err := SaveApproval("migrate-approval", "11", "people", "0x2222222222222222222222222222222222222222", "1.000000", row.Code, "pending", "over_tx_cap", "", "circle-1", "COMPLETE"); err != nil {
		t.Fatal(err)
	}
	approval, err := FindApproval("migrate-approval")
	if err != nil || approval == nil || approval.CircleState != "COMPLETE" || approval.CircleTxID != "circle-1" {
		t.Fatalf("approval %+v %v", approval, err)
	}
}
