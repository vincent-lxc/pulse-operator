// 本文件给出审计和看板上的 Circle 产品标签。
package treasury

import "strings"

const (
	// ProductLocalKey 是 go-ethereum 原始私钥执行器。
	ProductLocalKey = "local:key"
	// ProductLocalRPC 是直接读 USDC Transfer 日志的收入路径。
	ProductLocalRPC = "local:rpc"
	// ProductLocalHTTP 是手工补录的收入。
	ProductLocalHTTP = "local:http"
	// ProductWallets 是 Circle Developer-Controlled Wallets 的合约执行。
	ProductWallets = "circle:wallets"
	// ProductAgent 是 Circle Agent Wallet CLI。
	ProductAgent = "circle:agent"
	// ProductCCTP 是 Circle CCTP v2 铸到 Arc 的 USDC。
	ProductCCTP = "circle:cctp"
	// ProductGateway 是 Circle Gateway 的入账通知。
	ProductGateway = "circle:gateway"
)

// SpendingLimits 说明 Agent Wallet 的支出策略能否用在这条链上。
// Circle 只在主网 agent wallet 上接受 circle wallet limit；测试网会被拒绝。
func SpendingLimits(blockchain string) (supported bool, note string) {
	name := strings.ToUpper(strings.TrimSpace(blockchain))
	if name == "" || strings.Contains(name, "TESTNET") || strings.HasSuffix(name, "-SEPOLIA") || strings.HasSuffix(name, "-FUJI") || strings.HasSuffix(name, "-AMOY") {
		return false, "Circle agent-wallet spending policies are mainnet-only; `circle wallet limit` rejects testnet chains including ARC-TESTNET"
	}
	return true, "mainnet agent wallet: a human sets caps with `circle wallet limit set --address <agent> --chain " + name + " --policy-type stablecoin --per-tx <usdc> --daily <usdc> --weekly <usdc> --monthly <usdc>` and confirms the email OTP. The operator records the command and does not submit it."
}
