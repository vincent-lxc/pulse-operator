package procurement

import (
	"math/big"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
)

// EncodeERC20Transfer 编码把剩余 USDC 转回金库的普通 transfer。
func EncodeERC20Transfer(to common.Address, amount *big.Int) ([]byte, error) {
	args := abi.Arguments{{Type: mustABI("address")}, {Type: mustABI("uint256")}}
	packed, err := args.Pack(to, unitsOf(amount))
	if err != nil {
		return nil, err
	}
	return append(mustABISelector("transfer(address,uint256)"), packed...), nil
}
