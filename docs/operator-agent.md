# Operator agent

The treasury loop lives in `operator/` and is a [digitalwayhk/core](https://github.com/digitalwayhk/core) service. PolicyVault stays the authority. The agent only proposes a `decisionHash` and calls `pay` or `sweepToReserve`. Hard limits in Go run first. An optional LLM note cannot change a hard decision, and it is off unless `llm.enabled` is true.

## Loop and the Tameion flow

| Step | What this agent does |
| --- | --- |
| Revenue in | Read USDC `Transfer` logs to the vault (RPC driver) and accept `POST /api/operator/recordrevenue`. The dry-run fixture includes one inflow. |
| Liquidity | Compare balance, due-but-unpaid obligations inside `dueHorizon`, reserve floor, and reserve target. Category budget left is read from the vault snapshot. |
| Decide | For each open payable: `pay`, `defer`, `escalate_to_human`, then one cycle-level `sweep_to_reserve` when surplus remains. Reason codes are stable strings (`within_policy`, `cooldown`, `not_due`, `reserve_floor`, `over_tx_cap`, `over_budget`, `payee_not_allowlisted`, `surplus_above_target`). |
| Execute | `pay(category, payee, amount, decisionHash)` with the agent key. Over cap or over budget still submits `pay` so the vault opens an `ApprovalRequest`. A payee that is not allowlisted is not submitted, because that call reverts. `sweepToReserve` is `onlyOwner` in the contract, so live mode sends it only when `OWNER_PRIVATE_KEY` (or a 0600 file) is set; otherwise the outcome is `owner_key_required`. |
| Record | Append-only `data/audit.jsonl` (observation, liquidity, decision, execution). The same rows are stored with core models (`DecisionRecord`, `Payable`, `Revenue`, `Approval`, `SpendCategory`, `CycleSnapshot`) so `-view` can list them. |
| Human above the line | Pending approvals are listed on the dashboard and in Approvals. `notify.driver: telegram` is a stub: it checks `TELEGRAM_BOT_TOKEN` and `TELEGRAM_CHAT_ID`, then refuses to send. `driver: log` records the notice in the run output. |

`decisionHash` is keccak256 of canonical JSON (`v`, `agent_id`, `chain_id`, `vault`, `payable_id`, `action`, `category`, `payee`, `amount_units`, `reason_code`). The hash does not include the tx hash or the outcome, so a retry of the same decision hits `decisionUsed` instead of paying twice.

## Layout

```text
operator/
  contract/          service name "operator"
  models/            core models, SQLite via the framework
  treasury/          rules, ledger import, mock chain, live RPC client
  business/          one cycle: import, decide, persist
  api/public/        GET dashboard, POST recordrevenue, POST runonce
  api/manage/        read-only admin lists
  cmd/pulse/         server, or `demo`
  config/dry-run.yaml
  testdata/          sample ledger and vault snapshot
```

Core owns HTTP, gRPC, SQLite, and the admin UI. This repo does not add another web framework. Chain calls use go-ethereum because core does not speak ABI.

SQLite and `etc/` are created beside the **executable**, not the working directory. `go run` uses a throwaway binary path, so the screen-record flow builds `operator/bin/pulse` and reuses it. The audit JSONL is relative to the working directory (`operator/data/audit.jsonl`).

## Dry-run

Requires Go 1.26.6 or newer (the core module's `go` line). From the repo root:

```bash
scripts/operator-dry-run.sh
```

Or, after `cd operator`:

```bash
go test ./...
go run ./cmd/pulse demo
```

`go run` prints the same decisions. The admin database for that process is not the one `bin/pulse` uses.

Expected lines from `testdata/ledger.yaml` at clock `2026-10-03T12:00:00Z`, vault balance 20 USDC, floor 8, target 10:

| Payable | Action | Why |
| --- | --- | --- |
| cloud-oct 2.00 | `pay` | Allowlisted, inside cap, budget, and floor. |
| cloud-extra 0.50 | `defer` | Same payee, cooldown 1h. |
| contractor-bonus 3.00 | `escalate_to_human` | Over the 1 USDC per-tx cap. Mock `pay` returns a pending approval. |
| unknown-vendor 0.10 | `escalate_to_human` | Payee not allowlisted. No transaction. |
| conference 1.00 | `defer` | Due 2026-11-15, outside the 7 day horizon. |
| cycle 4.40 | `sweep_to_reserve` | Balance after the pay, minus unpaid dues and the 10 USDC target. |

No chain write happens. `chainDriver: mock` is the offline snapshot. `chainDriver: rpc` with `mode: dry-run` reads Arc and `eth_call`s `pay`, and does not send.

## Admin view and HTTP

Build once, demo, then serve:

```bash
cd operator
go build -o bin/pulse ./cmd/pulse
./bin/pulse demo
./bin/pulse -view 43123 -p 18091 -grpc 19091
```

| Surface | URL |
| --- | --- |
| Admin UI | http://127.0.0.1:43123 |
| Framework `server` service | http://127.0.0.1:18091 |
| Operator API | http://127.0.0.1:18092 |

`-p` is the base port. Core gives DataCenterID 1 to its built-in `server` service, so that process listens on 18091. This service is registered second and listens on 18092 (`base + DataCenterID - 1`). The view process proxies `/api/*`, so the same dashboard URL also works on port 43123. gRPC follows the same split: pass `-grpc 19091` and the operator gRPC port is 19092.

In the admin UI open menu management, run **更新菜单**, then open Decisions, Payables, Approvals, Revenue, Categories, Cycles. Manage routes are view and search only. The local view signs a TestToken for `platform-admin` by itself.

```bash
curl -s http://127.0.0.1:18092/api/operator/dashboard
curl -s http://127.0.0.1:43123/api/operator/dashboard
curl -s -X POST http://127.0.0.1:18092/api/operator/recordrevenue \
  -H 'Content-Type: application/json' \
  -d '{"ref":"wire-1","from":"0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","amount_usdc":"1.00","memo":"manual"}'
curl -s -X POST http://127.0.0.1:18092/api/operator/runonce
```

`recordrevenue` is public on purpose for the local demo. Do not expose the process to the internet. `TestToken` on `/api/servermanage/*` is local-only; still keep it off a public interface.

CSV ledgers use the header `id,category,payee,amount_usdc,due,memo`. YAML is the shape in `testdata/ledger.yaml`.

## Live Arc testnet

There is no key in this repo. Gas on Arc is USDC. Keep `maxFeePerGas` at or above 20 gwei; the sample uses 50. Chain id **5042002**. Vault `0x4FACE6592Ba1AdF83E35B01CcD93D8704d647C01`. USDC `0x3600000000000000000000000000000000000000` (6 decimals). Mainnet chain id 5042 is refused unless `CONFIRM_MAINNET=1`.

```bash
cd operator
cp config/live.example.yaml config/live.yaml   # gitignored if you prefer; do not commit keys
umask 077
# either export the hex key, or point agentKeyFile at a 0600 file
export ARC_RPC_URL=https://rpc.testnet.arc.io
export OPERATOR_PRIVATE_KEY=0x...   # agent key, the only key that may call pay
# optional, required for an actual sweepToReserve:
export OWNER_PRIVATE_KEY=0x...
# a key file is accepted only when its mode is 0600 or stricter (no group/other bits)
go build -o bin/pulse ./cmd/pulse
./bin/pulse demo -config config/live.yaml
# or leave it up and POST /api/operator/runonce
OPERATOR_CONFIG=config/live.yaml ./bin/pulse -view 43123 -p 18091 -grpc 19091
```

`OPERATOR_PRIVATE_KEY` wins over `agentKeyFile`. The same rule applies to the owner key. The process never prints the key. LLM stays off. Set `llm.enabled: true` and `OPERATOR_LLM_URL` only if you accept that the current client is a stub which attaches a note and still does not override the hard action.

Replace `testdata/ledger.yaml` with the payables you actually want considered. The on-chain category name must already exist (`setCategory` / `setPayee` are owner actions, not this agent's).

## Tests

```bash
cd operator && go test ./... && go vet ./...
cd agent && go test ./...
cd contracts && forge test
```

Run the two Go modules separately. There is no root `go.work`: the payment CLI and the operator pull incompatible `google.golang.org/genproto` graphs, and a workspace merges them into an ambiguous import. Operator tests use the mock chain and a temporary SQLite directory. They do not dial Arc.

## Screen recording (under three minutes)

1. Terminal: `scripts/operator-dry-run.sh`. Pause on the six `decision` lines: pay, defer (cooldown), escalate (over cap, simulated approval), escalate (not allowlisted, no tx), defer (not due), sweep.
2. `cd operator && ./bin/pulse -view 43123 -p 18091 -grpc 19091`. Open http://127.0.0.1:43123, update the menu, open Decisions and Approvals.
3. Second terminal: `curl -s http://127.0.0.1:18092/api/operator/dashboard`.
4. One sentence on camera: live mode is the same binary with `config/live.yaml`, `ARC_RPC_URL`, and `OPERATOR_PRIVATE_KEY`. Sweep needs the owner key because the contract says `onlyOwner`.

## What is stubbed

- Telegram delivery.
- LLM call. The hook exists; the default advisor is a no-op; a configured URL still cannot flip the action.
- Owner `approve` / `reject`. The agent lists pending requests. The human sends those transactions.
- Background loop. `loopInterval` is parsed and left unused so a server does not pay on a timer until that is turned on deliberately. Use `demo` or `POST /api/operator/runonce`.
