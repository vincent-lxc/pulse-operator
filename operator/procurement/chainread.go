// 本文件在付款前读链号，并调用 pendingCount 确认金库代码。
package procurement

import (
	"context"
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
)

// ReadChain 读取 chain id。vault 非空时再调用 pendingCount，成功才算代码核对通过。
func ReadChain(ctx context.Context, rpc, vault string) (*big.Int, bool, error) {
	client, err := ethclient.DialContext(ctx, rpc)
	if err != nil {
		return nil, false, fmt.Errorf("dial: %w", err)
	}
	defer client.Close()
	id, err := client.ChainID(ctx)
	if err != nil {
		return nil, false, err
	}
	if !common.IsHexAddress(vault) {
		return id, false, nil
	}
	addr := common.HexToAddress(vault)
	raw, err := client.CallContract(ctx, ethereum.CallMsg{To: &addr, Data: PendingCountCalldata()}, nil)
	if err != nil || len(raw) < 32 {
		return id, false, nil
	}
	return id, true, nil
}
