package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDryRunWithoutNetworkOrKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "invoice.json")
	os.WriteFile(path, []byte(`{"payment_id":"api-2026-09","category":"infra","payee":"0x0000000000000000000000000000000000000003","amount_usdc":"0.5"}`), 0600)
	// An invalid ambient key must be irrelevant: the CLI never reads it.
	t.Setenv("PRIVATE_KEY", "not-a-key")
	args := []string{"pay", "--request", path, "--vault", "0x0000000000000000000000000000000000000001", "--from", "0x0000000000000000000000000000000000000002", "--rpc", "https://rpc.testnet.arc.io"}
	var out bytes.Buffer
	if err := run(args, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "abi_dry_run_no_network") || !strings.Contains(out.String(), "500000") || !strings.Contains(out.String(), "no live AI") {
		t.Fatal(out.String())
	}
	if err := run(append(args, "--send-local"), &out); err == nil {
		t.Fatal("public-chain write was permitted")
	}
}
