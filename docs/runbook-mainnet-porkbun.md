# Runbook: Porkbun domains paid from Arc mainnet

This runbook is the human checklist. The operator does not deploy, fund, or spend during CI.
Default mode is `dry-run`. A mainnet payment needs every gate below at the same time.

## What the model decides, and what it does not

`planner.driver: rules` (the CI and dry-run default) does not call a model. It records the deterministic action.

`planner.driver: gateway` calls [Vercel AI Gateway](https://vercel.com/docs/ai-gateway) at `https://ai-gateway.vercel.sh/v1/chat/completions` (OpenAI-compatible). The default model is `openai/gpt-5.4-nano`, confirmed on the public `GET /v1/models` list on 2026-10-07 (tool-use and structured outputs). The key is `AI_GATEWAY_API_KEY`. The model returns `action`, `rationale`, `risk_notes`, and `confidence`.

The model may only pick an action the Go rules already allow (`pay`, `defer`, `escalate`, `reject`). It cannot raise a cap, add a payee, or spend past a budget. A disagreement is written to the audit as `planner_disagree`. Invalid JSON, a timeout, or a transport error becomes `escalate` and does **not** submit a transferring `pay`.

A bill whose amount is above the vault balance escalates with `insufficient_balance` and does not call the vault. Dry-run subtracts what this process has already committed to pay from the fixture balance and the domains remaining budget, so a later bill in the same run sees the lower figures. The fixture does not change on disk: a new `bill run` or `bill approve` process starts from those numbers again. On testnet and mainnet, a failed read of the balance, category, payee allowlist, or caps escalates with `observe_failed`. The bill is not treated as enabled, allowlisted, or uncapped, and the vault is not called. A live read subtracts only spend committed in this process that the chain balance does not already include (a vault approval, or a pay that has not transferred). In dry-run, a status of `paid`, `simulated_paid`, `dry_run_paid`, or `circle_confirmed` drops that amount from the pending total. On testnet, live, and mainnet, a bill drops it only after status `paid` and a real `0x` 32-byte transaction hash. A `dry_run_*` or `simulated_*` status, or an empty hash, sets the bill to `failed_vault` with reason code `vault_not_broadcast`, leaves that pending amount in place, and does not burn or pay the merchant.

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

4. Fund the vault with USDC (6 decimals). Fund the agent signer and the procurement wallet with native USDC for Arc gas. Arc gas is the native balance (`eth_getBalance`, 18 decimals), not the ERC-20 balance. The bill preflight refuses when either native balance is below `minGasUSDC` (default 0.05).

5. Three local keys. Mainnet does not use a Circle API key, entity secret, or wallet id. Iris (fees and messages) is a public API.

   - **Owner.** The forge scripts use `PRIVATE_KEY`. The operator reads `OWNER_PRIVATE_KEY` only for sweep, approve, and reject. On mainnet those admin paths stay simulate-only.
   - **Agent.** `OPERATOR_PRIVATE_KEY` or `secrets.agentKeyFile`. This key signs `PolicyVault.pay`. Its address must equal `agent` and the on-chain `PolicyVault.agent()`.
   - **Procurement and Base payer.** One key: `PROCUREMENT_PRIVATE_KEY` or `procurement.keyFile`. The same EOA is the Arc procurement wallet and the Base x402 payer. Leave `procurement.baseAddress` empty. Base needs USDC and no ETH. Forwarding pays the Base gas out of the CCTP fee.

   A key file is mode 0600. It may be raw hex, or JSON `{"address","private_key"}`. If `address` is present it must match the key. Do not commit the files.

6. Register a verified Porkbun account (email and phone), create a **live** API key, and raise the monthly API limit if the bills need it. Sandbox keys cannot pay with USDC.

7. Export `CONFIRM_MAINNET=1`, `AI_GATEWAY_API_KEY` (or keep `planner.driver: rules`), `PORKBUN_API_KEY`, `PORKBUN_SECRET_API_KEY`, `ARC_RPC_URL`, `BASE_RPC_URL`, `OPERATOR_PRIVATE_KEY`, and `PROCUREMENT_PRIVATE_KEY`. Do not set `CIRCLE_API_KEY`, `CIRCLE_ENTITY_SECRET`, or a Circle wallet id.

8. Set `maxSpendPerRunUSDC` and `maxBillUSDC`. Mainnet refuses to run without both. Those caps are also enforced on each bill: one bill over `maxBillUSDC`, or a run whose bills would pass `maxSpendPerRunUSDC`, escalates and does not transfer. Porkbun keys may be env vars or mode-0600 files (`apiKeyFile`, `secretFile`). `--years` must be 1; Porkbun charges the minimum term, so a multiplied quote would not match the charge. A register and a renewal of the same domain are `bill-register-<name>` and `bill-renew-<name>`. Before any payment client is built, preflight checks the agent key, the procurement key, `PolicyVault.agent()`, and the native gas balances. x402 is signed with the procurement key and refused when that key is not the configured payer. A missing key on mainnet fails closed and names the env var or file. It does not fall back to Circle.

`cctpBridge.irisBase` defaults to `https://iris-api.circle.com` on mainnet and the sandbox on every other mode. `circle.irisBase` is still accepted as an alias. Mainnet rejects a sandbox Iris URL and rejects `executor: circle-wallets` or `circle-agent`.

The operator loop, `runonce`, the admin routes, and the Approvals page keep `Broadcast: false`. On mainnet they still only simulate. `bill run` and `bill approve` set `Broadcast: true` only after `authorizeBills` and the mainnet gate have passed, in that same process. `CONFIRM_MAINNET=1` is still required before a send on chain 5042.

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

The Bills admin page can approve only in dry-run, and that button does not set `--yes` or `--i-understand-real-money`. Testnet, live, and mainnet hide it; calling the route returns an error telling you to use the CLI. Approve and Reject for an on-chain `ApprovalRequest` (the Approvals page and `POST /api/operator/approve` or `reject`), Bills → Reopen and Close, and `POST /api/operator/runonce`, `recordrevenue`, `cctp`, and `gateway` accept only a loopback caller, in every mode. A LAN address, a non-loopback `X-Forwarded-For` hop, or a non-loopback `X-Real-Ip` is rejected even when the token's client IP says `127.0.0.1`.

A refused `bill approve` leaves the bill's hashed columns unchanged, including `reasonCode` and `rationale`. The process error is the refusal. When `auditLog` is set, the same refusal is an `approve_blocked` row.

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

The decision hash is stored before `PolicyVault.pay`. A retry of a `decided` bill reuses it. `DecisionAlreadyUsed` marks the bill `failed_vault` and does not mint another hash. If the pay transaction was broadcast but the receipt could not be read, the bill is `failed_vault` with reason code `vault_receipt_unknown` and keeps that hash. It is not treated as transferred, and a retry does not burn or pay Porkbun. The burn transaction hash is saved as soon as it is broadcast, before Iris is polled; a retry that already has `CCTPBurnTx` only polls. With forwarding on, the burn counts as bridged only after Iris sets `forwardTxHash`. Before the first x402 signature, the operator reads Base USDC `balanceOf(payer)` and continues only when that balance is at least the bill cost. If the mint or that balance is still missing when the poll timeout ends (default 3 minutes, `cctpBridge.pollTimeout`), the bill stays `bridged` with reason code `awaiting_mint`. That retry polls and does not burn again, and it does not sign x402. A model `reject` of a bill the rules still allow becomes `escalate` (the bill stays open for a person) and the stored `risk_notes` are exactly the notes inside `decisionHash`. A Porkbun `PAYMENT_IN_PROGRESS` or `PAYMENT_PENDING` does not sign another x402 payload. Once `porkbun_checkout_id` is stored, a retry polls that checkout even when the Base balance is below the cost, because the USDC may already have been paid. If Porkbun asks for `PAYMENT_REQUIRED` again on that checkout, the bill becomes `failed_merchant` with reason code `checkout_needs_review` and is not signed a second time. If registration fails after the transfer, the money stays as Porkbun credit (`kept_as_credit`); retry that bill without `payWith`.

## If something sticks

- Vault paused or payee missing: the call reverts or escalates. Unpause or `setPayee` from the owner, then retry the same bill.
- Vault pay simulated (`vault_not_broadcast`): the bill did not leave the vault. Fix the signer and `CONFIRM_MAINNET=1`, then retry the same bill. Do not bridge around it.
- Vault receipt missing (`vault_receipt_unknown`): a transaction hash was broadcast and stored. Look it up before doing anything else. Do not burn or pay Porkbun for that bill.
- CCTP: read `GET https://iris-api.circle.com/v2/messages/26?transactionHash=<burn>`. Forwarding is done when `forwardTxHash` is set. The Base USDC balance is checked only before the first x402 signature. `awaiting_mint` means wait and retry; do not burn a second time.
- x402 `PAYMENT_PENDING` / `PAYMENT_IN_PROGRESS` / `IDEMPOTENCY_KEY_IN_USE`: do not pay again. Confirmation polls use `Idempotency-Key` `<attemptKey>-confirm` and are never signed. The poll body keeps `payWith: "usdc"` and adds `usdcCheckoutId`. The Base balance check is skipped once a checkout id is stored.
- The signed retry is the same request that received the 402: the same body (`payWith: "usdc"`, no `usdcCheckoutId`) and the same `Idempotency-Key`, plus the `PAYMENT-SIGNATURE` header. `usdcCheckoutId` is only for confirming a checkout that was paid elsewhere, and it is sent together with `payWith: "usdc"` (Porkbun API spec v3.60). A confirmation poll does not reuse that key.
- Porkbun create/renew uses one idempotency key per merchant attempt: `bill-<bill id>-a<N>`. A network retry of that attempt reuses the key. Before signing, the operator reads `GET /account/balance` and stores that balance in cents on the attempt. If that read fails, nothing is signed; the attempt stays unsigned and the next run retries the same key. After the x402 authorization is signed, and before that signed request is sent, the bill stores the checkout id and a signed marker (`validBefore`, nonce, and payer). The signature and keys are not stored. A signed attempt keeps its key and is never signed again. If a poll returns `PAYMENT_REQUIRED`, the bill is `checkout_needs_review` and is not signed. A new attempt is allowed only when Porkbun returns `PAYMENT_EXPIRED` (https://porkbun.com/api/json/v3/spec) and that authorization's `validBefore` has passed, or when `pulse bill merchant-reset --id <bill> --yes` passes every guard below. Unsigned attempts move to the next key only when the previous one left no usable checkout (no 402, `IDEMPOTENCY_KEY_MISMATCH`, `INSUFFICIENT_FUNDS`, or `PAYMENT_EXPIRED`). A message that merely contains "expir" is not expiry. `merchant_idempotency_mismatch` and `merchant_insufficient_funds` are separate from `x402_rejected`. A bill that already failed with the old `bill-<bill id>` key, and has no checkout id, takes attempt 2 on the next `bill run` and does not pay the vault or burn again. Attempts are in `bill export`. When a bill reaches `done`, a stale `merchant_*`, `x402_*`, or `checkout_needs_review` reason code is cleared.
- `bill merchant-reset` is CLI-only. It is not on the HTTP API. It requires `--yes`. It opens the next attempt for a signed bill only when all of these hold: the current time is after that attempt's `validBefore`; Base USDC `authorizationState(payer, nonce)` is false (an attempt with no recorded payer uses the configured payer; a payer that does not match the configured payer is refused); `GET /account/balance` matches the balance recorded at signing; `POST /domain/listAll` does not contain the domain. Otherwise it refuses and leaves the attempt in place. It does not sign, pay the vault, or burn. A signed attempt from before balances were stored can pass `--expected-balance-cents N` instead of a recorded balance. That flag is accepted only when the attempt has no recorded balance, and it is refused when a balance was recorded. `N` must equal the live Porkbun balance. On success the attempt and the audit log both record `N` and `balance_from_flag`.
- x402 auth-capture accepts Commerce Payments v1.0.0 (`0xBdEA…0cff` with collector `0x0E3d…7757`) and v1.1.0 (`0xf968…B19c` with collector `0x8612…ab88`) on Base. A mixed pair or any other escrow is `x402_unsupported_escrow` and is not signed. Other merchant or x402 failures are `x402_rejected`. The decoded `PAYMENT-REQUIRED` body is stored on the bill and in `bill export`. If the 402 offers `exact` and `auth-capture`, the signer uses `exact`.
- Arc transactions use `eth_gasPrice`, or the block base fee plus `maxPriorityFeePerGasGwei`, and never exceed `maxFeePerGasGwei`. They do not always bid the cap.
- `checkout_needs_review`: Porkbun asked for another signature on a checkout that was already signed. Reconcile it by hand. Do not sign again.
- `PAYMENT_FAILED`, `COST_MISMATCH`, `MONTHLY_SPEND_LIMIT_EXCEEDED`, `VERIFICATION_REQUIRED`: the bill stores the code and `next_action`. Fix the account or the quote before another signature.
- Leftover USDC: leave it, or transfer it back with the calldata from `return-float`. That transfer is not automatic.

## What this repo did not do

No mainnet deploy, no mainnet transaction, and no spend. The integration tests (`go test -tags integration`) are skipped unless `CONFIRM_TESTNET=1` or `CONFIRM_LLM=1` is set, and they do not send value. Circle `sign/typedData` was checked against the published API reference. Commerce Payments v1.0.0 and v1.1.0 escrow and EIP-3009 collector addresses were checked against the GitHub releases and against code on Base chain id 8453. Porkbun's live `PAYMENT-REQUIRED` body from the failed bill was not stored before this change; later offers are saved on the bill. The signer still refuses a network, asset, amount, or payTo that is not the bill's.
