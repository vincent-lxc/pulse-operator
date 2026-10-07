// 本文件记录 2026-10-07 从 Circle 文档抄下的地址。配置可以覆盖它们。
package procurement

// 这些地址来自：
// https://developers.circle.com/cctp/references/contract-addresses.md
// https://developers.circle.com/stablecoins/usdc-contract-addresses.md
// Arc 主网 TokenMessenger / MessageTransmitter 与协调人核对过的 Arc 文档一致。
const (
	ArcUSDC = "0x3600000000000000000000000000000000000000"

	BaseUSDC        = "0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913"
	BaseSepoliaUSDC = "0x036CbD53842c5426634e7929541eC2318f3dCF7e"

	// 主网 TokenMessengerV2，Arc 与 Base 都是这个地址。
	MainnetTokenMessenger = "0x28b5a0e9C621a5BadaA536219b3a228C8168cf5d"
	// 测试网 TokenMessengerV2，Arc testnet 与 Base Sepolia 都是这个地址。
	TestnetTokenMessenger = "0x8FE6B999Dc680CcFDD5Bf7EB0974218be2542DAA"

	BaseMessageTransmitter        = "0x81D40F21F12A8F0E3252Bccb954D722d4c464B64"
	BaseSepoliaMessageTransmitter = "0xE737e5cEBEEBa77EFE34D4aa090756590b1CE275"

	ArcMainnetMessageTransmitter = "0x81D40F21F12A8F0E3252Bccb954D722d4c464B64"
	ArcTestnetMessageTransmitter = "0xE737e5cEBEEBa77EFE34D4aa090756590b1CE275"

	// Forwarding Service hook：magic "cctp-forward" + version 0 + length 0。
	// https://developers.circle.com/cctp/howtos/transfer-usdc-with-forwarding-service.md
	ForwardHook = "0x636374702d666f72776172640000000000000000000000000000000000000000"

	// 这个地址在 Arc 测试网是 PolicyVault，在 Arc 主网是 PulseReceipt，不能当主网金库。
	TestnetPolicyVault = "0x4FACE6592Ba1AdF83E35B01CcD93D8704d647C01"

	AuthCaptureEscrowV11 = "0xf96815976523E00e65Be8f34cA5e64b4f41EB19c"
	EIP3009CollectorV11  = "0x8612dfdc421f80336cd14E8EF9cb1E765dB5ab88"

	ArcDomain  = uint32(26)
	BaseDomain = uint32(6)

	BaseChainID        int64 = 8453
	BaseSepoliaChainID int64 = 84532
	ArcMainnetChainID  int64 = 5042
	ArcTestnetChainID  int64 = 5042002
)
