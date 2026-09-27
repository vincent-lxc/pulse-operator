# Audit schema

Pulse writes one JSON object per decision to an **append-only JSONL** file
(`data/decisions.jsonl`). The file is write-once per `run_id` (`O_APPEND`;
duplicates are rejected).

## Record

| Field | Type | Notes |
| --- | --- | --- |
| `run_id` | string | Unique per decision (`run-<16 hex>`) |
| `agent_id` | string | Off-chain agent name; also copied into the on-chain `note` |
| `ts` | int64 | Unix seconds (UTC) |
| `steps` | array | Pipeline stages, in order |
| `final_action` | string | `buy` / `sell` / `hold` after gates |
| `action_code` | uint8 | `0` hold / `1` buy / `2` sell (PulseTradeStamp) |
| `size_hint` | uint64 | Notional hint in off-chain units |
| `symbol_id` | uint64 | CMC id or opaque key |
| `content_hash` | hex | keccak256 of the **canonical** payload |
| `decision_hash` | hex | Same value as `content_hash` |

`content_hash` / `decisionHash` **exclude** themselves. The hashed payload is
deterministic JSON (struct field order, `SetEscapeHTML(false)`, no trailing
newline):

`run_id`, `agent_id`, `ts`, `steps`, `final_action`, `action_code`, `size_hint`, `symbol_id`.

On Monad, `stamp(bytes32 decisionHash, …)` stores that 32-byte keccak.

## Step

| Field | Type | Notes |
| --- | --- | --- |
| `name` | string | `observe` `kronos` `plan` `risk` `jev` `execute` |
| `model_id` | string | e.g. `conservative-planner`, `hard-risk` |
| `model_version` | string | Pin or `stub` / `off` |
| `inputs_hash` | hex | keccak of that step's inputs |
| `outputs` | object | Short summary, not a full dump of market data |
| `risk` | object? | `{pass, reason}` on the hard-risk step |
| `jev` | object? | `{prompt, verdict, reason, model_id, questions, answers}` — I/O summary only; never an API key |

## Outcome store (`data/outcomes.jsonl`)

Keyed by `run_id`. Written after the sim/executor (or immediately on a gate
block).

| Field | Type | Notes |
| --- | --- | --- |
| `run_id` | string | Must match an audit line |
| `kind` | string | `pnl` or `gate_block` |
| `pnl` | number | Realized / paper PnL; ignored when `kind=gate_block` |
| `gate` | string | `risk` or `jev` when blocked |
| `reason` | string | Human-readable |
| `label` | string | See rules below |
| `recorded_at` | int64 | Unix seconds |

### Label rules

| Condition | Label | Weight update |
| --- | --- | --- |
| `kind=gate_block` | **negative** | shrink the firing rule |
| `kind=pnl` and `pnl > 0` | **positive** | grow the firing rule |
| `kind=pnl` and `pnl < 0` | **negative** | shrink the firing rule |
| `kind=pnl` and `pnl == 0` | **neutral** | no-op |

A blocked attempt is never neutral. Hard risk thresholds are **not** part of
this table: the feedback loop updates **rule weights only**. Proposed limit
changes are `ClampProposed` — same or stricter (smaller max position / daily
loss, longer cooldown, allowlist intersection). See `internal/policy`.

## What is *not* in the hash

- `content_hash` / `decision_hash` themselves
- The JSONL file name, byte offset, or previous-line hash
- The Monad tx hash (written by the stamp client after send)

Re-sealing a record must yield the same keccak. `go test ./internal/audit`
locks that in.
