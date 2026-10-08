package procurement

import (
	"math/big"
	"testing"
)

func TestArcGasPriceUsesSuggestionUnderTheCap(t *testing.T) {
	gwei := func(n int64) *big.Int {
		return new(big.Int).Mul(big.NewInt(n), big.NewInt(1_000_000_000))
	}
	got := ArcGasPrice(gwei(20), nil, gwei(2), gwei(50))
	if got.Cmp(gwei(20)) != 0 {
		t.Fatalf("suggested %s", got)
	}
	got = ArcGasPrice(gwei(80), nil, gwei(2), gwei(50))
	if got.Cmp(gwei(50)) != 0 {
		t.Fatalf("capped %s", got)
	}
	got = ArcGasPrice(nil, gwei(21), gwei(2), gwei(50))
	if got.Cmp(gwei(23)) != 0 {
		t.Fatalf("base+tip %s", got)
	}
	got = ArcGasPrice(gwei(1), nil, gwei(2), gwei(50))
	if got.Cmp(gwei(20)) != 0 {
		t.Fatalf("floor %s", got)
	}
}
