package procurement

import (
	"math/big"
	"testing"
)

func TestSandboxForwardFeeMath(t *testing.T) {
	raw := []byte(`[{"finalityThreshold":1000,"minimumFee":0,"forwardFee":{"low":54050,"med":54050,"high":54372}},{"finalityThreshold":2000,"minimumFee":0,"forwardFee":{"low":54050,"med":54050,"high":54372}}]`)
	quotes, err := ParseFeeQuotes(raw)
	if err != nil {
		t.Fatal(err)
	}
	q, err := SelectQuote(quotes, 1000)
	if err != nil || q.FinalityThreshold != 1000 {
		t.Fatal(q, err)
	}
	needed := CentsToUSDC(875)
	maxFee, burn, err := Amounts(needed, q, "med")
	if err != nil {
		t.Fatal(err)
	}
	if maxFee.Cmp(big.NewInt(54050)) != 0 {
		t.Fatalf("maxFee %s", maxFee)
	}
	if burn.Cmp(new(big.Int).Add(needed, maxFee)) != 0 {
		t.Fatalf("burn %s", burn)
	}
}

func TestProtocolFeeUsesCircleRounding(t *testing.T) {
	q := FeeQuote{FinalityThreshold: 1000, MinimumFee: 1.3, ForwardMed: big.NewInt(57543)}
	needed := big.NewInt(10_000_000)
	maxFee, burn, err := Amounts(needed, q, "med")
	if err != nil {
		t.Fatal(err)
	}
	// 10 USDC * 1.3 bps = 0.0013 USDC = 1300 units. round(1.3*100)=130.
	if maxFee.Cmp(big.NewInt(57543+1300)) != 0 {
		t.Fatalf("maxFee %s", maxFee)
	}
	if burn.Cmp(big.NewInt(10_000_000+57543+1300)) != 0 {
		t.Fatalf("burn %s", burn)
	}
}
