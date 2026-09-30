# Pulse Operator

An AI operator agent that pays for things in **USDC on Arc** — inside a spending policy it
cannot talk its way out of. Built for the **Tameion Agents Hackathon** (Canteen x Circle, on Arc).

- `contracts/` — Foundry project. `PolicyVault.sol` (new) holds the funds and enforces the policy.
- `agent/` — runnable deterministic invoice CLI (`cmd/operator`) → stable payment identity →
  PolicyVault ABI dry-run / local MockERC20 payment → durable recovery journal. The inherited
  trading/Jev/Monad stamp libraries remain separate; no real model runs in the payment demo.
- Pre-event code is isolated in the `tameion-start` commit (see [BASELINE.md](BASELINE.md)).
  Everything built during the event:
  <https://github.com/vincent-lxc/pulse-operator/compare/tameion-start...main>

## PolicyVault — limits enforced on-chain, not in the prompt

The agent never holds the treasury. It holds an **operator key** whose only power is
`PolicyVault.pay(category, payee, amount, decisionHash)`. Every rule below is checked by the
contract on every call, so a prompt-injected, jailbroken or buggy agent still cannot move more
than the owner allowed. The LLM's "instructions" are advisory; the vault is authoritative.

| Rule | Who sets it | Enforcement |
|---|---|---|
| Per-category **budget per epoch** (e.g. `infra` = 50 USDC / 7 days) | owner `setCategory` | `pay` over remaining budget → escalated or reverted |
| **Per-transaction cap** per category | owner `setCategory` | `pay` over cap → escalated or reverted |
| **Payee allowlist** per category | owner `setPayee` | non-allowlisted payee → always reverts |
| **Over-limit mode** | owner `setOverLimitMode` | `Escalate` (default): no transfer, creates a pending `ApprovalRequest`; `Revert`: call reverts |
| **Human approval** | owner `approve(id)` / `reject(id)` | approve transfers and counts against the current epoch budget |
| **Kill switch** | owner `pause()` / `unpause()` | fail-closed: `pay` and `approve` revert while paused (`reject`/`sweep` still work) |
| **Reserve sweep** | owner `setReserve`, `sweepToReserve(amount)` | funds can only be swept to the owner-set reserve |
| **Idempotency** | — | each `decisionHash` can be used by `pay` once, so a retried decision can't double-pay |
| **Key rotation** | owner `setAgent`, two-step `transferOwnership`/`acceptOwnership` | |

**Roles.** `owner` = the human (ideally a hardware wallet / multisig). `agent` = the operator
key the Go agent signs with. `reserve` = cold wallet for sweeps.

**Audit trail.** Every event carries a `decisionHash` — for agent payments it is the keccak256
of the agent's audit record (the same hash the pipeline already stamps), so each on-chain
transfer or escalation links back to the exact off-chain reasoning. Owner actions accept an
optional rationale hash (zero allowed).

**Views.** `remainingBudget(cat)`, `getCategory(cat)` (budget, cap, epoch window, spent split
into `autoSpent` vs `approvedSpent`), `pendingRequestIds()`, `pendingCount()`,
`getRequest(id)`, `counters()` → `{decidedByAgent, escalated, approved, rejected}`,
`isPayeeAllowed(cat, payee)`, `decisionUsed(hash)`, `balance()`.

**Epochs.** Each category has its own period (seconds) anchored at configuration time;
counters reset lazily when a new epoch starts. Changing only budget/cap keeps the current
epoch's spend; changing the period restarts the epoch.

**Invariant (tested).** For every category and epoch, what the agent spent autonomously is
`<= budget`; total spend can exceed the budget only through owner-approved requests.

### Build & test

```bash
cd contracts
forge build
forge test          # unit + fuzz + invariant (PolicyVault), plus baseline PulseReceipt/PulseTradeStamp
```

### Deploy (Arc testnet)

Arc facts used here (checked against docs.arc.io on 2026-09-27):

- Arc **testnet**: RPC `https://rpc.testnet.arc.io` (the older `https://rpc.testnet.arc.network`
  also answers), chainId **5042002**, explorer `https://explorer.testnet.arc.io`, faucet
  `https://faucet.circle.com`. **Mainnet**: `https://rpc.mainnet.arc.io`, chainId **5042**.
- **Gas is paid in USDC** (native balance, 18 decimals). The USDC **ERC-20 interface** at
  `0x3600000000000000000000000000000000000000` uses **6 decimals** and shares the same balance.
  The vault only ever uses the 6-decimal ERC-20 interface.
- Minimum base fee is **20 gwei**; transactions with `maxFeePerGas` below that are rejected /
  never mined. Keep `maxFeePerGas` comfortably above it.

```bash
cd contracts
cp .env.example .env   # fill PRIVATE_KEY (funded from the faucet) and AGENT_ADDRESS
source .env
forge script script/Deploy.s.sol:DeployPolicyVault \
  --rpc-url "$ARC_TESTNET_RPC_URL" --broadcast \
  --with-gas-price 50gwei --priority-gas-price 2gwei
```

`PRIVATE_KEY` is read from the environment only. The script refuses Arc mainnet unless
`CONFIRM_MAINNET=1`. Fund the vault by transferring USDC (ERC-20) to its address.

**Live on Arc testnet:** PolicyVault
[`0x4FACE6592Ba1AdF83E35B01CcD93D8704d647C01`](https://testnet.arcscan.app/address/0x4FACE6592Ba1AdF83E35B01CcD93D8704d647C01)
(source verified). The end-to-end smoke test (deposit → policy → agent pay → escalation → owner approve →
non-allowlisted revert → sweep) is written up in [docs/testnet-smoke.md](docs/testnet-smoke.md), with the
record in [deployments/arc-testnet.json](deployments/arc-testnet.json).

> Foundry's local EVM can't simulate Arc USDC transfers: they go through a precompile at `0x1800…`. For live calls that
> move USDC, use `cast send` (see `contracts/script/smoke-testnet.sh`). The tests use `MockERC20`.

## Runnable local payment demo

```bash
cd agent && go test ./... && cd ..
python3 scripts/local-demo.py
```

This launches its own silent local Anvil chain, deploys **MockERC20** + PolicyVault,
pays 0.50 mock USDC, escalates a 1.50 over-cap invoice for explicit local owner
approval, and verifies that process restarts and journal loss do not double-pay.
Default CLI mode is Arc PolicyVault **ABI dry-run without network**. Sending is
restricted to loopback chain 31337 and unlocked local development accounts; no
wallet/private key is read. Planner input is a deterministic invoice, **not live
AI output**; actual Arc USDC payments and real model integration are not exercised.

See [commands, identity/recovery behavior and exact boundaries](docs/local-payment-demo.md).
CI runs contracts, Go regressions and this local chain demo.

## License

MIT
