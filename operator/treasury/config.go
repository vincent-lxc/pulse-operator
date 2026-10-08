// 本文件读取运行配置。私钥和 RPC 只出现环境变量名或 0600 文件路径，不写入配置值。
package treasury

import (
	"fmt"
	"math/big"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config 是 dry-run 与 live 共用的配置。
type Config struct {
	Mode               string            `yaml:"mode"`
	AgentID            string            `yaml:"agentID"`
	ChainID            string            `yaml:"chainID"`
	Vault              string            `yaml:"vault"`
	Agent              string            `yaml:"agent"`
	USDC               string            `yaml:"usdc"`
	ChainDriver        string            `yaml:"chainDriver"`
	VaultFixture       string            `yaml:"vaultFixture"`
	Ledger             string            `yaml:"ledger"`
	AuditLog           string            `yaml:"auditLog"`
	FromBlock          uint64            `yaml:"fromBlock"`
	DueHorizon         time.Duration     `yaml:"dueHorizon"`
	Cooldown           time.Duration     `yaml:"cooldown"`
	ReserveFloorUSDC   string            `yaml:"reserveFloorUSDC"`
	ReserveTargetUSDC  string            `yaml:"reserveTargetUSDC"`
	Clock              string            `yaml:"clock"`
	Listen             string            `yaml:"listen"`
	LoopEnabled        bool              `yaml:"loopEnabled"`
	LoopInterval       time.Duration     `yaml:"loopInterval"`
	MaxSpendPerRunUSDC string            `yaml:"maxSpendPerRunUSDC"`
	LogLookback        uint64            `yaml:"logLookback"`
	LogChunk           uint64            `yaml:"logChunk"`
	FullLogScan        bool              `yaml:"fullLogScan"`
	ScanCursor         string            `yaml:"scanCursor"`
	LLM                LLMConfig         `yaml:"llm"`
	Notify             NotifyConfig      `yaml:"notify"`
	Secrets            SecretConfig      `yaml:"secrets"`
	Gas                GasConfig         `yaml:"gas"`
	Executor           string            `yaml:"executor"`
	Circle             CircleConfig      `yaml:"circle"`
	Planner            PlannerConfig     `yaml:"planner"`
	Porkbun            PorkbunConfig     `yaml:"porkbun"`
	Procurement        ProcurementConfig `yaml:"procurement"`
	Base               BaseChainConfig   `yaml:"base"`
	CCTP               BridgeConfig      `yaml:"cctpBridge"`
	BillsFile          string            `yaml:"billsFile"`
	MaxBillUSDC        string            `yaml:"maxBillUSDC"`
	// AcknowledgeExposedAdminView 确认操作者知道管理界面监听所有网卡，且框架 testtoken 会向局域网签发管理员 token。
	// 主网在 -view 不是 0 时，没有这项或 OPERATOR_ACK_EXPOSED_VIEW=1 就拒绝启动。
	AcknowledgeExposedAdminView bool `yaml:"acknowledgeExposedAdminView"`
}

// PlannerConfig 选择规则或 Vercel AI Gateway。密钥只写环境变量名。
type PlannerConfig struct {
	Driver    string        `yaml:"driver"`
	Model     string        `yaml:"model"`
	BaseURL   string        `yaml:"baseURL"`
	APIKeyEnv string        `yaml:"apiKeyEnv"`
	Timeout   time.Duration `yaml:"timeout"`
	JevKeyEnv string        `yaml:"jevKeyEnv"`
	JevURL    string        `yaml:"jevURL"`
	JevModel  string        `yaml:"jevModel"`
}

// PorkbunConfig 是域名账单的报价和限额。密钥只写环境变量名或 0600 路径。
type PorkbunConfig struct {
	APIBase             string `yaml:"apiBase"`
	APIKeyEnv           string `yaml:"apiKeyEnv"`
	APIKeyFile          string `yaml:"apiKeyFile"`
	SecretEnv           string `yaml:"secretEnv"`
	SecretFile          string `yaml:"secretFile"`
	MonthlyLimitCents   int64  `yaml:"monthlyLimitCents"`
	DailyCap            int    `yaml:"dailyCap"`
	PriceToleranceCents int64  `yaml:"priceToleranceCents"`
	FeeBufferUSDC       string `yaml:"feeBufferUSDC"`
}

// ProcurementConfig 是代理采购钱包。Arc 与 Base 地址不一致时必须显式写出 Base 地址。
type ProcurementConfig struct {
	Address          string `yaml:"address"`
	BaseAddress      string `yaml:"baseAddress"`
	WalletIDEnv      string `yaml:"walletIDEnv"`
	WalletIDFile     string `yaml:"walletIDFile"`
	BaseWalletIDEnv  string `yaml:"baseWalletIDEnv"`
	BaseWalletIDFile string `yaml:"baseWalletIDFile"`
	KeyEnv           string `yaml:"keyEnv"`
	KeyFile          string `yaml:"keyFile"`
}

// BaseChainConfig 是 CCTP 的目标链。
type BaseChainConfig struct {
	ChainID string `yaml:"chainID"`
	RPCEnv  string `yaml:"rpcEnv"`
	USDC    string `yaml:"usdc"`
}

// BridgeConfig 是 Arc 到 Base 的 CCTP V2。
type BridgeConfig struct {
	Forward         bool   `yaml:"forward"`
	AllowStandard   bool   `yaml:"allowStandard"`
	FeeLevel        string `yaml:"feeLevel"`
	SourceDomain    uint32 `yaml:"sourceDomain"`
	DestDomain      uint32 `yaml:"destDomain"`
	TokenMessenger  string `yaml:"tokenMessenger"`
	BaseMessenger   string `yaml:"baseMessenger"`
	BaseTransmitter string `yaml:"baseTransmitter"`
}

// CircleConfig 选择 Circle 产品。值里只有环境变量名和文件路径，没有密钥。
type CircleConfig struct {
	Blockchain        string     `yaml:"blockchain"`
	APIBase           string     `yaml:"apiBase"`
	IrisBase          string     `yaml:"irisBase"`
	CLI               string     `yaml:"cli"`
	APIKeyEnv         string     `yaml:"apiKeyEnv"`
	APIKeyFile        string     `yaml:"apiKeyFile"`
	EntitySecretEnv   string     `yaml:"entitySecretEnv"`
	EntitySecretFile  string     `yaml:"entitySecretFile"`
	WalletIDEnv       string     `yaml:"walletIDEnv"`
	WalletIDFile      string     `yaml:"walletIDFile"`
	OwnerWalletIDEnv  string     `yaml:"ownerWalletIDEnv"`
	OwnerWalletIDFile string     `yaml:"ownerWalletIDFile"`
	AgentAddressEnv   string     `yaml:"agentAddressEnv"`
	OwnerAddressEnv   string     `yaml:"ownerAddressEnv"`
	FeeLevel          string     `yaml:"feeLevel"`
	ExplicitGas       bool       `yaml:"explicitGas"`
	CCTP              CCTPConfig `yaml:"cctp"`
}

// CCTPConfig 列出要向 Iris 确认的 burn 交易。dry-run 不拨号。
type CCTPConfig struct {
	Enabled bool       `yaml:"enabled"`
	Burns   []CCTPBurn `yaml:"burns"`
}

// CCTPBurn 是一笔源链 burn，用来在 Arc 上确认 USDC 铸出。
type CCTPBurn struct {
	SourceDomain uint32 `yaml:"sourceDomain"`
	TxHash       string `yaml:"txHash"`
}

// LLMConfig 控制可选软判断，默认关闭。
type LLMConfig struct {
	Enabled bool   `yaml:"enabled"`
	URLEnv  string `yaml:"urlEnv"`
}

// NotifyConfig 选择通知实现。密钥只写环境变量名或 0600 文件路径。
type NotifyConfig struct {
	Driver    string `yaml:"driver"`
	TokenEnv  string `yaml:"tokenEnv"`
	TokenFile string `yaml:"tokenFile"`
	ChatEnv   string `yaml:"chatEnv"`
	ChatFile  string `yaml:"chatFile"`
}

// SecretConfig 只保存环境变量名和文件路径。
type SecretConfig struct {
	RPCEnv       string `yaml:"rpcEnv"`
	AgentKeyEnv  string `yaml:"agentKeyEnv"`
	AgentKeyFile string `yaml:"agentKeyFile"`
	OwnerKeyEnv  string `yaml:"ownerKeyEnv"`
	OwnerKeyFile string `yaml:"ownerKeyFile"`
}

// GasConfig 是 Arc 上以 USDC 支付的 gas 上限。
// maxFee 和 priorityFee 的单位是 gwei。gasLimit 是 gas 单位，只在 Circle explicitGas 时发送。
type GasConfig struct {
	MaxFeePerGasGwei         int64  `yaml:"maxFeePerGasGwei"`
	MaxPriorityFeePerGasGwei int64  `yaml:"maxPriorityFeePerGasGwei"`
	GasLimit                 uint64 `yaml:"gasLimit"`
}

// LoadConfig 从 YAML 读取配置并填入默认值。
func LoadConfig(path string) (Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	var cfg Config
	if err := yaml.Unmarshal(b, &cfg); err != nil {
		return Config{}, err
	}
	if cfg.ChainDriver == "" {
		cfg.ChainDriver = "mock"
	}
	if cfg.Gas.MaxFeePerGasGwei == 0 {
		cfg.Gas.MaxFeePerGasGwei = 50
	}
	if cfg.Gas.MaxPriorityFeePerGasGwei == 0 {
		cfg.Gas.MaxPriorityFeePerGasGwei = 2
	}
	if cfg.USDC == "" {
		cfg.USDC = "0x3600000000000000000000000000000000000000"
	}
	if cfg.Secrets.RPCEnv == "" {
		cfg.Secrets.RPCEnv = "ARC_RPC_URL"
	}
	if cfg.Secrets.AgentKeyEnv == "" {
		cfg.Secrets.AgentKeyEnv = "OPERATOR_PRIVATE_KEY"
	}
	if cfg.Secrets.OwnerKeyEnv == "" {
		cfg.Secrets.OwnerKeyEnv = "OWNER_PRIVATE_KEY"
	}
	if cfg.LogChunk == 0 {
		cfg.LogChunk = 9000
	}
	if cfg.LogLookback == 0 && !cfg.FullLogScan {
		cfg.LogLookback = 9000
	}
	if cfg.ScanCursor == "" {
		cfg.ScanCursor = "data/log-cursor.json"
	}
	if cfg.Listen == "" {
		cfg.Listen = "127.0.0.1"
	}
	if cfg.Executor == "" {
		cfg.Executor = "raw-key"
	}
	if cfg.Circle.APIBase == "" {
		cfg.Circle.APIBase = "https://api.circle.com"
	}
	if cfg.Circle.IrisBase == "" {
		cfg.Circle.IrisBase = "https://iris-api-sandbox.circle.com"
	}
	if cfg.Circle.CLI == "" {
		cfg.Circle.CLI = "circle"
	}
	if cfg.Circle.APIKeyEnv == "" {
		cfg.Circle.APIKeyEnv = "CIRCLE_API_KEY"
	}
	if cfg.Circle.EntitySecretEnv == "" {
		cfg.Circle.EntitySecretEnv = "CIRCLE_ENTITY_SECRET"
	}
	if cfg.Circle.WalletIDEnv == "" {
		cfg.Circle.WalletIDEnv = "CIRCLE_WALLET_ID"
	}
	if cfg.Circle.OwnerWalletIDEnv == "" {
		cfg.Circle.OwnerWalletIDEnv = "CIRCLE_OWNER_WALLET_ID"
	}
	if cfg.Circle.AgentAddressEnv == "" {
		cfg.Circle.AgentAddressEnv = "CIRCLE_AGENT_ADDRESS"
	}
	if cfg.Circle.OwnerAddressEnv == "" {
		cfg.Circle.OwnerAddressEnv = "CIRCLE_OWNER_ADDRESS"
	}
	if cfg.Circle.Blockchain == "" {
		cfg.Circle.Blockchain = BlockchainForChain(cfg.ChainID)
	}
	if cfg.Circle.FeeLevel == "" {
		cfg.Circle.FeeLevel = "MEDIUM"
	}
	if cfg.Notify.TokenEnv == "" {
		cfg.Notify.TokenEnv = "TELEGRAM_BOT_TOKEN"
	}
	if cfg.Notify.ChatEnv == "" {
		cfg.Notify.ChatEnv = "TELEGRAM_CHAT_ID"
	}
	if cfg.Planner.Driver == "" {
		cfg.Planner.Driver = "rules"
	}
	if cfg.Planner.Model == "" {
		cfg.Planner.Model = "openai/gpt-5.4-nano"
	}
	if cfg.Planner.BaseURL == "" {
		cfg.Planner.BaseURL = "https://ai-gateway.vercel.sh/v1"
	}
	if cfg.Planner.APIKeyEnv == "" {
		cfg.Planner.APIKeyEnv = "AI_GATEWAY_API_KEY"
	}
	if cfg.Planner.Timeout <= 0 {
		cfg.Planner.Timeout = 20 * time.Second
	}
	if cfg.Planner.JevKeyEnv == "" {
		cfg.Planner.JevKeyEnv = "JEV_API_KEY"
	}
	if cfg.Planner.JevURL == "" {
		cfg.Planner.JevURL = "https://api.typesafe.ai/v1/systemone"
	}
	if cfg.Planner.JevModel == "" {
		cfg.Planner.JevModel = "jev-latest"
	}
	if cfg.Porkbun.APIBase == "" {
		cfg.Porkbun.APIBase = "https://api.porkbun.com/api/json/v3"
	}
	if cfg.Porkbun.APIKeyEnv == "" {
		cfg.Porkbun.APIKeyEnv = "PORKBUN_API_KEY"
	}
	if cfg.Porkbun.SecretEnv == "" {
		cfg.Porkbun.SecretEnv = "PORKBUN_SECRET_API_KEY"
	}
	if cfg.Porkbun.MonthlyLimitCents == 0 {
		cfg.Porkbun.MonthlyLimitCents = 10000
	}
	if cfg.Porkbun.DailyCap == 0 {
		cfg.Porkbun.DailyCap = 10
	}
	if cfg.Porkbun.FeeBufferUSDC == "" {
		cfg.Porkbun.FeeBufferUSDC = "0.02"
	}
	if cfg.Procurement.WalletIDEnv == "" {
		cfg.Procurement.WalletIDEnv = "CIRCLE_PROCUREMENT_WALLET_ID"
	}
	if cfg.Procurement.BaseWalletIDEnv == "" {
		cfg.Procurement.BaseWalletIDEnv = "CIRCLE_PROCUREMENT_BASE_WALLET_ID"
	}
	if cfg.Procurement.KeyEnv == "" {
		cfg.Procurement.KeyEnv = "PROCUREMENT_PRIVATE_KEY"
	}
	if cfg.Base.RPCEnv == "" {
		cfg.Base.RPCEnv = "BASE_RPC_URL"
	}
	if cfg.CCTP.SourceDomain == 0 {
		cfg.CCTP.SourceDomain = 26
	}
	if cfg.CCTP.DestDomain == 0 {
		cfg.CCTP.DestDomain = 6
	}
	if cfg.CCTP.FeeLevel == "" {
		cfg.CCTP.FeeLevel = "med"
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// Validate 检查模式、储备和 gas 下限。
func (c Config) Validate() error {
	switch c.Mode {
	case "dry-run", "live", "testnet", "mainnet":
	default:
		return fmt.Errorf("mode must be dry-run, testnet, or mainnet")
	}
	if c.ChainDriver != "mock" && c.ChainDriver != "rpc" {
		return fmt.Errorf("chainDriver must be mock or rpc")
	}
	switch c.Executor {
	case "raw-key", "circle-wallets", "circle-agent":
	default:
		return fmt.Errorf("executor must be raw-key, circle-wallets, or circle-agent")
	}
	if (c.Mode == "live" || c.Mode == "testnet" || c.Mode == "mainnet") && c.ChainDriver != "rpc" {
		return fmt.Errorf("%s mode requires chainDriver rpc", c.Mode)
	}
	if c.Mode == "mainnet" && c.ChainID != "5042" {
		return fmt.Errorf("mainnet mode requires chainID 5042")
	}
	if c.Mode == "testnet" && c.ChainID != "5042002" {
		return fmt.Errorf("testnet mode requires chainID 5042002")
	}
	switch c.Planner.Driver {
	case "", "rules", "gateway":
	default:
		return fmt.Errorf("planner.driver must be rules or gateway")
	}
	floor, err := ParseUSDCAllowZero(c.ReserveFloorUSDC)
	if err != nil {
		return fmt.Errorf("reserveFloorUSDC: %w", err)
	}
	target, err := ParseUSDCAllowZero(c.ReserveTargetUSDC)
	if err != nil {
		return fmt.Errorf("reserveTargetUSDC: %w", err)
	}
	if target.Cmp(floor) < 0 {
		return fmt.Errorf("reserve target must be >= reserve floor")
	}
	if c.Gas.MaxFeePerGasGwei != 0 && c.Gas.MaxFeePerGasGwei < 20 {
		return fmt.Errorf("maxFeePerGasGwei must be >= 20 on Arc")
	}
	if c.Circle.ExplicitGas {
		if c.Gas.GasLimit == 0 {
			return fmt.Errorf("circle explicitGas requires gas.gasLimit")
		}
		if c.Gas.MaxPriorityFeePerGasGwei <= 0 {
			return fmt.Errorf("circle explicitGas requires maxPriorityFeePerGasGwei")
		}
	}
	if c.LoopEnabled {
		if c.LoopInterval <= 0 {
			return fmt.Errorf("loopEnabled requires loopInterval > 0")
		}
		max, err := ParseUSDCAllowZero(c.MaxSpendPerRunUSDC)
		if err != nil || max == nil || max.Sign() <= 0 {
			return fmt.Errorf("loopEnabled requires maxSpendPerRunUSDC > 0")
		}
	}
	if c.Vault == "" || c.Agent == "" || c.ChainID == "" {
		return fmt.Errorf("vault, agent and chainID are required")
	}
	return nil
}

// PolicyFrom 把配置转成硬规则。now 为空时使用配置里的 clock 或当前时间。
func (c Config) PolicyFrom(now time.Time) (Policy, error) {
	floor, err := ParseUSDCAllowZero(c.ReserveFloorUSDC)
	if err != nil {
		return Policy{}, err
	}
	target, err := ParseUSDCAllowZero(c.ReserveTargetUSDC)
	if err != nil {
		return Policy{}, err
	}
	if now.IsZero() {
		if c.Clock != "" {
			parsed, err := time.Parse(time.RFC3339, c.Clock)
			if err != nil {
				return Policy{}, err
			}
			now = parsed
		} else {
			now = time.Now().UTC()
		}
	}
	horizon := c.DueHorizon
	if horizon == 0 {
		horizon = 7 * 24 * time.Hour
	}
	agentID := c.AgentID
	if agentID == "" {
		agentID = "pulse-operator"
	}
	var maxSpend *big.Int
	if strings.TrimSpace(c.MaxSpendPerRunUSDC) != "" {
		maxSpend, err = ParseUSDCAllowZero(c.MaxSpendPerRunUSDC)
		if err != nil {
			return Policy{}, fmt.Errorf("maxSpendPerRunUSDC: %w", err)
		}
	}
	return Policy{
		AgentID:        agentID,
		ChainID:        c.ChainID,
		Vault:          NormalizeAddress(c.Vault),
		ReserveFloor:   floor,
		ReserveTarget:  target,
		Horizon:        horizon,
		Cooldown:       c.Cooldown,
		Now:            now.UTC(),
		MaxSpendPerRun: maxSpend,
	}, nil
}

// ExecutorProduct 返回这一轮执行器对应的审计标签。
func (c Config) ExecutorProduct() string {
	switch c.Executor {
	case "circle-wallets":
		return ProductWallets
	case "circle-agent":
		return ProductAgent
	default:
		return ProductLocalKey
	}
}

// BlockchainForChain 把 Arc chain id 映射成 Circle 的区块链名。
func BlockchainForChain(chainID string) string {
	if chainID == "5042" {
		return "ARC"
	}
	return "ARC-TESTNET"
}

// BlockchainForBase 是 x402 签名要用的 Base 链名。不要拿 Arc 钱包去签。
func BlockchainForBase(chainID, mode string) string {
	if chainID == "8453" || mode == "mainnet" {
		return "BASE"
	}
	return "BASE-SEPOLIA"
}

// ForwardCCTP 在没有打开标准转账回退时使用 Forwarding Service。
// 只写 allowStandard: true 且 forward: false 才改走自助 receiveMessage。
func (c Config) ForwardCCTP() bool {
	if c.CCTP.AllowStandard && !c.CCTP.Forward {
		return false
	}
	return true
}
