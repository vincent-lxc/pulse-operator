// operator is a deterministic payment CLI. It never loads .env or private keys.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/ethereum/go-ethereum/common"
	"github.com/vincent-lxc/pulse-on-monad/agent/internal/payment"
	"io"
	"math/big"
	"os"
	"time"
)

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "operator:", err)
		os.Exit(1)
	}
}
func run(args []string, out io.Writer) error {
	flags := flag.NewFlagSet("operator pay", flag.ContinueOnError)
	flags.SetOutput(out)
	request := flags.String("request", "", "JSON invoice: payment_id, category, payee, amount_usdc")
	vault := flags.String("vault", "", "PolicyVault address")
	agent := flags.String("from", "", "operator address (unlocked Anvil account for send-local)")
	chain := flags.String("chain-id", "5042002", "identity scope; Arc testnet ABI dry-run by default")
	endpoint := flags.String("rpc", "", "loopback RPC (required only for send-local)")
	dir := flags.String("data-dir", "./data/payments", "durable local payment journal")
	send := flags.Bool("send-local", false, "send only to numeric loopback RPC on chain 31337")
	fromBlock := flags.Uint64("from-block", 0, "vault deployment block for recovery logs")
	flags.Usage = func() {
		fmt.Fprintln(out, "Usage: operator pay --request invoice.json --vault 0x... --from 0x... [--chain-id 31337 --rpc http://127.0.0.1:PORT --send-local]")
		flags.PrintDefaults()
	}
	if len(args) == 0 || args[0] == "help" {
		flags.Usage()
		return nil
	}
	if args[0] != "pay" {
		return fmt.Errorf("expected pay or help")
	}
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected positional arguments")
	}
	if !common.IsHexAddress(*vault) || !common.IsHexAddress(*agent) {
		return fmt.Errorf("--vault and --from must be addresses")
	}
	id, ok := new(big.Int).SetString(*chain, 10)
	if !ok {
		return fmt.Errorf("invalid chain-id")
	}
	b, err := os.ReadFile(*request)
	if err != nil {
		return err
	}
	var req payment.Request
	if err := json.Unmarshal(b, &req); err != nil {
		return err
	}
	in, err := payment.Prepare(req, id, common.HexToAddress(*vault), common.HexToAddress(*agent))
	if err != nil {
		return err
	}
	var result *payment.Result
	if !*send {
		result, err = payment.DryRun(in)
	} else {
		if id.Cmp(big.NewInt(31337)) != 0 {
			return fmt.Errorf("send-local only permits chain-id 31337")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		client, e := payment.DialLocal(ctx, *endpoint, *fromBlock)
		if e != nil {
			return e
		}
		defer client.Close()
		result, err = payment.Execute(ctx, *dir, in, client)
	}
	if err != nil {
		return err
	}
	return json.NewEncoder(out).Encode(struct {
		Mode    string          `json:"mode"`
		Planner string          `json:"planner"`
		Intent  payment.Intent  `json:"intent"`
		Result  *payment.Result `json:"result"`
	}{mode(*send), "deterministic invoice; no live AI", in, result})
}
func mode(send bool) string {
	if send {
		return "local_mock_usdc"
	}
	return "abi_dry_run_no_network"
}
