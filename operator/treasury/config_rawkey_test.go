package treasury

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMainnetIrisAndExecutor(t *testing.T) {
	root := moduleRoot(t)
	cfg, err := LoadConfig(filepath.Join(root, "config", "mainnet.example.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Executor != "raw-key" || cfg.IrisAPI() != "https://iris-api.circle.com" || cfg.CCTP.PollTimeout != 3*time.Minute {
		t.Fatalf("executor %s iris %s poll %s", cfg.Executor, cfg.IrisAPI(), cfg.CCTP.PollTimeout)
	}
	if strings.Contains(string(mustRead(t, filepath.Join(root, "config", "mainnet.example.yaml"))), "CIRCLE_API_KEY") {
		t.Fatal("mainnet example still names a Circle API key")
	}

	testnet, err := LoadConfig(filepath.Join(root, "config", "testnet.example.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if testnet.IrisAPI() != "https://iris-api-sandbox.circle.com" {
		t.Fatal(testnet.IrisAPI())
	}

	dir := t.TempDir()
	base := "mode: mainnet\nchainID: \"5042\"\nchainDriver: rpc\nvault: \"0x1111111111111111111111111111111111111111\"\nagent: \"0x2222222222222222222222222222222222222222\"\nreserveFloorUSDC: \"0\"\nreserveTargetUSDC: \"0\"\n"
	sandbox := filepath.Join(dir, "sandbox.yaml")
	if err := os.WriteFile(sandbox, []byte(base+"executor: raw-key\ncircle:\n  irisBase: https://iris-api-sandbox.circle.com\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(sandbox); err == nil || !strings.Contains(err.Error(), "sandbox") {
		t.Fatalf("got %v", err)
	}
	alias := filepath.Join(dir, "alias.yaml")
	if err := os.WriteFile(alias, []byte(base+"executor: raw-key\ncircle:\n  irisBase: https://iris-api-sandbox.circle.com\ncctpBridge:\n  irisBase: https://iris-api.circle.com\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	aliased, err := LoadConfig(alias)
	if err != nil || aliased.IrisAPI() != "https://iris-api.circle.com" {
		t.Fatalf("%v %s", err, aliased.IrisAPI())
	}
	circleExec := filepath.Join(dir, "circle.yaml")
	if err := os.WriteFile(circleExec, []byte(base+"executor: circle-wallets\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(circleExec); err == nil || !strings.Contains(err.Error(), "circle-wallets") {
		t.Fatalf("got %v", err)
	}
	liveMainnet := filepath.Join(dir, "live-mainnet.yaml")
	if err := os.WriteFile(liveMainnet, []byte(strings.Replace(base, "mode: mainnet", "mode: live", 1)+"executor: raw-key\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(liveMainnet); err == nil || !strings.Contains(err.Error(), "5042") {
		t.Fatalf("got %v", err)
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
