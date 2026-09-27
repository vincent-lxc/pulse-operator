# PolicyVault: Arc testnet smoke test

Run on 2026-09-27, about 07:11–07:14 UTC (15:11–15:14 UTC+8), on Arc testnet (chainId 5042002).
Machine-readable record: [`deployments/arc-testnet.json`](../deployments/arc-testnet.json).

## Deployment

| | |
|---|---|
| PolicyVault | [`0x4FACE6592Ba1AdF83E35B01CcD93D8704d647C01`](https://testnet.arcscan.app/address/0x4FACE6592Ba1AdF83E35B01CcD93D8704d647C01) |
| Deploy tx | [`0x8f8f7996…`](https://testnet.arcscan.app/tx/0x8f8f79965023ac3ea0e6dc561f5b48a124802650bd2a3ba04ad79bf000c91817) (block 64231944) |
| Source | verified on Blockscout, full match ([contract tab](https://testnet.arcscan.app/address/0x4FACE6592Ba1AdF83E35B01CcD93D8704d647C01?tab=contract)), built from commit `607e605` |
| Token | Arc USDC ERC-20 `0x3600000000000000000000000000000000000000` (6 decimals) |
| Owner / reserve | `0x60C35aBAF3B7eA46646e8e796181c9b583C191C7` |
| Agent (operator key) | `0xb71c11F7b28D9A912e7C3ACfbe423f7DeA568DEa` |

Commands used:

```bash
forge script script/Deploy.s.sol:DeployPolicyVault --rpc-url https://rpc.testnet.arc.io \
  --broadcast --slow --with-gas-price 50gwei --priority-gas-price 2gwei \
  --verify --verifier blockscout --verifier-url https://explorer.testnet.arc.io/api/
```

(`testnet.arcscan.app` redirects to the Blockscout instance at `explorer.testnet.arc.io`. The effective gas price was 22 gwei, above the 20 gwei floor.)

## Workflow

Policy: category `api` with a budget of 2 USDC per 7-day epoch and a 1 USDC per-tx cap. One allowlisted payee,
`0xf1E6D4369a589D5542eA15d0627d7675B84F2f1b`, a throwaway address from `cast wallet new` whose key was discarded. Over-limit mode is `Escalate`.

| # | Step | Tx | Block | Result |
|---|---|---|---|---|
| 1 | owner → vault: USDC.transfer 5 USDC | [`0x3d6eecbd…`](https://testnet.arcscan.app/tx/0x3d6eecbd3faa5bfe13e3c38466eb39eece796947bdd205108ad542622b3ce7a2) | 64232175 | ✅ vault funded with 5 USDC |
| 2 | owner: setCategory(api, 2 USDC/week, cap 1 USDC) | [`0x476d7ee7…`](https://testnet.arcscan.app/tx/0x476d7ee771599b870a6cd4a3599c8fe1d4eeb9ced55e02608b4a0cbbf2ba450b) | 64232179 | ✅ CategoryConfigured |
| 3 | owner: setPayee(api, payee, true) | [`0xb7d6102e…`](https://testnet.arcscan.app/tx/0xb7d6102e8fbf4f583244a9c5630b31e103d37d155f0357d8d5994f146c96a273) | 64232183 | ✅ PayeeSet |
| 4 | agent: pay(api, payee, 0.5 USDC) | [`0xfc7d5e40…`](https://testnet.arcscan.app/tx/0xfc7d5e40ee594803aef7d7a5560464ce22667d4d0dac5c529cfdeae2a2c9a613) | 64232187 | ✅ paid right away (AgentPaid), 0.5 USDC → payee |
| 5 | agent: pay(api, payee, 1.5 USDC) | [`0x8a8d5cfb…`](https://testnet.arcscan.app/tx/0x8a8d5cfbb965fe5312cf3b332722f067ea5889a65d5aebdde1d3d272801c0a33) | 64232192 | ✅ over cap → **no transfer**, ApprovalRequested id=1 (reason 1 = OVER_TX_CAP) |
| 6 | owner: approve(1) | [`0xde4cc4ae…`](https://testnet.arcscan.app/tx/0xde4cc4ae935a36af5fb05084d696b369188ead78330c6778741317f830fec266) | 64232197 | ✅ RequestApproved, 1.5 USDC → payee, counted against the epoch budget |
| 7 | owner: sweepToReserve(0.5 USDC) | [`0xaa486882…`](https://testnet.arcscan.app/tx/0xaa486882107383ac2d1ccdc04134f819de8464bcce89223fcb49fbee62ea836a) | 64232202 | ✅ SweptToReserve, 0.5 USDC → reserve (= owner) |
| — | agent: pay(api, **non-allowlisted** `0xcFa5B889…`, 0.1 USDC) | `eth_call` only (no gas spent) | — | ✅ reverted `PayeeNotAllowed(api, 0xcFa5B889…)` (selector `0x381b76e5`) |

Final on-chain state:
- `counters()`: decidedByAgent 1, escalated 1, approved 1, rejected 0.
- `getCategory(api)`: spent 2.0 = autoSpent 0.5 + approvedSpent 1.5, remaining 0, no pending requests.
- Vault holds 2.5 USDC. The payee received 2.0 USDC.

## decisionHashes

Each hash is `keccak256` of a small JSON decision record, so anyone can reproduce it with `cast keccak '<json>'`:

| Decision | JSON | keccak256 |
|---|---|---|
| set_policy | `{v:1,actor:owner,action:set_policy,category:api,budget_usdc:2,per_tx_cap_usdc:1,period:7d,payee:0xf1E6D4369a589D5542eA15d0627d7675B84F2f1b,note:arc-testnet smoke}` | `0x036b904387c115d760d54d12d61e89b892729af7a49e180c7cd4d191345ad910` |
| pay 0.5 | `{v:1,actor:agent,action:pay,category:api,payee:0xf1E6D4369a589D5542eA15d0627d7675B84F2f1b,amount_usdc:0.5,reason:api credits top-up, within policy,note:arc-testnet smoke #1}` | `0xb4bef68d1685f14e81d417676d442fc81d1e9ecf538db6d7c271df9eab8f42cf` |
| pay 1.5 | `{v:1,actor:agent,action:pay,category:api,payee:0xf1E6D4369a589D5542eA15d0627d7675B84F2f1b,amount_usdc:1.5,reason:annual api plan, exceeds per-tx cap,note:arc-testnet smoke #2}` | `0x331ebc4c11a8dd0cd9e4f7c1a012288a7df77dcf12deaf13e992a7e9e29a84c6` |
| sweep | `{v:1,actor:owner,action:sweep_to_reserve,amount_usdc:0.5,note:arc-testnet smoke #3}` | `0xca7977ade2f90ba945328e3208411a2dd4325ec55433ea219c816ed9bb20d628` |

## Gotcha: Foundry cannot simulate Arc USDC transfers

Arc's USDC ERC-20 (`NativeFiatTokenV2_2` behind `0x3600…`) moves the native balance through a
precompile at `0x1800000000000000000000000000000000000000`. Foundry's local EVM does not implement that precompile,
so `forge script` simulation (and fork tests) revert with `StackUnderflow` on any `USDC.transfer`.
Deploying still works because it never moves USDC. For live calls that move USDC, use `cast send`, which the node
executes directly (see `contracts/script/smoke-testnet.sh`). Unit, fuzz and invariant tests use `MockERC20`.
