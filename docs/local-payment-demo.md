# Local payment operator (no public-chain transactions)

`agent/cmd/operator` is a runnable **deterministic invoice pipeline**, not a
live AI agent. It accepts an explicit invoice, validates six-decimal USDC amounts,
creates a stable scoped payment identity, and encodes `PolicyVault.pay`.
The contract is the authoritative budget/allowlist/cap/approval gate.
The CLI does not load `.env`, read `PRIVATE_KEY`, access a wallet, call a model,
use the inherited Monad stamp client, or automatically approve requests.

## Default: Arc ABI dry-run, no network

```bash
cd agent
cat > /tmp/pulse-invoice.json <<'JSON'
{"payment_id":"infra-2026-09-invoice-001","category":"infra","payee":"0x0000000000000000000000000000000000000003","amount_usdc":"0.50"}
JSON
go run ./cmd/operator pay --request /tmp/pulse-invoice.json \
  --vault 0x0000000000000000000000000000000000000001 \
  --from 0x0000000000000000000000000000000000000002
```

Default identity chain ID: Arc testnet `5042002`; amount `0.50` encodes **500000**
ERC-20 units. Output says `abi_dry_run_no_network` and `no live AI`. It shows the
identity, typed intent and ABI calldata. An RPC flag alone never enables sending.

## End-to-end local chain

Install Go 1.22+, Python 3 and Foundry (`forge`, `anvil`, `cast` on PATH), then:

```bash
python3 scripts/local-demo.py
# optional tooling paths / already-built CLI:
python3 scripts/local-demo.py --foundry-bin /path/to/foundry/bin \
  --operator-bin /path/to/operator --output /tmp/pulse-local-results.json
```

The script builds, starts a silent **Anvil chain 31337 on a random loopback port**,
deploys local MockERC20 and PolicyVault, and uses Anvil's unlocked development
accounts. It never reads, saves or prints private keys, forwards model/wallet
credentials or connects to a public RPC. It closes the node and removes its
isolated temporary data directory afterward.

Assertions (real local EVM, not an in-memory payment stub):

1. Deposit 5.00 mock USDC; budget 2.00; per-payment cap 1.00; allowlist the payee.
2. A separate CLI process pays invoice 001 for 0.50: payee balance 500000 units.
3. Restart the CLI: recover from `decisionUsed` + `AgentPaid`, with no new send.
4. Delete the journal and restart: chain event recovery still prevents double pay.
5. Reuse that ID with 0.75 after journal loss: reject conflicting on-chain terms.
6. Invoice 002 for 1.50 exceeds cap: create approval request 1, no transfer.
7. Restart: requestCount stays 1, balance stays 500000.
8. The demo's explicit **human-owner fixture**, not the CLI agent, approves locally.
9. Balance becomes 2000000; repeating the invoice after approval sends nothing.

Sending from the CLI requires **both** `--send-local` and `--chain-id 31337`,
plus `--rpc http://127.0.0.1:PORT` (or numeric IPv6 loopback). The client checks
actual chain ID before writes and contract code before calls; it rejects public
RPCs, hostname endpoints, proxies and HTTP redirects. This release intentionally
has **no public Arc send mode**. Its Arc support is PolicyVault ABI dry-run;
Arc's actual USDC precompile, wallet signing and real model integrations remain
unverified. Mock USDC is not money; a local Anvil receipt is not an Arc receipt.

## Payment identity and restart recovery

- Use the external invoice/payment ID consistently across processes and retries.
- `decisionHash = keccak256(JSON(domain, chain ID, vault, payment ID))`.
  It is a **payment identity**, not a hash of a real model's reasoning. Run IDs,
  timestamps, agent key rotations and mutable AI output cannot create a new identity
  for a retry. The executing agent is retained in the journal, not the identity.
- Payment terms are in the durable journal and the contract events. Changing
  terms under the same ID is refused locally; after journal loss, consumed-ID
  event recovery also checks category/payee/amount before returning the result.
- Preserve `--data-dir` and `--from-block` (deployment block; default 0). Each
  run takes an OS process lock; each journal write uses fsync + atomic rename.
- Stages: submitting → submitted (known tx hash) → complete. Recovery always
  checks the original chain first. A mined decision wins over an interrupted
  local journal; a known pending tx is waited for without sending again.
- An unknown send outcome fails closed; it does not regenerate a payment ID or
  automatically resend. Reconcile the original local chain/event first. A chain
  reset with a completed journal is refused; use a fresh data directory only for
  a deliberately fresh local experiment. Never change invoice ID to retry.
- `approval_requested` describes the original agent decision and preserves the
  request ID, even if the human has since approved it. It does not claim the
  request is still pending.

The preserved baseline `internal/jev`, trading risk and Monad stamp packages are
not connected to this invoice CLI. Real TypeSafe/Kronos or other AI credentials
are not needed for this demo; any future integration must keep the same stable
payment identity and the contract's hard limits.

API references: [go-ethereum v1.14.13 ABI](https://pkg.go.dev/github.com/ethereum/go-ethereum@v1.14.13/accounts/abi),
[RPC CallContext](https://pkg.go.dev/github.com/ethereum/go-ethereum@v1.14.13/rpc#Client.CallContext),
[Go file Sync](https://pkg.go.dev/os#File.Sync).
