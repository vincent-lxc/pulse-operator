# Operator agent

The treasury loop lives in `operator/` and is a [digitalwayhk/core](https://github.com/digitalwayhk/core) service. PolicyVault stays the authority. The agent only proposes a `decisionHash` and calls `pay` or `sweepToReserve`. Hard limits in Go run first. An optional LLM note cannot change a hard decision, and it is off unless `llm.enabled` is true.

## Loop and the Tameion flow

| Step | What this agent does |
| --- | --- |
| Revenue in | Read USDC `Transfer` logs (`local:rpc`), confirm CCTP v2 burns with Iris (`circle:cctp`), accept Gateway webhooks (`circle:gateway`), and accept `POST /api/operator/recordrevenue` (`local:http`). |
| Liquidity | Compare balance, due-but-unpaid obligations inside `dueHorizon`, reserve floor, and reserve target. Category budget left is read from the vault snapshot. |
| Decide | For each open payable: `pay`, `defer`, `escalate_to_human`, then one cycle-level `sweep_to_reserve` when surplus remains. Reason codes are stable strings (`within_policy`, `cooldown`, `not_due`, `reserve_floor`, `over_tx_cap`, `over_budget`, `payee_not_allowlisted`, `surplus_above_target`). |
| Execute | `pay(category, payee, amount, decisionHash)`. `executor: raw-key` signs with go-ethereum. `executor: circle-wallets` sends the same call through Circle Developer-Controlled Wallets (`feeLevel: MEDIUM` by default; optional gwei `maxFee` + `priorityFee` + `gasLimit`). `executor: circle-agent` shells out to `circle wallet execute`. Over cap or over budget still submits `pay` so the vault opens an `ApprovalRequest`. A payee that is not allowlisted is not submitted. `sweepToReserve` is `onlyOwner`: raw-key needs `OWNER_PRIVATE_KEY`; Circle wallets need `CIRCLE_OWNER_WALLET_ID`; the agent CLI needs `CIRCLE_OWNER_ADDRESS`. Missing that signer records `owner_key_required`. |
| Record | Append-only `data/audit.jsonl`. Every line has `circle` (`circle:wallets`, `circle:agent`, `circle:cctp`, `circle:gateway`, `local:key`, or `local:rpc`). The same tag is a column on Decisions, Approvals, Revenue, and Cycles. |
| Human above the line | Pending approvals are listed on the dashboard and in Treasury → Approvals. That view has Approve and Reject commands, and `POST /api/operator/approve` / `reject` call `PolicyVault.approve` / `reject` with the owner key or the Circle owner wallet. `notify.driver: telegram` sends when `TELEGRAM_BOT_TOKEN` and `TELEGRAM_CHAT_ID` are set (env or a 0600 file); otherwise it does nothing. `driver: log` records the notice in the run output. The notice includes the request id and, when a tx exists, an Arcscan link. |

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
| cloud-oct 2.00 | `pay` | Allowlisted, inside cap, budget, and floor. Tagged `circle:wallets`. |
| cloud-extra 0.50 | `defer` | Same payee, cooldown 1h. |
| contractor-bonus 3.00 | `escalate_to_human` | Over the 1 USDC per-tx cap. Mock `pay` returns a pending approval. |
| unknown-vendor 0.10 | `escalate_to_human` | Payee not allowlisted. No transaction. |
| conference 1.00 | `defer` | Due 2026-11-15, outside the 7 day horizon. |
| cycle 4.40 | `sweep_to_reserve` | Balance after the pay, minus unpaid dues and the 10 USDC target. Tagged `circle:wallets`. |

The fixture also prints two inflows that are already inside the 20 USDC balance: 4.00 `circle:cctp` and 1.00 `circle:gateway`. Dry-run does not call Iris or Circle.

No chain write happens. `chainDriver: mock` is the offline snapshot. A later mock run continues from `data/mock-state.json` (balance, budgets, pending approvals) so it does not sweep the same surplus again. `chainDriver: rpc` with `mode: dry-run` reads Arc and `eth_call`s `pay`. A simulated pay (`dry_run_paid`) updates the in-run balance and cooldown so the sweep amount matches a live run, and it does not mark the payable paid. An `eth_call` revert is stored on the decision. The `run` and `liquidity` lines are the observation before execution.

`paid` and `closed` payables stay out of the obligation total. An `escalated` payable still counts until approve marks it `paid` (and stores the tx hash) or reject marks it `closed`. A dry-run approve or reject does not change the payable.

USDC `Transfer` logs are read in chunks of `logChunk` (default 9000). The first scan is the last `logLookback` blocks unless `fullLogScan: true`. `data/log-cursor.json` stores the last scanned block. Public `https://rpc.testnet.arc.io` often returns HTTP 429; RPC calls retry with backoff. Fallback endpoint: `https://rpc.blockdaemon.testnet.arc.io`. On-chain inflows are tagged `local:rpc`.

The API and gRPC processes bind to `listen` (default `127.0.0.1`). `OPERATOR_BIND` overrides it. Loopback binds also set the framework local-visit check. Core's admin view (`-view`) still listens on `:<port>` on every interface; that address is hardcoded in the framework.

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

`-p` is the base port. Core gives DataCenterID 1 to its built-in `server` service, so that process listens on 18091. This service is registered second and listens on 18092 (`base + DataCenterID - 1`). The view process proxies `/api/*`, so the same dashboard URL also works on port 43123. gRPC follows the same split: pass `-grpc 19091` and the operator gRPC port is 19092. HTTP and gRPC bind to 127.0.0.1 unless `OPERATOR_BIND` or `listen` says otherwise.

In the admin UI open menu management, run **更新菜单**, then open Decisions, Payables, Approvals, Revenue, Categories, Cycles. Manage routes are view and search only, except Approvals, which also has Approve and Reject. The local view signs a TestToken for `platform-admin` by itself.

Decisions and Approvals include `circle_tx_id` and `circle_state` when Circle submitted the transaction. The browser document title stays **BitZoom Exchange Admin**. Core embeds that string in the admin frontend and `IService` has no title setting, so this service cannot rename it. The sidebar name is still 金库 / Treasury.

```bash
curl -s http://127.0.0.1:18092/api/operator/dashboard
curl -s http://127.0.0.1:43123/api/operator/dashboard
curl -s -X POST http://127.0.0.1:18092/api/operator/recordrevenue \
  -H 'Content-Type: application/json' \
  -d '{"ref":"wire-1","from":"0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","amount_usdc":"1.00","memo":"manual"}'
curl -s -X POST http://127.0.0.1:18092/api/operator/runonce
curl -s -X POST http://127.0.0.1:18092/api/operator/approve \
  -H 'Content-Type: application/json' \
  -d '{"request_id":"1"}'
curl -s -X POST http://127.0.0.1:18092/api/operator/reject \
  -H 'Content-Type: application/json' \
  -d '{"request_id":"1"}'
```

Approve and Reject also appear on Treasury → Approvals after **更新菜单**. Select the row, then run the command. Those manage routes need the admin session. The public routes above follow the same rule as `runonce`: keep the process on loopback.

Approving a request that is no longer pending returns HTTP 422 with the decoded custom error, for example `RequestNotPending(4)` or `DecisionAlreadyUsed(0x…)`. The audit line stores that reason. A transport failure is still HTTP 500.

Each run copies `pendingRequestIds()` into Approvals. Loading the dashboard does the same read, without scanning logs. A request opened by another signer shows up there. A row that is already approved or rejected is not moved back to pending. The dashboard `circle` field comes from the current `executor` (`circle:wallets`, `circle:agent`, or `local:key`). If the config file cannot be read, the last cycle's value is kept.

Iris returns 404 with its message when a burn is unknown (`POST /api/operator/cctpin`), instead of an empty 500.

`recordrevenue` is public on purpose for the local demo. Do not expose the process to the internet. `TestToken` on `/api/servermanage/*` is local-only; still keep it off a public interface.

CSV ledgers use the header `id,category,payee,amount_usdc,due,memo`. YAML is the shape in `testdata/ledger.yaml`.

## Circle integration

PolicyVault is still the spending authority. Circle is the signer and the inbound USDC rail. The dry-run config sets `executor: circle-wallets` so the demo tags every decision, and it does that with no API key. Live mode is the same code path with credentials.

| RFB step | Circle product | What is live |
| --- | --- | --- |
| Execute `pay` / `sweepToReserve` / `approve` / `reject` | Developer-Controlled Wallets (`circle:wallets`) | `POST /v1/w3s/developer/transactions/contractExecution` on `https://api.circle.com`. Entity secret ciphertext is RSA-OAEP SHA-256, re-encrypted every request. The body sends `walletId`, `blockchain`, and `feeLevel` (`MEDIUM` unless `circle.feeLevel` says otherwise). It does not send `gasPrice`. Set `circle.explicitGas: true` to send gwei `maxFee`, gwei `priorityFee`, and `gasLimit` instead. Status is `GET /v1/w3s/transactions/{id}` until `data.transaction.state` is `COMPLETE`. `FAILED`, `CANCELLED`, and `DENIED` fail with Circle's reason. |
| Execute `pay` / `sweepToReserve` | Agent Wallet CLI (`circle:agent`) | `circle wallet execute "pay(bytes32,address,uint256,bytes32)" ... --contract <vault> --address <agent> --chain ARC-TESTNET --output json`. Install `@circle-fin/cli` (`circle`). The operator does not pass a private key to the CLI. |
| Spending limits | Agent Wallet, mainnet only | `circle wallet limit` rejects testnet, including `ARC-TESTNET`. On `ARC` (mainnet) a human sets caps with `circle wallet limit set --address <agent> --chain ARC --policy-type stablecoin --per-tx ... --daily ... --weekly ... --monthly ...` and confirms the email OTP. The operator records that command in the audit `circle_limits` field and does not submit the OTP. |
| Revenue in | CCTP v2 (`circle:cctp`) | Arc domain is **26**. Iris is `GET /v2/messages/{sourceDomain}?transactionHash=`. Testnet host `https://iris-api-sandbox.circle.com`, mainnet `https://iris-api.circle.com`. Iris cannot list "everything minted to the vault"; it needs the source-chain burn hash. Live mode polls `circle.cctp.burns`. `POST /api/operator/cctpin` with `{"source_domain":"0","tx_hash":"0x..."}` fetches Iris and stores an unreconciled inflow. The next cycle adds it unless that tx hash is already in the USDC transfer log. |
| Revenue in | Gateway (`circle:gateway`) | `POST /api/operator/gatewayhook` accepts `gateway.deposit.finalized`, `gateway.mint.finalized`, and `gateway.mint.forwarded` when `domain` is 26 and the wallet is the vault. Live mode verifies `X-Circle-Signature` (ECDSA-SHA256 over the raw body) with `GET /v2/notifications/publicKey/{keyId}`. Dry-run accepts an unsigned body so the local demo can show the tag. Subscribe with `POST /v2/notifications/subscriptions/permissionless` (`domains: ["26"]`, `notificationTypes: ["gateway.*"]`). The endpoint must be public HTTPS; this process does not create the subscription. |
| Execute fallback | go-ethereum (`local:key`) | `executor: raw-key`. `OPERATOR_PRIVATE_KEY` / `OWNER_PRIVATE_KEY`. |

### Live with Developer-Controlled Wallets

```bash
cd operator
cp config/live.example.yaml config/live.yaml
# in config/live.yaml set executor: circle-wallets and chainDriver: rpc
umask 077
export ARC_RPC_URL=https://rpc.testnet.arc.io
export CIRCLE_API_KEY=...                 # Console → Keys → API key. Testnet key for ARC-TESTNET.
export CIRCLE_ENTITY_SECRET=...           # 64 hex chars. Or CIRCLE_ENTITY_SECRET file path in config, mode 0600.
export CIRCLE_WALLET_ID=...               # developer-controlled wallet that is the PolicyVault agent
export CIRCLE_OWNER_WALLET_ID=...         # developer-controlled wallet that is the vault owner; required for sweep, approve, and reject
go build -o bin/pulse ./cmd/pulse
./bin/pulse demo -config config/live.yaml
```

A file path in `apiKeyFile`, `entitySecretFile`, or `walletIDFile` is read only when its mode has no group or other bits. The process never prints those values.

### Live with the Agent Wallet CLI

```bash
# install and authenticate the Circle CLI first (email OTP). Do not import the operator key into a local wallet.
export CIRCLE_AGENT_ADDRESS=0x...         # agent wallet address allowed to call pay
export CIRCLE_OWNER_ADDRESS=0x...         # owner wallet address, required for sweep
# config executor: circle-agent
./bin/pulse demo -config config/live.yaml
```

On Arc testnet the flow runs and spending limits stay off. On Arc mainnet (`blockchain: ARC`, chain id 5042, `CONFIRM_MAINNET=1`) set the policy with the human OTP command above before sending value.

### Confirm a CCTP burn and a Gateway notice

```bash
curl -s -X POST http://127.0.0.1:18092/api/operator/cctpin \
  -H 'Content-Type: application/json' \
  -d '{"source_domain":"0","tx_hash":"0xYOUR_SOURCE_BURN"}'
curl -s -X POST http://127.0.0.1:18092/api/operator/gatewayhook \
  -H 'Content-Type: application/json' \
  -d '{"notificationType":"gateway.mint.finalized","notification":{"walletAddress":"0x4FACE6592Ba1AdF83E35B01CcD93D8704d647C01","domain":"26","tokenAddress":"0x3600000000000000000000000000000000000000","amount":"1.000000","from":"0xdddddddddddddddddddddddddddddddddddddddd","txHash":"0xcccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"}}'
```

The gateway sample is for dry-run. Live requests need `X-Circle-Signature` and `X-Circle-Key-Id`.

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

1. Terminal: `scripts/operator-dry-run.sh`. Pause on `circle=circle:cctp`, `circle=circle:gateway`, then the six `decision` lines. Each decision includes `circle=circle:wallets`: pay, defer (cooldown), escalate (over cap), escalate (not allowlisted), defer (not due), sweep.
2. `cd operator && ./bin/pulse -view 43123 -p 18091 -grpc 19091`. Open http://127.0.0.1:43123, update the menu, open Decisions and Revenue. The Circle column is the product tag.
3. Second terminal: `curl -s http://127.0.0.1:18092/api/operator/dashboard`. The top-level `circle` field is `circle:wallets`.
4. One sentence on camera: dry-run tags Circle and does not call it. Live `executor: circle-wallets` needs `CIRCLE_API_KEY`, `CIRCLE_ENTITY_SECRET`, and `CIRCLE_WALLET_ID`. Agent-wallet spending limits are mainnet-only (`circle wallet limit set --chain ARC`).

## Scheduled loop

`loopEnabled` defaults to false. Set it true together with `loopInterval` (for example `5m`) and `maxSpendPerRunUSDC` (a positive USDC amount). The server then waits one interval and runs the same cycle as `runonce`. A run already in progress is skipped. Autonomous pays that would push the run over `maxSpendPerRunUSDC` are deferred with `max_spend_per_run`. Leave the flag off for the one-shot demo.

## What is stubbed

- LLM call. The hook exists; the default advisor is a no-op; a configured URL still cannot flip the action.
- Creating the Circle webhook subscription. The receiver and the signature check are in this process. Registering the public HTTPS endpoint is a Console / `POST /v2/notifications/subscriptions/permissionless` step.
- Agent-wallet spending-limit changes. Reading the supported chain is in code. Setting a limit needs a human email OTP, and Circle rejects the call on testnet.
- CCTP discovery without a burn transaction hash. Iris looks up one source transaction. It does not stream every mint to the vault. The USDC `Transfer` log still catches the mint after it lands.
- The admin view listen address. API and gRPC honor `listen` / `OPERATOR_BIND`. The framework's HTML server always uses `:<view port>`.
- The admin document title. The embedded frontend is **BitZoom Exchange Admin**. There is no config key for it.
