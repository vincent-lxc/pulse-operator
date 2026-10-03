// 本文件读取运行配置。私钥和 RPC 只出现环境变量名或 0600 文件路径，不写入配置值。
package treasury

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Config 是 dry-run 与 live 共用的配置。
type Config struct {
	Mode              string        `yaml:"mode"`
	AgentID           string        `yaml:"agentID"`
	ChainID           string        `yaml:"chainID"`
	Vault             string        `yaml:"vault"`
	Agent             string        `yaml:"agent"`
	USDC              string        `yaml:"usdc"`
	ChainDriver       string        `yaml:"chainDriver"`
	VaultFixture      string        `yaml:"vaultFixture"`
	Ledger            string        `yaml:"ledger"`
	AuditLog          string        `yaml:"auditLog"`
	FromBlock         uint64        `yaml:"fromBlock"`
	DueHorizon        time.Duration `yaml:"dueHorizon"`
	Cooldown          time.Duration `yaml:"cooldown"`
	ReserveFloorUSDC  string        `yaml:"reserveFloorUSDC"`
	ReserveTargetUSDC string        `yaml:"reserveTargetUSDC"`
	Clock             string        `yaml:"clock"`
	LoopInterval      time.Duration `yaml:"loopInterval"`
	LLM               LLMConfig     `yaml:"llm"`
	Notify            NotifyConfig  `yaml:"notify"`
	Secrets           SecretConfig  `yaml:"secrets"`
	Gas               GasConfig     `yaml:"gas"`
}

// LLMConfig 控制可选软判断，默认关闭。
type LLMConfig struct {
	Enabled bool   `yaml:"enabled"`
	URLEnv  string `yaml:"urlEnv"`
}

// NotifyConfig 选择通知实现。
type NotifyConfig struct {
	Driver string `yaml:"driver"`
}

// SecretConfig 只保存环境变量名和文件路径。
type SecretConfig struct {
	RPCEnv       string `yaml:"rpcEnv"`
	AgentKeyEnv  string `yaml:"agentKeyEnv"`
	AgentKeyFile string `yaml:"agentKeyFile"`
	OwnerKeyEnv  string `yaml:"ownerKeyEnv"`
	OwnerKeyFile string `yaml:"ownerKeyFile"`
}

// GasConfig 是 Arc 上以 USDC 支付的 gas 上限，单位 gwei。
type GasConfig struct {
	MaxFeePerGasGwei         int64 `yaml:"maxFeePerGasGwei"`
	MaxPriorityFeePerGasGwei int64 `yaml:"maxPriorityFeePerGasGwei"`
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
	if cfg.FromBlock == 0 {
		cfg.FromBlock = 64231944
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// Validate 检查模式、储备和 gas 下限。
func (c Config) Validate() error {
	if c.Mode != "dry-run" && c.Mode != "live" {
		return fmt.Errorf("mode must be dry-run or live")
	}
	if c.ChainDriver != "mock" && c.ChainDriver != "rpc" {
		return fmt.Errorf("chainDriver must be mock or rpc")
	}
	if c.Mode == "live" && c.ChainDriver != "rpc" {
		return fmt.Errorf("live mode requires chainDriver rpc")
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
	return Policy{
		AgentID:       agentID,
		ChainID:       c.ChainID,
		Vault:         NormalizeAddress(c.Vault),
		ReserveFloor:  floor,
		ReserveTarget: target,
		Horizon:       horizon,
		Cooldown:      c.Cooldown,
		Now:           now.UTC(),
	}, nil
}
