package procurement

import "math/big"

// ArcGasPrice 用 base fee 加小费，没有 base fee 时用 eth_gasPrice。结果不超过配置上限。
// Arc 上低于 20 gwei 时抬到 20 gwei，不再直接打到上限。
func ArcGasPrice(suggested, baseFee, tip, maxFee *big.Int) *big.Int {
	floor := big.NewInt(20_000_000_000)
	var price *big.Int
	if baseFee != nil && baseFee.Sign() > 0 {
		add := tip
		if add == nil || add.Sign() <= 0 {
			add = big.NewInt(2_000_000_000)
		}
		price = new(big.Int).Add(baseFee, add)
	} else if suggested != nil && suggested.Sign() > 0 {
		price = new(big.Int).Set(suggested)
	} else if maxFee != nil && maxFee.Sign() > 0 {
		price = new(big.Int).Set(maxFee)
	} else {
		price = new(big.Int).Set(floor)
	}
	if maxFee != nil && maxFee.Sign() > 0 && price.Cmp(maxFee) > 0 {
		price = new(big.Int).Set(maxFee)
	}
	if price.Cmp(floor) < 0 && (maxFee == nil || maxFee.Cmp(floor) >= 0) {
		price = new(big.Int).Set(floor)
	}
	return price
}

func arcChain(chainID *big.Int) bool {
	if chainID == nil {
		return false
	}
	return chainID.Cmp(big.NewInt(ArcMainnetChainID)) == 0 || chainID.Cmp(big.NewInt(ArcTestnetChainID)) == 0
}
