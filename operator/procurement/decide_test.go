package procurement

import (
	"math/big"
	"testing"
)

func TestDecideHardPaysInsideCaps(t *testing.T) {
	got := DecideHard(Facts{
		CategoryEnabled: true, PayeeAllowed: true,
		Amount: big.NewInt(8_000_000), Remaining: big.NewInt(30_000_000), PerTxCap: big.NewInt(15_000_000),
		QuoteCents: 875, MonthlySpent: 0, MonthlyLimit: 10000, DailyCount: 0, DailyCap: 10,
	})
	if got.Action != "pay" || !got.Submit || got.ReasonCode != "within_policy" {
		t.Fatalf("%+v", got)
	}
}

func TestDecideHardOverCapStillSubmits(t *testing.T) {
	got := DecideHard(Facts{
		CategoryEnabled: true, PayeeAllowed: true,
		Amount: big.NewInt(20_000_000), Remaining: big.NewInt(30_000_000), PerTxCap: big.NewInt(15_000_000),
		QuoteCents: 875, MonthlyLimit: 10000, DailyCap: 10,
	})
	if got.Action != "escalate_to_human" || !got.Submit || got.ReasonCode != "over_tx_cap" {
		t.Fatalf("%+v", got)
	}
}

func TestDecideHardPriceDriftDoesNotSubmit(t *testing.T) {
	got := DecideHard(Facts{
		CategoryEnabled: true, PayeeAllowed: true, Requoted: true,
		QuoteCents: 900, PreviousCents: 875, ToleranceCents: 0,
		Amount: big.NewInt(1), Remaining: big.NewInt(30), PerTxCap: big.NewInt(15),
		DailyCap: 10, MonthlyLimit: 10000,
	})
	if got.Submit || got.ReasonCode != "price_drift" {
		t.Fatalf("%+v", got)
	}
}

func TestDecideHardDailyCapDoesNotSubmit(t *testing.T) {
	got := DecideHard(Facts{
		CategoryEnabled: true, PayeeAllowed: true,
		QuoteCents: 204, DailyCount: 10, DailyCap: 10,
		Amount: big.NewInt(1), Remaining: big.NewInt(30), PerTxCap: big.NewInt(15),
		MonthlyLimit: 10000,
	})
	if got.Submit || got.ReasonCode != "daily_cap" {
		t.Fatalf("%+v", got)
	}
}
