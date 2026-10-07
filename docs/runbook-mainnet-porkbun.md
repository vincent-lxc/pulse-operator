# Runbook: Porkbun domains paid from Arc mainnet

This runbook is the human checklist. The operator does not deploy, fund, or spend during CI.
Default mode is `dry-run`. A mainnet payment needs every gate below at the same time.

## What the model decides, and what it does not

`planner.driver: rules` (the CI and dry-run default) does not call a model. It records the deterministic action.

`planner.driver: gateway` calls [Vercel AI Gateway](https://vercel.com/docs/ai-gateway) at `https://ai-gateway.vercel.sh/v1/chat/completions` (OpenAI-compatible). The default model is `openai/gpt-5.4-nano`, confirmed on the public `GET /v1/models` list on 2026-10-07 (tool-use and structured outputs). The key is `AI_GATEWAY_API_KEY`. The model returns `action`, `rationale`, `risk_notes`, and `confidence`.

The model may only pick an action the Go rules already allow (`pay`, `defer`, `escalate`, `reject`). It cannot raise a cap, add a payee, or spend past a budget. A disagreement is written to the audit as `planner_disagree`. Invalid JSON, a timeout, or a transport error becomes `escalate` and does **not** submit a transferring `pay`.

A bill whose amount is above the vault balance escalates with `insufficient_balance` and does not call the vault. Dry-run subtracts what this process has already committed to pay from the fixture balance and the domains remaining budget, so a later bill in the same run sees the lower figures. The fixture does not change on disk: a new `bill run` or `bill approve` process starts from those numbers again. On testnet and mainnet, a failed read of the balance, category, payee allowlist, or caps escalates with `observe_failed`. The bill is not treated as enabled, allowlisted, or uncapped, and the vault is not called. A live read subtracts only spend committed in this process that the chain balance does not already include (a vault approval, or a pay that has not transferred). A status of `paid`, `simulated_paid`, `dry_run_paid`, or `circle_confirmed` drops that amount from the pending total, because the next observation already shows the debit.

PolicyVault still enforces the category budget, per-transaction cap, payee allowlist, pause, and one-time `decisionHash` on chain. An in-policy `pay` transfers. An over-cap `pay` only opens an `ApprovalRequest` when the owner set over-limit mode to Escalate.

Optional Jev review (`JEV_API_KEY`, `https://api.typesafe.ai/v1/systemone`, model `jev-latest`) can only narrow a `pay` to `escalate`. A Jev transport error is recorded and does not cancel a payment the rules and the primary model already accepted.

The `decisionHash` is the keccak of canonical JSON that includes the planner driver (`rules`, `gateway`, `error`, or `owner`), the model id, the planner action, the rationale, the prompt hash, the risk notes, the confidence, and whether the model disagreed. That driver is stored on the bill row as `planner`. Latency and the raw response are stored on the bill and the decision row, not inside the hash. After the bill leaves `decided`, the decision row's `outcome` is updated to the bill state (`done`, `escalated`, `closed`, `failed_vault`, and the other progress states).

Prompts are built from the bill, the vault's remaining budget and cap, the balance, pending requests, and recent decisions. Private keys and API keys are redacted before the request is sent.

## Money

Public prices checked for this runbook (registration / renewal, USD, treated 1:1 with USDC):

| TLD | Register | Renew |
| --- | --- | --- |
| .dev | 8.75 | 12.87 |
| .app | 8.75 | 14.93 |
| .xyz | 2.04 | 14.21 |
| .com | 11.08 | 11.08 |

CCTP forwarding Arc (domain 26) → Base (domain 6) quoted about 0.054 USDC on the sandbox Iris fee API (`forward=true`, fast finality). The operator adds a 0.02 USDC buffer by default. The vault pays `cost + maxFee + buffer` to the procurement wallet. Leftover USDC stays in that wallet. `pulse bill return-float` prints the ERC-20 transfer back to the vault and does not send it.

Suggested first deposit: **15 USDC** in the vault (one .dev plus up to two .xyz) or about **25 USDC** for two .dev/.app names. Also keep about **1 USDC** on the vault agent signer and **0.5 USDC** on the procurement wallet for Arc gas. Forwarding does not need Base ETH. The standard `depositForBurn` + `receiveMessage` path does, and it stays off unless `cctpBridge.allowStandard: true` and `forward: false`.

The operator's daily cap defaults to 10 checkouts. That is **our** limit. The Porkbun USDC guide fetched on 2026-10-07 does not state a 10-per-day cap for `payWith: usdc`. Porkbun's API monthly spend limit is $100 unless they raise it. Orders above $100 are refused by the operator's default monthly limit as well.

## Before the first mainnet run

1. Deploy a **new** PolicyVault. Do not reuse `0x4FACE6592Ba1AdF83E35B01CcD93D8704d647C01` (PolicyVault on Arc testnet, PulseReceipt on Arc mainnet). The deploy script and `SetupDomains` both revert if that address is used on chain 5042.

   ```bash
   cd contracts
   CONFIRM_MAINNET=1 EXPECTED_CHAIN_ID=5042 \
     forge script script/Deploy.s.sol:DeployPolicyVault --rpc-url "$ARC_MAINNET_RPC" --broadcast
   ```

2. Fill `deployments/arc-mainnet.json` with the new address. Copy `operator/config/mainnet.example.yaml` and set `vault`, `agent`, and `procurement.address`.

3. Owner setup, from the vault owner key:

   ```bash
   CONFIRM_MAINNET=1 EXPECTED_CHAIN_ID=5042 \
     VAULT_ADDRESS=0x... PROCUREMENT_ADDRESS=0x... AGENT_ADDRESS=0x... \
     DOMAINS_BUDGET=30000000 DOMAINS_PER_TX_CAP=15000000 DOMAINS_PERIOD=1209600 \
     forge script script/SetupDomains.s.sol:SetupDomains --rpc-url "$ARC_MAINNET_RPC" --broadcast
   ```

   That sets category `domains` (30 USDC / 14 days, 15 USDC per transaction), allowlists only the procurement wallet, sets over-limit mode to Escalate, and sets the agent when it differs. `pendingCount()` is the code check; a non-vault reverts.

4. Fund the vault with USDC (6 decimals). Fund the agent signer and the procurement wallet with native USDC for Arc gas.

5. Create a Circle **live** API key and entity secret. A testnet wallet set cannot transact on ARC mainnet. Use one developer-controlled wallet with blockchains ARC and BASE so the address matches, or set `procurement.baseAddress` to an address that the same signer controls. The operator refuses a Base address it cannot prove it controls.

6. Register a verified Porkbun account (email and phone), create a **live** API key, and raise the monthly API limit if the bills need it. Sandbox keys cannot pay with USDC.

7. Export `CONFIRM_MAINNET=1`, `AI_GATEWAY_API_KEY` (or keep `planner.driver: rules`), `PORKBUN_API_KEY`, `PORKBUN_SECRET_API_KEY`, `ARC_RPC_URL`, `BASE_RPC_URL`, Circle or `PROCUREMENT_PRIVATE_KEY` / `OPERATOR_PRIVATE_KEY`. Put key files at mode 0600. Do not commit them.

8. Set `maxSpendPerRunUSDC` and `maxBillUSDC`. Mainnet refuses to run without both. Those caps are also enforced on each bill: one bill over `maxBillUSDC`, or a run whose bills would pass `maxSpendPerRunUSDC`, escalates and does not transfer. Porkbun keys may be env vars or mode-0600 files (`apiKeyFile`, `secretFile`). `--years` must be 1; Porkbun charges the minimum term, so a multiplied quote would not match the charge. A register and a renewal of the same domain are `bill-register-<name>` and `bill-renew-<name>`. x402 typed data is signed with the Base wallet (`CIRCLE_PROCUREMENT_BASE_WALLET_ID`) and rejected unless the recovered signer is the payer.

## Commands

Dry-run (no network writes, fixture prices, synthetic ids):

```bash
cd operator
go run ./cmd/pulse bill add porkbun --domain pulseoperator.dev
go run ./cmd/pulse bill run --config config/dry-run.yaml --id bill-register-pulseoperator-dev
go run ./cmd/pulse bill list
go run ./cmd/pulse bill export --id bill-register-pulseoperator-dev
go run ./cmd/pulse bill return-float --config config/dry-run.yaml --amount 0.05
```

An escalated bill with no vault transaction is not picked up by `bill run`. The owner can send it through the vault without another model call, put it back in the queue, or close it:

```bash
go run ./cmd/pulse bill approve --config config/dry-run.yaml --id bill-register-pulseoperator-dev --yes
go run ./cmd/pulse bill reopen --config config/dry-run.yaml --id bill-register-pulseoperator-dev
go run ./cmd/pulse bill close --config config/dry-run.yaml --id bill-register-pulseoperator-dev
```

`approve` still refuses when the chain read failed, the category is disabled, the payee is not allowlisted, the quote is missing, or the amount is above the vault balance. It also refuses `monthly_limit` and `daily_cap`: those stops are Porkbun's, and paying the vault first would bridge USDC to Base and then leave it there when Porkbun rejects the order. `max_bill` and `max_spend_per_run` stay in force unless you pass `--override-cap` and `--override-reason`. The reason is appended to the audit as `owner_override` and stored in the decision rationale, so it is inside `decisionHash`. `--yes` and `--i-understand-real-money` default to false. Outside dry-run, `approve` requires `--yes` for that one bill. Mainnet still needs `CONFIRM_MAINNET=1` and `--i-understand-real-money` as well.

`reopen` of a bill that is already `done`, `merchant_paid`, `kept_as_credit`, `vault_paid`, or `bridged` says the bill is already paid. A bill that reached the vault and is still waiting says to settle it on Approvals.

The Bills admin page can approve only in dry-run, and that button does not set `--yes` or `--i-understand-real-money`. Testnet, live, and mainnet hide it; calling the route returns an error telling you to use the CLI. Approve and Reject for an on-chain `ApprovalRequest` (the Approvals page and `POST /api/operator/approve` or `reject`) accept only a loopback caller, in every mode. A LAN address, a non-loopback `X-Forwarded-For` hop, or a non-loopback `X-Real-Ip` is rejected even when the token's client IP says `127.0.0.1`.

The admin view (`./bin/pulse -view 43123`) listens on all interfaces. The framework has no bind-address option for that port, and this repo cannot disable `/api/servermanage/testtoken`. That route issues an admin token to RFC1918 callers when the process is marked local. Firewall the view to localhost, use an SSH tunnel, or pass `-view 0`. Mainnet will not start while `-view` is missing or non-zero unless `acknowledgeExposedAdminView: true` or `OPERATOR_ACK_EXPOSED_VIEW=1`. The default `-view` is 80, so `./pulse` with a mainnet config and no ack flag exits before it listens. API and gRPC still bind to `listen` (default `127.0.0.1`).

Testnet (Arc 5042002, Base Sepolia 84532). Point `porkbun.apiBase` at a local mock; Porkbun sandbox has no USDC, so x402 is not live there. This still dials the RPCs in the config.

```bash
cd operator
go run ./cmd/pulse bill run --config config/testnet.example.yaml --id bill-register-pulseoperator-dev --yes
```

Mainnet, one bill, interactive confirm:

```bash
cd operator
CONFIRM_MAINNET=1 go run ./cmd/pulse bill run \
  --config config/mainnet.yaml \
  --id bill-register-pulseoperator-dev \
  --i-understand-real-money --yes
```

Several bills in one process require `--auto` together with the spend caps. `--yes` alone is rejected when more than one bill is selected. `pause()` on the vault stops `pay` and `approve`.

## Idempotency

The decision hash is stored before `PolicyVault.pay`. A retry of a `decided` bill reuses it. `DecisionAlreadyUsed` marks the bill `failed_vault` and does not mint another hash. The burn transaction hash is saved as soon as it is broadcast, before Iris is polled; a retry that already has `CCTPBurnTx` only polls. A model `reject` of a bill the rules still allow becomes `escalate` (the bill stays open for a person) and the stored `risk_notes` are exactly the notes inside `decisionHash`. A Porkbun `PAYMENT_IN_PROGRESS` or `PAYMENT_PENDING` does not sign another x402 payload. If registration fails after the transfer, the money stays as Porkbun credit (`kept_as_credit`); retry that bill without `payWith`.

## If something sticks

- Vault paused or payee missing: the call reverts or escalates. Unpause or `setPayee` from the owner, then retry the same bill.
- CCTP: read `GET https://iris-api.circle.com/v2/messages/26?transactionHash=<burn>`. Forwarding is done when `forwardTxHash` is set. Do not burn a second time.
- x402 `PAYMENT_PENDING` / `PAYMENT_IN_PROGRESS`: do not pay again. Poll Porkbun with the same idempotency key and `usdcCheckoutId`.
- `PAYMENT_FAILED`, `COST_MISMATCH`, `MONTHLY_SPEND_LIMIT_EXCEEDED`, `VERIFICATION_REQUIRED`: the bill stores the code and `next_action`. Fix the account or the quote before another signature.
- Leftover USDC: leave it, or transfer it back with the calldata from `return-float`. That transfer is not automatic.

## What this repo did not do

No mainnet deploy, no mainnet transaction, and no spend. The integration tests (`go test -tags integration`) are skipped unless `CONFIRM_TESTNET=1` or `CONFIRM_LLM=1` is set, and they do not send value. Circle `sign/typedData` was checked against the published API reference. Porkbun's live `PAYMENT-REQUIRED` extra fields were not captured from a real 402; the signer follows the x402 Foundation exact and auth-capture specs and refuses a network, asset, or amount that is not the bill's.
