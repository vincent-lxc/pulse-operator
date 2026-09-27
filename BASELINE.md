# BASELINE — pre-existing code (pre-dates Tameion Agents Hackathon)

This commit (tagged **`tameion-start`**) contains **only code that existed before the
Tameion Agents Hackathon (Canteen x Circle, on Arc), which started 2026-09-27**.
Every file below was copied **byte-for-byte** from the author's earlier public repos
(git blob SHAs are identical to the source blobs). No logic was changed.

The only files authored at event start are this `BASELINE.md` (a manifest) and the
`LICENSE` file created by GitHub with the repo.

**Everything written during the event is in the diff after this tag:**

    https://github.com/vincent-lxc/pulse-operator/compare/tameion-start...main

## Source repositories and exact commits

| Source repo | Commit SHA | Commit date (UTC) | License |
|---|---|---|---|
| https://github.com/vincent-lxc/pulse-on-monad | `0260897ff3f263a2e4d902a849a35cd96b153cc2` | 2026-09-26T19:26:38Z | MIT |
| https://github.com/vincent-lxc/pulse-receipt | `19e07b5c8c5244f921c6cecb9ef6733a51d4147a` | 2026-09-19T21:00:54Z | MIT |

Vendored third-party: `contracts/lib/forge-std` = foundry-rs/forge-std **v1.16.2**
(`bf647bd6046f2f7da30d0c2bf435e5c76a780c1b`), as vendored in pulse-receipt (MIT/Apache-2.0).

## What is reused

- `agent/` — Go module from pulse-on-monad (module path kept verbatim:
  `github.com/vincent-lxc/pulse-on-monad/agent`). Packages: `internal/risk` (hard risk
  gate), `internal/audit` (audit JSONL + `decisionHash`), `internal/jev` (optional
  TypeSafe Jev soft gate), `internal/stamp` (on-chain decisionHash stamping), plus their
  in-module dependencies `internal/policy` and `internal/outcome`.
- `contracts/` — Foundry project config + forge-std from pulse-receipt (Arc-configured),
  `PulseReceipt.sol` (deployed on Arc mainnet, see `docs/pulse-receipt-deployments.md`)
  and `PulseTradeStamp.sol` from pulse-on-monad, with their tests.
- `contracts/script/DeployPulseReceipt.s.sol` is pulse-receipt `script/Deploy.s.sol`
  (renamed only, content identical).

## File manifest (source → destination)

| Source repo | Source path | Destination path | Blob SHA |
|---|---|---|---|
| `pulse-on-monad` | `.gitignore` | `.gitignore` | `bd44e435f1cf` |
| `pulse-on-monad` | `agent/go.mod` | `agent/go.mod` | `a6dea2d0dc10` |
| `pulse-on-monad` | `agent/go.sum` | `agent/go.sum` | `ec0845ac5364` |
| `pulse-on-monad` | `agent/internal/audit/record.go` | `agent/internal/audit/record.go` | `ba58af96e1a5` |
| `pulse-on-monad` | `agent/internal/audit/record_test.go` | `agent/internal/audit/record_test.go` | `d39e6e305af2` |
| `pulse-on-monad` | `agent/internal/audit/store.go` | `agent/internal/audit/store.go` | `8ddbfe7bb62b` |
| `pulse-on-monad` | `agent/internal/jev/client.go` | `agent/internal/jev/client.go` | `a202a3f11d92` |
| `pulse-on-monad` | `agent/internal/jev/client_test.go` | `agent/internal/jev/client_test.go` | `441b43e7e951` |
| `pulse-on-monad` | `agent/internal/outcome/label.go` | `agent/internal/outcome/label.go` | `40c15e9688fe` |
| `pulse-on-monad` | `agent/internal/outcome/label_test.go` | `agent/internal/outcome/label_test.go` | `6190da11e83e` |
| `pulse-on-monad` | `agent/internal/outcome/store.go` | `agent/internal/outcome/store.go` | `fbb716a49448` |
| `pulse-on-monad` | `agent/internal/policy/weights.go` | `agent/internal/policy/weights.go` | `3bec35cad7ad` |
| `pulse-on-monad` | `agent/internal/policy/weights_test.go` | `agent/internal/policy/weights_test.go` | `adfa0ef38a83` |
| `pulse-on-monad` | `agent/internal/risk/engine.go` | `agent/internal/risk/engine.go` | `1c117244304b` |
| `pulse-on-monad` | `agent/internal/risk/engine_test.go` | `agent/internal/risk/engine_test.go` | `22d4d861c585` |
| `pulse-on-monad` | `agent/internal/stamp/client.go` | `agent/internal/stamp/client.go` | `6b46715e7542` |
| `pulse-on-monad` | `agent/internal/stamp/encode.go` | `agent/internal/stamp/encode.go` | `ceb9b85ee444` |
| `pulse-on-monad` | `agent/internal/stamp/encode_test.go` | `agent/internal/stamp/encode_test.go` | `9a3edeb4a574` |
| `pulse-on-monad` | `agent/internal/stamp/live.go` | `agent/internal/stamp/live.go` | `3786a27ef44f` |
| `pulse-on-monad` | `agent/internal/stamp/live_test.go` | `agent/internal/stamp/live_test.go` | `dffec8dfb288` |
| `pulse-on-monad` | `contracts/.gitignore` | `contracts/.gitignore` | `83f939c72683` |
| `pulse-on-monad` | `contracts/src/PulseTradeStamp.sol` | `contracts/src/PulseTradeStamp.sol` | `c21ea67cbcdc` |
| `pulse-on-monad` | `contracts/test/PulseTradeStamp.t.sol` | `contracts/test/PulseTradeStamp.t.sol` | `cbdd20023740` |
| `pulse-on-monad` | `docs/audit-schema.md` | `docs/audit-schema.md` | `4fc9aa8b6aa5` |
| `pulse-receipt` | `.env.example` | `contracts/.env.example` | `b9f11ea7c78f` |
| `pulse-receipt` | `docs/deployments.md` | `docs/pulse-receipt-deployments.md` | `479677625d1b` |
| `pulse-receipt` | `foundry.lock` | `contracts/foundry.lock` | `31b0fe2e2535` |
| `pulse-receipt` | `foundry.toml` | `contracts/foundry.toml` | `e83c062e76e0` |
| `pulse-receipt` | `lib/forge-std/.gitattributes` | `contracts/lib/forge-std/.gitattributes` | `27042d458c62` |
| `pulse-receipt` | `lib/forge-std/.gitignore` | `contracts/lib/forge-std/.gitignore` | `756106d38842` |
| `pulse-receipt` | `lib/forge-std/CONTRIBUTING.md` | `contracts/lib/forge-std/CONTRIBUTING.md` | `8f4aa895b498` |
| `pulse-receipt` | `lib/forge-std/LICENSE-APACHE` | `contracts/lib/forge-std/LICENSE-APACHE` | `cf01a499fbc5` |
| `pulse-receipt` | `lib/forge-std/LICENSE-MIT` | `contracts/lib/forge-std/LICENSE-MIT` | `28f98304ac6d` |
| `pulse-receipt` | `lib/forge-std/README.md` | `contracts/lib/forge-std/README.md` | `13015e4fc6b8` |
| `pulse-receipt` | `lib/forge-std/RELEASE_CHECKLIST.md` | `contracts/lib/forge-std/RELEASE_CHECKLIST.md` | `82b33c2cd441` |
| `pulse-receipt` | `lib/forge-std/foundry.toml` | `contracts/lib/forge-std/foundry.toml` | `6035b855c4d3` |
| `pulse-receipt` | `lib/forge-std/package.json` | `contracts/lib/forge-std/package.json` | `fbcea6682a5a` |
| `pulse-receipt` | `lib/forge-std/src/Base.sol` | `contracts/lib/forge-std/src/Base.sol` | `d948010fe9f8` |
| `pulse-receipt` | `lib/forge-std/src/Config.sol` | `contracts/lib/forge-std/src/Config.sol` | `3d35bb583a0c` |
| `pulse-receipt` | `lib/forge-std/src/LibVariable.sol` | `contracts/lib/forge-std/src/LibVariable.sol` | `32fe5bfa929c` |
| `pulse-receipt` | `lib/forge-std/src/Script.sol` | `contracts/lib/forge-std/src/Script.sol` | `d43fa8a7abcb` |
| `pulse-receipt` | `lib/forge-std/src/StdAssertions.sol` | `contracts/lib/forge-std/src/StdAssertions.sol` | `daad7d976d38` |
| `pulse-receipt` | `lib/forge-std/src/StdChains.sol` | `contracts/lib/forge-std/src/StdChains.sol` | `4fed0b523255` |
| `pulse-receipt` | `lib/forge-std/src/StdCheats.sol` | `contracts/lib/forge-std/src/StdCheats.sol` | `95a5bf95e91c` |
| `pulse-receipt` | `lib/forge-std/src/StdConfig.sol` | `contracts/lib/forge-std/src/StdConfig.sol` | `167523082bcc` |
| `pulse-receipt` | `lib/forge-std/src/StdConstants.sol` | `contracts/lib/forge-std/src/StdConstants.sol` | `9f069ef8cb24` |
| `pulse-receipt` | `lib/forge-std/src/StdError.sol` | `contracts/lib/forge-std/src/StdError.sol` | `94df1594514f` |
| `pulse-receipt` | `lib/forge-std/src/StdInvariant.sol` | `contracts/lib/forge-std/src/StdInvariant.sol` | `2c79b6e2595a` |
| `pulse-receipt` | `lib/forge-std/src/StdJson.sol` | `contracts/lib/forge-std/src/StdJson.sol` | `2c8944222af2` |
| `pulse-receipt` | `lib/forge-std/src/StdMath.sol` | `contracts/lib/forge-std/src/StdMath.sol` | `19942f78db95` |
| `pulse-receipt` | `lib/forge-std/src/StdStorage.sol` | `contracts/lib/forge-std/src/StdStorage.sol` | `3035880eefda` |
| `pulse-receipt` | `lib/forge-std/src/StdStyle.sol` | `contracts/lib/forge-std/src/StdStyle.sol` | `33b815fcbd48` |
| `pulse-receipt` | `lib/forge-std/src/StdToml.sol` | `contracts/lib/forge-std/src/StdToml.sol` | `a5198d82e9e8` |
| `pulse-receipt` | `lib/forge-std/src/StdUtils.sol` | `contracts/lib/forge-std/src/StdUtils.sol` | `8dd7b4732c42` |
| `pulse-receipt` | `lib/forge-std/src/Test.sol` | `contracts/lib/forge-std/src/Test.sol` | `af91dd81cc40` |
| `pulse-receipt` | `lib/forge-std/src/Vm.sol` | `contracts/lib/forge-std/src/Vm.sol` | `4fe3f416ebf9` |
| `pulse-receipt` | `lib/forge-std/src/console.sol` | `contracts/lib/forge-std/src/console.sol` | `0ac1b691ad9f` |
| `pulse-receipt` | `lib/forge-std/src/console2.sol` | `contracts/lib/forge-std/src/console2.sol` | `1ecdbbf7232c` |
| `pulse-receipt` | `lib/forge-std/src/interfaces/IERC1155.sol` | `contracts/lib/forge-std/src/interfaces/IERC1155.sol` | `9bf979dc5a51` |
| `pulse-receipt` | `lib/forge-std/src/interfaces/IERC165.sol` | `contracts/lib/forge-std/src/interfaces/IERC165.sol` | `fced182216c5` |
| `pulse-receipt` | `lib/forge-std/src/interfaces/IERC20.sol` | `contracts/lib/forge-std/src/interfaces/IERC20.sol` | `1a17fe134105` |
| `pulse-receipt` | `lib/forge-std/src/interfaces/IERC4626.sol` | `contracts/lib/forge-std/src/interfaces/IERC4626.sol` | `e63fce431826` |
| `pulse-receipt` | `lib/forge-std/src/interfaces/IERC6909.sol` | `contracts/lib/forge-std/src/interfaces/IERC6909.sol` | `d448b0fa3f8a` |
| `pulse-receipt` | `lib/forge-std/src/interfaces/IERC721.sol` | `contracts/lib/forge-std/src/interfaces/IERC721.sol` | `9a03145ac28b` |
| `pulse-receipt` | `lib/forge-std/src/interfaces/IERC7540.sol` | `contracts/lib/forge-std/src/interfaces/IERC7540.sol` | `3082c518187a` |
| `pulse-receipt` | `lib/forge-std/src/interfaces/IERC7575.sol` | `contracts/lib/forge-std/src/interfaces/IERC7575.sol` | `980f766ac23b` |
| `pulse-receipt` | `lib/forge-std/src/interfaces/IMulticall3.sol` | `contracts/lib/forge-std/src/interfaces/IMulticall3.sol` | `6a94133e0645` |
| `pulse-receipt` | `lib/forge-std/src/safeconsole.sol` | `contracts/lib/forge-std/src/safeconsole.sol` | `e12d0600a3fe` |
| `pulse-receipt` | `remappings.txt` | `contracts/remappings.txt` | `feaba2dd12ce` |
| `pulse-receipt` | `script/Deploy.s.sol` | `contracts/script/DeployPulseReceipt.s.sol` | `acd82e3ba1ff` |
| `pulse-receipt` | `src/PulseReceipt.sol` | `contracts/src/PulseReceipt.sol` | `c5858d26b164` |
| `pulse-receipt` | `test/PulseReceipt.t.sol` | `contracts/test/PulseReceipt.t.sol` | `22486907dfb1` |
