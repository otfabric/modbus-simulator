# modbus-simulator — Requirements

## Objective

`modbus-simulator` is a standalone, deterministic, stateful Modbus **device simulator**
used for automated integration and end-to-end testing of the Modbus API Gateway.

The simulator emulates one or more Modbus devices, maintains runtime state in memory,
exposes configurable device identity and register maps, and produces deterministic
execution traces that can be asserted in CI.

The simulator models Modbus *protocol behavior*; it does not replay historical
measurements or discovery captures.

Data-format identifiers use the `modbus-simulator.*` prefix
(`modbus-simulator.fixture.v1`, `modbus-simulator.trace.v1`, `modbus-simulator.expect.v1`).
The optional `$schema` URLs under `otfabric.dev/schemas/modbus-simulator/…` are advisory;
validation uses embedded schemas selected by `schemaVersion`.

---

## Adversarial review — resolved inconsistencies

The following contradictions and gaps in the original draft have been resolved in this
document. They are listed so reviewers can see the decisions that were made.

| # | Issue in original draft | Resolution |
|---|-------------------------|------------|
| 1 | "serves over TCP/RTU" but go-modbus server is TCP-only | **v1 is TCP (and TCP+TLS) only.** RTU server is deferred; noted as a library gap. |
| 2 | "deterministic execution traces" vs wall-clock `ts`/`durationMs` | Determinism is defined over responses, state changes, and sequence order. `ts`/`durationMs` are informational and **excluded** from assertions. |
| 3 | "State Reset" listed 4 modes but `stateMode` enum had only 2 | v1 `stateMode` enum = `persistent`, `resetOnConnection`. `resetViaControl` and `reloadFixture` are **deferred** (no control channel in v1). |
| 4 | `defaults` (top-level) and `behavior` (per-device) both held `unknownAddress`/`stateMode` with no precedence | Per-device `behavior` **overrides** top-level `defaults`. `exceptions`/`faults` are per-device only. |
| 5 | Exceptions promised "address range" matching, but match schema had only single `address` | Match supports `address` **or** `addressRange` (mutually exclusive), plus `fc`, `area`, `quantity`. |
| 6 | `matchedRule` was a free string with no way to correlate to a fixture rule | Exception/fault rules take an optional `id`; `matchedRule` in the trace echoes that `id` (or a reserved policy label). |
| 7 | `delay` listed as a "fault" alongside `timeout`, but a delay still succeeds | `delay` produces a **successful** response after a latency; `timeout`/`connectionClose` produce failure records. |
| 8 | "exactly one trace record per request" vs faults with no response | **Every request that reaches a device produces exactly one trace record**, including timeout/close faults (recorded with `ok:false`). |
| 9 | FC43 named identity fields vs `objects` array — overlap undefined | Named fields map to objects `0x00/0x01/0x02`; `objects` holds additional ids. Duplicate ids are rejected. |
| 10 | Assertion spec had two incompatible shapes (`expect` array vs `contains/ordered/exact/...`) | Unified into one `modbus-simulator.expect.v1` grammar (see §Assertions). Matching is **subset/partial**. |
| 11 | `stateChange` (singular, expectation) vs `stateChanges` (plural, trace) | An expectation `stateChange` matches **any** element of the trace record's `stateChanges` array. |
| 12 | Fixture shown in YAML in some places, JSON in others | Fixtures, traces, and expectations are **JSON/JSONL only**. |
| 13 | `writable` allowed on all areas, but discrete inputs/input registers are read-only | `writable` is **forbidden by schema** on `discreteInputs` and `inputRegisters`. |
| 14 | `$schema` pointed at a hosted URL; validation must work offline in CI | Schemas are **embedded** (`go:embed`) and validated locally. `$schema` is optional and never fetched. |
| 15 | Implementation order wrote the JSONL writer before defining trace structs | Reordered: structs and schema precede the writer (see §Implementation Plan). |
| 16 | `seq` scope undefined for multiple connections | `seq` is a single monotonic counter **per simulator process**, starting at 1. |
| 17 | simulator run command had no listen/port flag defined | Flags defined below (`--listen`, `--fixture`, `--trace`, `--max-clients`) with `MODBUS_SIMULATOR_*` env fallbacks. |
| 18 | Exit codes for validate / assert commands unspecified | Defined: validation = `3`; assertion failure = dedicated code `8`; transport = `4`. |

Round-2 review corrections (accepted with minor changes):

| # | Review point | Resolution |
|---|--------------|------------|
| 19 | Unit ID `0` (broadcast) is ambiguous for a gateway simulator | `unitId` range is now **`1–247`**; unit `0` is rejected. Broadcast is explicitly deferred. |
| 20 | `unknownAddress: zero` auto-creating points on write is too magical for CI | Renamed to **`zeroForReads`**: unknown **reads** return `0`/`false`; unknown **writes** are `illegal_data_address` in *both* modes. |
| 21 | FC43 behavior for present-identity-but-missing-object undefined | Documented: absent identity → `illegal_function`; present identity → return only the available objects per FC43 semantics. |
| 22 | Precedence of configured rules vs built-in validation undefined | Added explicit **Rule Evaluation Order** (exceptions → faults → built-in validation → normal operation). |
| 23 | `delay` fault didn't surface in the trace | `delay` now sets `matchedRule` to its rule `id` even though the response is `ok:true`. |
| 24 | `exact` assertion could be read as byte-for-byte JSON equality | Clarified: `exact` = exact record count + ordered one-to-one matcher coverage using subset field matching; `ts`/`durationMs`/`raw` ignored. |
| 25 | `stateChanges` was optional in the trace | `stateChanges` is now **required** in every record (empty array when none). |
| 26 | Consumers had to map FC→area themselves | Added normalized **`request.area`** to every request record. |
| 27 | Exception/fault rule fields lacked semantic validation | Added: `addressRange.start <= end`, `quantity > 0` when present, and (trace/request) `values` non-empty for FC15/FC16. |
| 28 | Command placement (top-level `fixture`/`trace` vs nested) | The simulator is a **dedicated `modbus-simulator` binary** exposing `run`/`validate`/`assert-trace` at the top level. |

Later revisions (v1.2.0) of decisions above:

| # | Earlier decision | Now |
|---|------------------|-----|
| 1 | TCP+TLS optional, to be enabled later | **Implemented**: `run --tls-cert/--tls-key/--tls-client-ca` (see §Transport). RTU stays deferred. |
| 3 | `resetViaControl` and `reloadFixture` deferred for lack of a control channel | **Implemented as control actions**, not as `stateMode` values: `run --control` (see §Control API). |
| 3 | `resetOnConnection` resets the shared state when a client connects | **State per connection**: each connection works on its own copy of the fixture values (see §Behavior section). |
| 19 | Broadcast deferred; unit `0` rejected | **Opt-in**: a fixture with `"broadcast": true` executes writes to unit `0` on every device (see §Broadcast). A fixture `unitId` is still `1–247`. |
| — | FC22 and FC23 deferred | **Implemented** (see §Supported Function Codes). |
| — | Trace `raw` reserved | **Implemented**: `run --trace-raw` (see §Record shape). |

---

## High-Level Architecture

Three artifacts are introduced:

1. **Fixture** — declarative, human-authored, JSON-Schema-validated, versioned
   description of the initial simulated Modbus world. Loaded once at startup.
2. **Simulator** — an in-memory Modbus TCP server that loads a fixture and serves
   requests with mutable runtime state.
3. **Trace** — an append-only, machine-generated JSONL execution log. Every served
   request produces exactly one record. Traces can later be asserted.

Commands — as a dedicated tool, the simulator exposes its verbs at the top level:

```
modbus-simulator validate <fixture.json>
modbus-simulator run --fixture <fixture.json> [--listen :5020] [--trace trace.jsonl]
modbus-simulator assert-trace <trace.jsonl> --expect <expected.json>
```

```
modbus-simulator
├── run          start the stateful TCP simulator from a fixture
├── validate     validate a fixture (schema + semantic checks)
├── assert-trace assert a generated trace against expectations
└── codecs list  list go-modbus codec IDs usable in fixture `codec` fields
```

Because the simulator is a dedicated binary (resolution #28), these verbs live at the
top level; future subcommands (e.g. `dump-state`, `reset`, `export-fixture`) can be
added next to them.

---

## Transport (v1)

- **TCP** (`tcp://`) is required.
- **TCP+TLS** (Modbus/TCP Security) is served when `run` is given `--tls-cert`,
  `--tls-key` and `--tls-client-ca` (PEM files; all three are required). Clients must
  present a certificate that chains to the client CA. The trace then records
  `conn.transport` as `tcp+tls`.
- **RTU / serial server is out of scope for v1**: `github.com/otfabric/go-modbus`
  provides only TCP/TLS server transports. RTU server support would require new
  transport work and is explicitly deferred.

All devices in a fixture are served on a **single TCP listener** and routed by Unit ID
(the go-modbus server exposes `UnitID` on every handler request).

---

## Supported Function Codes

### Required (v1)

| FC | Description | Area / behavior |
|----|-------------|-----------------|
| 1 | Read Coils | `coils` read |
| 2 | Read Discrete Inputs | `discreteInputs` read (read-only) |
| 3 | Read Holding Registers | `holdingRegisters` read |
| 4 | Read Input Registers | `inputRegisters` read (read-only) |
| 5 | Write Single Coil | `coils` write |
| 6 | Write Single Holding Register | `holdingRegisters` write |
| 15 | Write Multiple Coils | `coils` write |
| 16 | Write Multiple Holding Registers | `holdingRegisters` write |
| 22 | Mask Write Register | `holdingRegisters` write: `(current AND andMask) OR (orMask AND NOT andMask)` |
| 23 | Read/Write Multiple Registers | `holdingRegisters` write, then read |
| 43 / 14 | Read Device Identification (MEI 0x0E) | device `identity` |

FC5/FC15 and FC6/FC16 are distinguished in the trace via the request `fc` field
(the library provides `FunctionCode` on each handler request).

FC23 is one step: the write is made first, then the read, so the read sees the values
just written. Both are checked before anything is written; if the write targets an
unknown or read-only register, or the read is not allowed by the `unknownAddress`
policy, the request fails with `illegal_data_address` and nothing changes.

In a rule `match`, `fc` may be `22` or `23`. For FC23 `address`/`addressRange` match when
the read **or** the write covers the address, and `quantity` is the number of registers
read.

---

## Design Principles

The simulator prioritizes:

- deterministic responses, state changes, and sequence ordering
- protocol correctness
- reproducibility and CI-friendliness
- human-readable fixtures, machine-readable traces
- strict schema validation, minimal hidden behavior

Separation of concerns:

- The **fixture** describes what the simulated device *is* (initial state).
- The **simulator** maintains what the device *currently contains* (mutable state).
- The **trace** records everything that *happened*.

> **Determinism note.** Given the same fixture and the same ordered sequence of requests,
> the simulator always produces the same responses, `stateChanges`, `matchedRule` values,
> and `seq` ordering. The `ts` and `durationMs` fields reflect real wall-clock time and
> are **not** deterministic; assertions must never depend on them.

---

## Fixture Specification

### General rules

The fixture shall:

- be JSON
- carry an explicit, versioned schema (`schemaVersion`)
- validate against an embedded JSON Schema
- reject unknown fields (`"additionalProperties": false`) everywhere except designated
  free-form `metadata` objects
- support future schema evolution via new `schemaVersion` values

Schema version identifier: `modbus-simulator.fixture.v1`. The version must **not** be inferred
from the file name.

### Top-level shape

```json
{
  "$schema": "https://otfabric.dev/schemas/modbus-simulator/fixture-v1.json",
  "schemaVersion": "modbus-simulator.fixture.v1",
  "name": "gateway-integration-test-device",
  "description": "optional free text",
  "defaults": {
    "unknownAddress": "exception",
    "stateMode": "persistent"
  },
  "metadata": {},
  "devices": []
}
```

- **Required:** `schemaVersion`, `devices` (non-empty).
- **Optional:** `$schema`, `name`, `description`, `defaults`, `broadcast`, `metadata`.
- `$schema` is advisory only and is **never fetched over the network**; validation uses
  the embedded schema selected by `schemaVersion`.
- `metadata` is the only object with `"additionalProperties": true`.

### Device model

```json
{
  "unitId": 1,
  "name": "inverter-a",
  "metadata": {},
  "identity": {},
  "state": {},
  "behavior": {}
}
```

- **Required:** `unitId`, `state`.
- **Optional:** `name`, `metadata`, `identity`, `behavior`.
- Validation:
  - `unitId`: integer `1–247`; must be unique across `devices`. Unit `0` (the Modbus
    broadcast address) is **rejected** as a device id; see §Broadcast for requests to it.
  - `name`: string.

`behavior` fields override the top-level `defaults` for that device. `exceptions` and
`faults` are per-device only and are not present in `defaults`.

### State model (four memory areas)

```json
{
  "state": {
    "coils": [],
    "discreteInputs": [],
    "holdingRegisters": [],
    "inputRegisters": []
  }
}
```

| Area | Read FC | Write FC | Value type |
|------|---------|----------|------------|
| `coils` | 1 | 5, 15 | boolean |
| `discreteInputs` | 2 | — (read-only) | boolean |
| `holdingRegisters` | 3 | 6, 16 | integer `0–65535`, or a typed codec value |
| `inputRegisters` | 4 | — (read-only) | integer `0–65535`, or a typed codec value |

All four keys are optional; an omitted area is empty.

### Register entry format

Array-based entries (arrays validate better with JSON Schema and allow per-point metadata).

Coils / discrete inputs:

```json
{ "address": 0, "value": true, "writable": true }
```

Holding / input registers (raw uint16):

```json
{ "address": 100, "value": 2301, "writable": false, "name": "grid_voltage_l1", "description": "Voltage L1 in decivolts" }
```

Holding / input registers (codec-backed):

```json
{
  "address": 200,
  "codec": "float32/layout:4321",
  "value": 230.5,
  "writable": true,
  "name": "grid_voltage_l1"
}
```

Validation rules:

- `address`: integer `0–65535`. For raw register points: one register. For codec points:
  span is derived from the codec `RegisterSpec().Count`; spans must not overlap within
  an area.
- `value`: boolean for `coils`/`discreteInputs`; integer `0–65535` for raw register
  points; typed JSON for codec points (numbers, strings, byte arrays, RFC3339 times,
  IP/MAC strings) matching the codec `ValueKind`.
- `codec`: optional go-modbus stable ID on register points; must resolve via
  `codec.RuntimeCodecByID`. Semantic validation encodes and round-trip decodes the value.
- `writable`: boolean. **Allowed only on `coils` and `holdingRegisters`.** The schema
  rejects `writable` on `discreteInputs` and `inputRegisters`.
- `name`, `description`: optional strings.
- `datatype` and per-point `metadata` are reserved and not accepted.

Codec discovery: `modbus-simulator codecs list` wraps `codec.AvailableCodecDescriptors()`.

**Trace decode overlay (optional):** `modbus-simulator run --trace-decoded` adds an
optional `decoded[]` array to trace records when codec fields are fully contained in
the request window. Documented in [`schemas/trace-v1.json`](./schemas/trace-v1.json).

### Writable rules

- Default `writable`:
  - `coils`: `true`
  - `holdingRegisters`: `true`
  - `discreteInputs`, `inputRegisters`: always read-only (not configurable)
- A write targeting a read-only address returns exception `illegal_data_address`.
  (This is the single deterministic choice; `illegal_data_value` is not used for this case.)

### Device identity (FC43)

```json
{
  "identity": {
    "vendorName": "OTFabric",
    "productCode": "GWTEST",
    "majorMinorRevision": "1.0.0",
    "objects": [
      { "id": 128, "value": "custom test object" }
    ]
  }
}
```

- If `identity` is present, `vendorName`, `productCode`, and `majorMinorRevision` are
  required. These map to standard objects `0x00`, `0x01`, `0x02` respectively.
- `objects[]` supplies additional identity objects. `id` is an integer `0–255`; `value`
  is a string. Ids `0x00–0x02` **must not** appear in `objects` (they are set by the
  named fields); duplicate ids are rejected. Vendor-specific objects use `0x80–0xFF`.
- FC43/14 behavior:
  - If `identity` is **absent**, the device returns `illegal_function` for FC43.
  - If `identity` is **present** but a requested object id is not configured, the simulator
    returns only the available objects per normal FC43 semantics (stream reads return the
    configured objects for the requested category; an individual read of a missing object
    returns no object rather than fabricating one). It does **not** return `illegal_function`
    in this case.

### Behavior section

```json
{
  "behavior": {
    "unknownAddress": "exception",
    "stateMode": "persistent",
    "exceptions": [],
    "faults": []
  }
}
```

- `unknownAddress`: `exception` (default) or `zeroForReads`.
  - `exception`: reads and writes to an address not present in the fixture return
    `illegal_data_address`.
  - `zeroForReads`: reads to unknown addresses return `0`/`false`. **Writes to unknown
    addresses are `illegal_data_address` in both modes** — the simulator never
    auto-creates points, so a mis-mapped gateway write fails loudly instead of silently
    growing the register map.
- `stateMode`: `persistent` (default) or `resetOnConnection`.
  - `persistent`: runtime state persists for the lifetime of the process.
  - `resetOnConnection`: every client connection works on its own copy of the fixture's
    initial values, created when the connection is first served and discarded when it
    ends. Clients do not see each other's writes, and a new connection does not disturb
    one that is in progress.
  - Resetting and reloading on demand are control actions, not state modes: see
    §Control API.

### Broadcast

```json
{ "schemaVersion": "modbus-simulator.fixture.v1", "broadcast": true, "devices": [] }
```

- `broadcast` (top level, boolean, default `false`). When `false`, unit `0` is an unknown
  unit: `illegal_function`.
- When `true`, unit `0` is the broadcast address, as on a serial line behind a gateway:
  - a write (FC5, FC6, FC15, FC16, FC22) is executed by every device, in ascending unit
    order. Each device takes the write as a whole or, when one of the addresses is
    unknown or read-only for it, ignores it;
  - any other request is ignored;
  - **no response is sent** in either case, and the connection stays open.
- Exception and fault rules are per device and are not applied to a broadcast.
- Trace: one record with `unitId` `0` and `response` `{ "ok": true, "noResponse": true }`
  (`ok: false` for an ignored request). Each `stateChanges` entry carries the `unitId` of
  the device it changed.

### Exception rules

Deterministic, matchable protocol exceptions.

```json
{
  "id": "hr-999-illegal",
  "match": { "fc": 3, "area": "holdingRegisters", "address": 999, "quantity": 1 },
  "exceptionCode": "illegal_data_address"
}
```

- `id`: optional string; echoed as the trace `matchedRule`. If omitted the simulator
  synthesizes a stable id (`exception[<index>]`).
- `match` fields (all optional; **all present fields must match** for the rule to fire):
  - `fc`: one of the supported FCs.
  - `area`: one of the four area names.
  - `address`: integer `0–65535`, **or**
  - `addressRange`: `{ "start": <int>, "end": <int> }` with `start <= end` — matches if any
    requested address falls in `[start, end]`. `address` and `addressRange` are mutually
    exclusive.
  - `quantity`: integer `> 0`; exact match on the request quantity.
- `exceptionCode`: one of `illegal_function`, `illegal_data_address`, `illegal_data_value`,
  `slave_device_failure`.
- Rules are evaluated in array order; the first match wins and short-circuits.
- Configured-exception matches are recorded in the trace `matchedRule` field.
- Semantic validation (`modbus-simulator validate`): `addressRange.start <= addressRange.end`,
  `quantity > 0` when present, `fc` restricted to supported codes.

### Fault rules (optional; lower priority than exceptions for v1)

Faults model transport/runtime behavior, not Modbus exceptions.

```json
{
  "id": "hr-100-timeout",
  "match": { "fc": 3, "area": "holdingRegisters", "address": 100 },
  "fault": "timeout",
  "delayMs": 500
}
```

- `match`: same grammar and semantic validation as exception `match`.
- `fault`:
  - `delay`: wait `delayMs`, then serve the request **normally** (response `ok:true`). The
    trace still records `matchedRule` (the rule `id`) so latency behavior is debuggable.
  - `timeout`: wait `delayMs` (if set), send **no** response to the client, then record the
    transaction with `{ "ok": false, "fault": "timeout" }`.
  - `connectionClose`: close the client connection without responding; record
    `{ "ok": false, "fault": "connectionClose" }`.
- `delayMs`: integer milliseconds (`>= 0`).
- All faults set the trace `matchedRule` to the rule `id`, regardless of `ok`.

> **On the wire.** `timeout` sends nothing and keeps the connection open: the client runs
> into its own timeout, and a request it sent after the unanswered one is still served.
> `connectionClose` closes the connection without sending a byte. Until v1.2.0 both were
> answered with a *Server Device Failure* exception.

### Rule evaluation order

Each request is resolved in this fixed order; the first stage that produces an outcome
wins. Configured rules are evaluated **before** built-in validation so tests can force
specific exceptions/faults on otherwise-valid addresses.

1. **Configured exception rules** (array order, first match wins) → Modbus exception.
2. **Configured fault rules** (array order, first match wins) → transport fault
   (`delay` continues to step 3–4 after waiting; `timeout`/`connectionClose` terminate).
3. **Built-in validation** — unknown-address policy (`unknownAddress`) and writable/area
   checks (writes to read-only areas or unknown addresses → `illegal_data_address`).
4. **Normal state operation** — read returns current values; write mutates state and emits
   `stateChanges`.

---

## Runtime State

- The simulator maintains all device state in memory.
- Writes permanently modify simulator memory until the applicable reset (`stateMode`).
- Example: `FC6 register 100 = 42` → a subsequent `FC3 register 100` returns `42`.
- Handlers may be invoked concurrently (one goroutine per client connection); state access
  and trace writing must be synchronized.

---

## Trace Specification

The trace is an append-only JSONL execution log, independent of the fixture format.

- append-only, machine-generated, one JSON object per line
- **exactly one record per served request**, including exception and fault outcomes
- produced by `modbus-simulator run --trace <file>`; if `--trace` is omitted, no
  trace is written

### Record shape

```json
{
  "schemaVersion": "modbus-simulator.trace.v1",
  "ts": "2026-07-05T10:00:00.123Z",
  "seq": 1,
  "conn": { "transport": "tcp", "remote": "127.0.0.1:54012" },
  "unitId": 1,
  "request": {},
  "response": {},
  "stateChanges": [],
  "matchedRule": "hr-999-illegal",
  "durationMs": 2
}
```

- **Required:** `schemaVersion`, `ts`, `seq`, `unitId`, `request`, `response`,
  `stateChanges`, `durationMs`. `stateChanges` is always present (empty array for reads,
  FC43, exceptions, and faults) so consumers never branch on its presence.
- **Optional:** `conn`, `matchedRule`, `decoded`, `raw`.
- `response.noResponse` is `true` when no response was sent (a broadcast).
- `seq`: monotonic counter per simulator process, starting at 1, incremented once per
  served request across all connections. Records are written in `seq` order, which is the
  order in which the requests took effect on the state, also with concurrent clients.
- `unitId`: the unit the request was addressed to, `0–255`. A unit that is not in the
  fixture is answered with `illegal_function` and still traced.
- `matchedRule`: the `id` of the exception/fault rule that fired, the string
  `"unknownAddress"` when the `unknownAddress` policy produced the outcome, or absent
  when neither applied (the schema types it as a string; the writer never emits `null`).
- `decoded`: typed codec overlay, present only with `run --trace-decoded`.
- `raw`: `{ "request": "<hex>", "response": "<hex>" }`, present only with
  `run --trace-raw`. The PDUs (function code and data, without the MBAP header) are
  encoded from the decoded request and the returned response as the specification
  prescribes; they are not a capture of the wire. `response` is absent when none was sent
  and for FC43.

### Request format

Every request record carries a normalized `area` (derived from the FC) so consumers and
assertion files never need to reimplement FC→area mapping. FC43 uses `area: "identity"`.

```jsonl
{ "fc": 3, "name": "read_holding_registers", "area": "holdingRegisters", "address": 0, "quantity": 2 }
{ "fc": 6, "name": "write_single_register", "area": "holdingRegisters", "address": 1, "value": 777 }
{ "fc": 16, "name": "write_multiple_registers", "area": "holdingRegisters", "address": 10, "values": [100, 200, 300] }
{ "fc": 22, "name": "mask_write_register", "area": "holdingRegisters", "address": 4, "andMask": 242, "orMask": 37 }
{ "fc": 23, "name": "read_write_multiple_registers", "area": "holdingRegisters", "address": 9, "quantity": 4, "writeAddress": 10, "values": [200, 300] }
{ "fc": 43, "name": "read_device_identification", "area": "identity", "meiType": 14, "readDeviceIdCode": 1, "objectId": 0 }
```

For FC23, `address` and `quantity` describe the read, `writeAddress` and `values` the
write; the response carries the `values` read.

`values` (FC15/FC16) must be non-empty; the writer never emits an empty `values` array and
`modbus-simulator validate`/`modbus-simulator assert-trace` reject one.

### Response format

```jsonc
{ "ok": true, "values": [2301, 501] }         // read (registers: ints; coils/discrete: booleans)
{ "ok": true }                                 // successful write
{ "ok": false, "exceptionCode": "illegal_data_address" }
{ "ok": false, "fault": "timeout" }
```

FC43 response:

```json
{
  "ok": true,
  "identity": {
    "vendorName": "OTFabric",
    "productCode": "GWTEST",
    "majorMinorRevision": "1.0.0",
    "objects": [ { "id": 128, "value": "integration-test-device" } ]
  }
}
```

### State change format

Writes record one entry per affected address in `stateChanges`; reads and FC43 leave
`stateChanges` empty (`[]`).

```jsonl
{ "area": "holdingRegisters", "address": 1, "before": 501, "after": 777 }
{ "area": "coils", "address": 0, "before": false, "after": true }
```

Multi-writes (FC15/FC16) produce one entry per address, in ascending address order.

### Example trace (JSONL)

```jsonl
{"schemaVersion":"modbus-simulator.trace.v1","ts":"2026-07-05T10:00:00.100Z","seq":1,"conn":{"transport":"tcp","remote":"127.0.0.1:54012"},"unitId":1,"request":{"fc":3,"name":"read_holding_registers","area":"holdingRegisters","address":0,"quantity":2},"response":{"ok":true,"values":[2301,501]},"stateChanges":[],"durationMs":2}
{"schemaVersion":"modbus-simulator.trace.v1","ts":"2026-07-05T10:00:01.000Z","seq":2,"conn":{"transport":"tcp","remote":"127.0.0.1:54012"},"unitId":1,"request":{"fc":6,"name":"write_single_register","area":"holdingRegisters","address":1,"value":777},"response":{"ok":true},"stateChanges":[{"area":"holdingRegisters","address":1,"before":501,"after":777}],"durationMs":1}
{"schemaVersion":"modbus-simulator.trace.v1","ts":"2026-07-05T10:00:02.000Z","seq":3,"conn":{"transport":"tcp","remote":"127.0.0.1:54012"},"unitId":1,"request":{"fc":3,"name":"read_holding_registers","area":"holdingRegisters","address":1,"quantity":1},"response":{"ok":true,"values":[777]},"stateChanges":[],"durationMs":1}
{"schemaVersion":"modbus-simulator.trace.v1","ts":"2026-07-05T10:00:03.000Z","seq":4,"conn":{"transport":"tcp","remote":"127.0.0.1:54012"},"unitId":1,"request":{"fc":43,"name":"read_device_identification","area":"identity","meiType":14,"readDeviceIdCode":1,"objectId":0},"response":{"ok":true,"identity":{"vendorName":"OTFabric","productCode":"GWTEST","majorMinorRevision":"1.0.0","objects":[{"id":128,"value":"integration-test-device"}]}},"stateChanges":[],"durationMs":1}
{"schemaVersion":"modbus-simulator.trace.v1","ts":"2026-07-05T10:00:04.000Z","seq":5,"conn":{"transport":"tcp","remote":"127.0.0.1:54012"},"unitId":1,"request":{"fc":3,"name":"read_holding_registers","area":"holdingRegisters","address":999,"quantity":1},"response":{"ok":false,"exceptionCode":"illegal_data_address"},"stateChanges":[],"matchedRule":"hr-999-illegal","durationMs":1}
```

---

## Assertions

Trace assertions enable deterministic integration tests without requiring exact
timestamps or byte-for-byte equality. They answer questions such as:

- Gateway wrote register 100
- Gateway polled a register N times
- Gateway requested FC43
- Gateway never accessed address 999
- Gateway observed updated values after writes

### Matching model

- Assertions match against trace records using **subset (partial) matching**: only the
  fields present in the expectation are compared; all other trace fields are ignored.
- `ts`, `durationMs`, and `raw` are never matched (non-deterministic / transport detail).
- A `request`/`response` object in an expectation matches a record when every field it
  specifies equals the corresponding record field. `request.area` may be used directly.
- A `stateChange` (singular) in an expectation matches a record when **any** element of
  that record's `stateChanges` array matches all specified fields (e.g. `after` alone).

### Expectation file (`modbus-simulator.expect.v1`)

A single top-level `assertions` array; each entry is exactly one assertion of a supported
kind. This is the one canonical shape (the earlier `expect`-array form is subsumed by
`ordered`/`contains`).

```json
{
  "schemaVersion": "modbus-simulator.expect.v1",
  "assertions": [
    {
      "kind": "contains",
      "match": { "request": { "fc": 43 } }
    },
    {
      "kind": "notContains",
      "match": { "request": { "address": 999 } }
    },
    {
      "kind": "count",
      "match": { "request": { "fc": 3, "address": 0 } },
      "equals": 3
    },
    {
      "kind": "ordered",
      "sequence": [
        {
          "request": { "fc": 6, "address": 1, "value": 777 },
          "response": { "ok": true },
          "stateChange": { "area": "holdingRegisters", "address": 1, "after": 777 }
        },
        {
          "request": { "fc": 3, "address": 1, "quantity": 1 },
          "response": { "ok": true, "values": [777] }
        }
      ]
    }
  ]
}
```

### Assertion kinds

| kind | payload | passes when |
|------|---------|-------------|
| `contains` | `match` | at least one trace record matches `match` |
| `notContains` | `match` | no trace record matches `match` |
| `count` | `match` + one of `equals`/`atLeast`/`atMost` | number of matching records satisfies the bound |
| `ordered` | `sequence[]` | the listed matchers occur in the trace in the given relative order (not necessarily contiguous) |
| `exact` | `sequence[]` | the trace record count equals `len(sequence)` and record *i* matches matcher *i*, in order, with no extra or missing records |

`match`/`sequence[]` entries use the same `{ request, response, stateChange }` matcher
described in *Matching model*.

> **`exact` is not byte-for-byte JSON equality.** It asserts two things: (1) the trace has
> exactly as many records as the sequence, and (2) each record satisfies its positional
> subset matcher. Fields not named in a matcher — and always `ts`/`durationMs`/`raw` — are
> ignored. Use `exact` when a test must pin the complete request order; use `ordered` when
> extra records are acceptable.

### `assert-trace` semantics

- Reads a JSONL trace and a `modbus-simulator.expect.v1` file (both schema-validated).
- Evaluates all assertions and reports each pass/fail with context.
- Exit codes:
  - `0`: all assertions passed.
  - validation error (malformed trace/expect, schema violation): exit `3`.
  - assertion failure: dedicated exit code `8`, distinct from validation (`3`) and
    transport (`4`).

---

## CLI Details

### `modbus-simulator validate <fixture.json>`

- Validates the fixture against the embedded `modbus-simulator.fixture.v1` schema **and** the
  semantic rules above: `unitId` in `1–247` and unique; unique addresses (coils, discrete
  inputs) and non-overlapping register spans per area; codec id, value and round-trip checks;
  `writable` only on writable areas; identity object id rules; mutually-exclusive
  `address`/`addressRange` with `start <= end`; `quantity > 0` when present; supported
  FCs only in rule matches (1–6, 15, 16, 22, 23, 43).
- Supports `--format text|json`.
- Exit `0` on success, `3` on any validation failure.

### `modbus-simulator run`

| Flag | Env | Default | Meaning |
|------|-----|---------|---------|
| `--fixture` | `MODBUS_SIMULATOR_FIXTURE` | (required) | path to fixture JSON |
| `--listen` | `MODBUS_SIMULATOR_LISTEN` | `:5020` | TCP listen address `host:port` |
| `--trace` | `MODBUS_SIMULATOR_TRACE` | (none) | trace JSONL output path; omitted → no trace |
| `--trace-decoded` | `MODBUS_SIMULATOR_TRACE_DECODED` | `false` | add the typed codec `decoded[]` overlay to trace records |
| `--trace-raw` | `MODBUS_SIMULATOR_TRACE_RAW` | `false` | add the request and response PDU bytes to trace records |
| `--max-clients` | `MODBUS_SIMULATOR_MAX_CLIENTS` | `0` (unlimited) | max concurrent clients |
| `--control` | `MODBUS_SIMULATOR_CONTROL` | (disabled) | HTTP control API address `host:port` |
| `--tls-cert` | `MODBUS_SIMULATOR_TLS_CERT` | (none) | server certificate, PEM |
| `--tls-key` | `MODBUS_SIMULATOR_TLS_KEY` | (none) | server private key, PEM |
| `--tls-client-ca` | `MODBUS_SIMULATOR_TLS_CLIENT_CA` | (none) | CA that client certificates must chain to, PEM |

- Validates the fixture before binding the listener (same rules as `modbus-simulator validate`).
- Runs until the context is cancelled (SIGINT/SIGTERM).
- Startup/progress messages go to stderr; the trace goes only to the `--trace` file.
- Each trace record is flushed as it is written, so the file can be read while `run` is
  still serving. A record that could not be written makes `run` exit with code `4`.

- The three `--tls-*` flags must be given together (usage error, exit `2`, otherwise). A
  file that cannot be loaded makes `run` exit with code `4`.

#### Control API

With `--control`, `run` serves a small HTTP API for as long as it runs. It has no
authentication and is meant for a loopback address.

| Request | Effect | Status |
|---------|--------|--------|
| `POST /reset` | restore every unit to the fixture values, for every connection | `200` |
| `POST /reset?unit=N` | restore one unit | `200`; `404` unknown unit; `400` not a unit id |
| `POST /reload` | read the fixture file again, validate it and serve it; all state is dropped | `200`; `422` when the fixture is not valid — nothing changes then |
| `GET /healthz` | liveness | `200` |

- Replies are JSON: `{ "ok": true }` (plus `unit` or a `fixture` summary), or
  `{ "ok": false, "error": "…" }`. Another method on a path is `405`.
- A reset or reload takes effect between two requests, never in the middle of one.
- Control actions are not written to the trace; `seq` continues. A test that resets
  between cases can find the boundary by the requests it sends itself.

### `modbus-simulator assert-trace <trace.jsonl> --expect <expected.json>`

- Flags: `--expect` (required, env `MODBUS_SIMULATOR_EXPECT`), `--format text|json`.
- Exit codes as defined in *`assert-trace` semantics*.

### `modbus-simulator codecs list`

- Lists the go-modbus codec stable IDs with name, family, value kind and register/byte
  counts (`codec.AvailableCodecDescriptors()`), sorted by ID.
- Flags: `--family <name>` (filter), `--format text|json` (env `MODBUS_SIMULATOR_FORMAT`).

---

## Example Fixture

```json
{
  "$schema": "https://otfabric.dev/schemas/modbus-simulator/fixture-v1.json",
  "schemaVersion": "modbus-simulator.fixture.v1",
  "name": "gateway-integration-test",
  "defaults": { "unknownAddress": "exception", "stateMode": "persistent" },
  "devices": [
    {
      "unitId": 1,
      "name": "inverter-a",
      "identity": {
        "vendorName": "OTFabric",
        "productCode": "GWTEST",
        "majorMinorRevision": "1.0.0",
        "objects": [ { "id": 128, "value": "integration-test-device" } ]
      },
      "state": {
        "coils": [ { "address": 0, "value": true, "writable": true } ],
        "discreteInputs": [ { "address": 0, "value": false } ],
        "holdingRegisters": [
          { "address": 0, "value": 2301, "writable": false },
          { "address": 1, "value": 501, "writable": true }
        ],
        "inputRegisters": [ { "address": 0, "value": 1234 } ]
      },
      "behavior": {
        "unknownAddress": "exception",
        "exceptions": [
          {
            "id": "hr-999-illegal",
            "match": { "fc": 3, "area": "holdingRegisters", "address": 999 },
            "exceptionCode": "illegal_data_address"
          }
        ]
      }
    }
  ]
}
```

---

## Implementation Plan

Implemented in dependency order (structs and schemas precede the code that emits them).

### Phase 1 — Fixture model and validation
1. Add fixture Go structs in `internal/simulator`, mirroring the schema; all objects tagged
   for strict decoding.
2. Author the embedded JSON Schema (`modbus-simulator.fixture.v1`) via `go:embed`.
3. Add a JSON Schema validator dependency (`github.com/santhosh-tekuri/jsonschema/v6`)
   and a validation helper that runs schema + semantic checks.
4. Wire `modbus-simulator validate` (top-level `cmd/` command); validation errors → exit 3.
5. Tests: valid fixtures pass; each semantic rule (unit `0` rejected, dup unit id, dup
   address, `writable` on read-only area, identity id conflicts, unsupported FC,
   `address`+`addressRange`, `start > end`, `quantity <= 0`) fails with a clear message.

### Phase 2 — Stateful simulator core
6. Build an in-memory device store (four areas per unit id, writable flags, `unknownAddress`
   policy, `stateMode`), safe for concurrent access.
7. Implement a fixture loader that initializes the store from a fixture.
8. Implement a go-modbus `RequestHandler` (+ `DeviceIdentificationHandler`) that routes by
   `UnitID`, distinguishes read/write via `IsWrite`+`FunctionCode`, and resolves each
   request in the fixed **rule evaluation order** (exceptions → faults → built-in
   validation → normal operation), returning library sentinel errors for exceptions.
9. Tests: read/write/read round-trip proves persistence; read-only + unknown-address writes
   rejected in both modes (no auto-create); `zeroForReads` reads; rule precedence
   (configured exception overrides an otherwise-valid address); `delay` still records
   `matchedRule`; `resetOnConnection`.

### Phase 3 — Trace
10. Add trace Go structs and the embedded `modbus-simulator.trace.v1` schema (`stateChanges`
    required, `request.area` required, non-empty `values` for FC15/FC16).
11. Implement a synchronized JSONL trace writer (monotonic `seq`, one record per request,
    including faults; `matchedRule` set for every configured exception/fault including
    `delay`) integrated into the handler.
12. Wire `modbus-simulator run` (`cmd/run.go`) with the flags above, fixture validation
    before bind, and context-cancel lifecycle.
13. Tests: served requests produce well-formed, schema-valid records; write records carry
    correct `stateChanges`; exception records carry `matchedRule`.

### Phase 4 — Assertions
14. Add expectation Go structs and the embedded `modbus-simulator.expect.v1` schema.
15. Implement the subset matcher and the five assertion kinds (`contains`, `notContains`,
    `count`, `ordered`, `exact`).
16. Wire `modbus-simulator assert-trace` with a dedicated exit code `8` for failures.
17. Tests: each kind passes and fails as expected; `ts`/`durationMs` never affect results.

### Phase 5 — End-to-end
18. Integration test proving the core scenario end-to-end against a real TCP client
    (`go-modbus` client): fixture sets holding register 1 = 501 → client writes 777 →
    client reads back 777 → trace + `modbus-simulator assert-trace` confirm the value and the
    state change.
19. Docs/README updates and shell-completion registration for the commands.

### First behavior to prove
```
fixture initializes holding register 1 = 501
client writes holding register 1 = 777
client reads holding register 1
trace proves the returned value is 777
```
