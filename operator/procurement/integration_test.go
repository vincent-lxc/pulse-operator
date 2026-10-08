//go:build integration

package procurement

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestReadChainOptIn 只在显式打开时读取 chain id，不发送交易。
func TestReadChainOptIn(t *testing.T) {
	rpc := os.Getenv("ARC_TESTNET_RPC")
	if rpc == "" || os.Getenv("CONFIRM_TESTNET") != "1" {
		t.Skip("set ARC_TESTNET_RPC and CONFIRM_TESTNET=1")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	id, ok, err := ReadChain(ctx, rpc, TestnetPolicyVault)
	if err != nil {
		t.Fatal(err)
	}
	if !eqChain(id, ArcTestnetChainID) || !ok {
		t.Fatalf("chain %s code %v", id, ok)
	}
}
