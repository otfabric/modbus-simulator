# modbus-simulator

[![Go](https://img.shields.io/badge/Go-1.25%2B-00ADD8?style=flat&logo=go)](https://go.dev/)
[![Go Reference](https://pkg.go.dev/badge/github.com/otfabric/modbus-simulator.svg)](https://pkg.go.dev/github.com/otfabric/modbus-simulator)
[![License](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
[![CI](https://github.com/otfabric/modbus-simulator/actions/workflows/ci.yml/badge.svg)](https://github.com/otfabric/modbus-simulator/actions/workflows/ci.yml)
[![Code Coverage](https://codecov.io/gh/otfabric/modbus-simulator/graph/badge.svg)](https://codecov.io/gh/otfabric/modbus-simulator)
[![Release](https://img.shields.io/github/v/release/otfabric/modbus-simulator?label=release)](https://github.com/otfabric/modbus-simulator/releases)

Deterministic, stateful Modbus **TCP device simulator** for integration and end-to-end testing. Load a declarative JSON fixture (raw registers or [go-modbus](https://github.com/otfabric/go-modbus) codec fields with typed values), serve one or more Modbus devices from a single listener (plain TCP or TLS), optionally record a JSONL trace of every request, and assert traces in CI.

Repository: [github.com/otfabric/modbus-simulator](https://github.com/otfabric/modbus-simulator)

See [REQUIREMENTS.md](./REQUIREMENTS.md) for the full specification.

## Table of contents

- 📦 [Install](#install)
- 🚀 [Quick start](#quick-start)
- ⌨️ [Commands](#commands)
  - 🌐 [Global flags](#global-flags)
  - ✅ [`validate`](#validate)
  - ▶️ [`run`](#run)
  - 🔢 [`codecs list`](#codecs-list)
  - 🔍 [`assert-trace`](#assert-trace)
  - ℹ️ [`version`](#version)
  - 🐚 [`completion`](#completion)
  - 🚦 [Exit codes](#exit-codes)
- 📄 [Artifacts](#artifacts)
- 🧩 [Codec register fields](#codec-register-fields)
- 📡 [Supported function codes](#supported-function-codes)
- 🧠 [State, reset and reload](#state-reset-and-reload)
- 💥 [Fault injection](#fault-injection)
- 📣 [Broadcast](#broadcast)
- 🔒 [TLS](#tls)
- ⚠️ [Limitations](#limitations)
- 📚 [Examples](#examples)
- 🛠️ [Development](#development)
- 📜 [License](#license)

## Install

Download a pre-built binary for Linux, macOS or Windows from [GitHub Releases](https://github.com/otfabric/modbus-simulator/releases), or install with Go (**1.25+**):

```bash
go install github.com/otfabric/modbus-simulator@latest
```

Or build from a checkout:

```bash
make build          # lint + test, then bin/modbus-simulator
make install        # copies to /usr/local/bin (uses sudo)
```

`make build` runs the full `make check` first and needs [staticcheck](https://staticcheck.dev/) and [golangci-lint](https://golangci-lint.run/) v2 on `PATH`. For a plain build without them: `go build -o bin/modbus-simulator .`

## Quick start

Validate a fixture and start the simulator:

```bash
FIXTURE=examples/all-function-codes.json

modbus-simulator validate "$FIXTURE"
modbus-simulator run --fixture "$FIXTURE" --listen :1502
```

While the simulator runs, point your Modbus client or gateway at `tcp://127.0.0.1:1502` (unit ID `1`).

For trace recording and CI assertions, add `--trace trace.jsonl`, generate traffic, stop the server (Ctrl+C), then run `assert-trace` with an expectation file — see [`assert-trace`](#assert-trace) and [Artifacts](#artifacts).

Per–function-code and codec fixtures are in [Examples](#examples). Command details and flags are in [Commands](#commands) below.

## Commands

```
modbus-simulator [--debug]
├── validate <fixture.json>
├── run
├── assert-trace <trace.jsonl>
├── codecs list
├── version
└── completion [bash|zsh|fish|powershell]
```

### Global flags

| Flag | Default | Description |
|------|---------|-------------|
| `--debug` | `false` | Print debug information (applies to all commands) |

### `validate`

Validate a fixture against the embedded JSON Schema and semantic rules.

#### Flags

| Flag | Environment variable | Default | Description |
|------|---------------------|---------|-------------|
| `--format` | `MODBUS_SIMULATOR_FORMAT` | `text` | Output format: `text` or `json` |

#### Examples

```bash
# Exit 0 on success, 3 on validation failure
modbus-simulator validate device.json

# Machine-readable summary
modbus-simulator validate device.json --format json
```

### `run`

Start the stateful Modbus TCP simulator from a fixture. Runs until interrupted (SIGINT/SIGTERM).

#### Flags

| Flag | Environment variable | Default | Description |
|------|---------------------|---------|-------------|
| `--fixture` | `MODBUS_SIMULATOR_FIXTURE` | *(required)* | Path to fixture JSON |
| `--listen` | `MODBUS_SIMULATOR_LISTEN` | `:5020` | TCP listen address (`host:port`) |
| `--trace` | `MODBUS_SIMULATOR_TRACE` | *(none)* | JSONL trace output path |
| `--trace-decoded` | `MODBUS_SIMULATOR_TRACE_DECODED` | `false` | Include typed codec decode overlay in trace records |
| `--trace-raw` | `MODBUS_SIMULATOR_TRACE_RAW` | `false` | Include the request and response PDU bytes (hex) in trace records |
| `--max-clients` | `MODBUS_SIMULATOR_MAX_CLIENTS` | `0` (unlimited) | Max concurrent clients; a client over the limit is disconnected |
| `--control` | `MODBUS_SIMULATOR_CONTROL` | *(disabled)* | HTTP control API address (`host:port`) — see [State, reset and reload](#state-reset-and-reload) |
| `--tls-cert` | `MODBUS_SIMULATOR_TLS_CERT` | *(none)* | Server certificate (PEM) — see [TLS](#tls) |
| `--tls-key` | `MODBUS_SIMULATOR_TLS_KEY` | *(none)* | Server private key (PEM) |
| `--tls-client-ca` | `MODBUS_SIMULATOR_TLS_CLIENT_CA` | *(none)* | CA certificates (PEM) that client certificates must chain to |

#### Examples

```bash
# Serve on the default port (:5020)
modbus-simulator run --fixture device.json

# Custom listen address and trace output
modbus-simulator run --fixture device.json --listen :1502 --trace trace.jsonl

# Trace with typed codec decode overlay and the PDU bytes
modbus-simulator run --fixture examples/codec-floats.json --trace trace.jsonl --trace-decoded --trace-raw

# Control API for reset and reload
modbus-simulator run --fixture device.json --control 127.0.0.1:5021

# TLS with client certificates
modbus-simulator run --fixture device.json --tls-cert server.pem --tls-key server.key --tls-client-ca ca.pem

# Environment variables instead of flags
MODBUS_SIMULATOR_FIXTURE=device.json MODBUS_SIMULATOR_LISTEN=:5020 modbus-simulator run
```

### `codecs list`

List [go-modbus](https://github.com/otfabric/go-modbus) codec stable IDs and metadata. Use these IDs in fixture `codec` fields.

#### Flags

| Flag | Environment variable | Default | Description |
|------|---------------------|---------|-------------|
| `--family` | *(none)* | *(none)* | Filter by family: `integer`, `float`, `text`, `bcd`, `bytes`, `network`, `hardware_address`, `decimal_limb`, `time`, … |
| `--format` | `MODBUS_SIMULATOR_FORMAT` | `text` | Output format: `text` or `json` |

#### Examples

```bash
modbus-simulator codecs list
modbus-simulator codecs list --family float --format json
```

### `assert-trace`

Assert a JSONL trace against an expectation file (`modbus-simulator.expect.v1`).

#### Flags

| Flag | Environment variable | Default | Description |
|------|---------------------|---------|-------------|
| `--expect` | `MODBUS_SIMULATOR_EXPECT` | *(required)* | Path to expectation JSON |
| `--format` | `MODBUS_SIMULATOR_FORMAT` | `text` | Output format: `text` or `json` |

#### Examples

```bash
# Exit 8 if any assertion fails
modbus-simulator assert-trace trace.jsonl --expect expected.json

# Machine-readable report
modbus-simulator assert-trace trace.jsonl --expect expected.json --format json
```

### `version`

Print build metadata (version, tag, commit, build date).

#### Flags

None.

#### Examples

```bash
modbus-simulator version
```

### `completion`

Generate shell completion scripts for `bash`, `zsh`, `fish`, or `powershell`.

#### Flags

None (shell name is a required positional argument).

#### Examples

```bash
# Bash
source <(modbus-simulator completion bash)

# Zsh
source <(modbus-simulator completion zsh)

# Fish
modbus-simulator completion fish | source

# PowerShell
modbus-simulator completion powershell | Out-String | Invoke-Expression
```

### Exit codes

| Code | Meaning |
|------|---------|
| `0` | Success |
| `2` | Usage / flag error |
| `3` | Validation error (invalid fixture, trace, or expectation) |
| `4` | Transport / server error |
| `8` | Assertion failure |
| `10` | Internal error |

## Artifacts

Three JSON artifacts drive the workflow:

| Artifact | `schemaVersion` | Schema file | Published `$id` |
|----------|-----------------|-------------|------------------|
| Fixture | `modbus-simulator.fixture.v1` | [schemas/fixture-v1.json](./schemas/fixture-v1.json) | `https://otfabric.dev/schemas/modbus-simulator/fixture-v1.json` |
| Trace (JSONL) | `modbus-simulator.trace.v1` | [schemas/trace-v1.json](./schemas/trace-v1.json) | `https://otfabric.dev/schemas/modbus-simulator/trace-v1.json` |
| Expectation | `modbus-simulator.expect.v1` | [schemas/expect-v1.json](./schemas/expect-v1.json) | `https://otfabric.dev/schemas/modbus-simulator/expect-v1.json` |

```
schemas/
├── fixture-v1.json    # device state, identity, behavior (raw + codec register points)
├── trace-v1.json      # one JSON object per served request (JSONL)
└── expect-v1.json     # trace assertions (contains, count, ordered, exact, …)
```

- **Fixture** — declarative initial Modbus world; raw uint16/bool points or codec-backed
  multi-register fields with typed values. Semantic validation uses the go-modbus codec
  registry (see `validate` and `codecs list`).
- **Trace** — append-only execution log; optional output from `run --trace`.
- **Expectation** — subset matchers evaluated by `assert-trace`.

Schemas are [embedded](./schemas/embed.go) in the binary so `validate` and `assert-trace` work offline. Set `"$schema"` in your JSON to the published `$id` or a relative path (e.g. `"schemas/fixture-v1.json"`) for editor validation; the CLI selects the schema from `schemaVersion`, not from `$schema`.

## Codec register fields

Register points in a fixture can use raw unsigned integers (`0–65535`, one register each) **or** a go-modbus **codec** with a typed JSON value. The simulator encodes codec values to wire registers at load time; FC handling stays register-native on the wire.

**Raw point:**

```json
{ "address": 100, "value": 2301, "writable": false, "name": "raw_counter" }
```

**Codec point:**

```json
{
  "address": 10,
  "codec": "float32/layout:4321",
  "value": 230.5,
  "writable": true,
  "name": "voltage"
}
```

| Field | Meaning |
|-------|---------|
| `codec` | Stable ID from [go-modbus/codec](https://github.com/otfabric/go-modbus/blob/main/CODECS.md) (e.g. `float32/layout:4321`, `ascii/registers:4`, `ip_addr`, `datetime2_s2000`) |
| `value` | Typed JSON matching the codec (numbers, strings, byte arrays, RFC3339 times, IP/MAC strings) |
| span | Derived from the codec register count — do not specify `count`; overlapping spans in one area are rejected |

Discover IDs with `modbus-simulator codecs list`. Validation runs JSON Schema plus semantic encode/decode round-trip checks. Optional trace overlay: `run --trace-decoded` adds a `decoded[]` field when a read/write fully covers a codec field.

## Supported function codes

| FC | Area |
|----|------|
| 1, 5, 15 | Coils |
| 2 | Discrete inputs (read-only) |
| 3, 6, 16, 22, 23 | Holding registers |
| 4 | Input registers (read-only) |
| 43 / 14 | Device identification |

- **FC22 (mask write)** sets the register to `(current AND andMask) OR (orMask AND NOT andMask)`.
- **FC23 (read/write multiple)** makes the write first, then the read, as one step. If either is not allowed, nothing is written.

All devices in a fixture share one listener and are routed by unit ID (`1–247`). A request for a unit ID that is not in the fixture is answered with an `illegal_function` exception.

## State, reset and reload

A device's `stateMode` (set in `defaults` or per device in `behavior`) decides who sees a write:

| `stateMode` | State |
|-------------|-------|
| `persistent` (default) | One state shared by all clients, for as long as the simulator runs. |
| `resetOnConnection` | Every client connection works on its own copy of the fixture values, discarded when it disconnects. Clients do not see each other's writes, and a new connection never disturbs one that is in progress. |

With `run --control <host:port>` a running simulator also takes commands over HTTP, so a test suite can start it once and reset it between cases:

| Request | Effect |
|---------|--------|
| `POST /reset` | Restore every unit to the fixture values, for all clients |
| `POST /reset?unit=N` | Restore one unit (`404` when it is not in the fixture) |
| `POST /reload` | Read the fixture file again and serve it from then on. A fixture that is not valid is refused with `422` and the simulator carries on unchanged |
| `GET /healthz` | `200` while the simulator runs |

```bash
modbus-simulator run --fixture device.json --control 127.0.0.1:5021 &
curl -X POST http://127.0.0.1:5021/reset
curl -X POST http://127.0.0.1:5021/reload
```

Replies are JSON (`{"ok":true}`, or `{"ok":false,"error":"…"}`). The control API has **no authentication**: bind it to a loopback address. Resets and reloads are not written to the trace; `seq` simply continues.

## Fault injection

Fault rules in a device's `behavior.faults` change how a matching request is answered (see [REQUIREMENTS.md](./REQUIREMENTS.md#fault-rules-optional-lower-priority-than-exceptions-for-v1) for the match grammar):

| `fault` | On the wire | In the trace |
|---------|-------------|--------------|
| `delay` | The normal response, after `delayMs` | `ok: true`, `matchedRule` |
| `timeout` | **No response**; the connection stays open, so the client runs into its own timeout. `delayMs`, when set, is waited first | `ok: false`, `fault: "timeout"` |
| `connectionClose` | The connection is **closed** without a response | `ok: false`, `fault: "connectionClose"` |

```json
{ "id": "hr-100-timeout", "match": { "fc": 3, "address": 100 }, "fault": "timeout" }
```

## Broadcast

Set `"broadcast": true` at the top level of a fixture to make unit `0` the broadcast address, as on a serial line behind a gateway:

- A write to unit 0 (FC 5, 6, 15, 16, 22) is executed by every device that can take it; a device where the address is unknown or read-only ignores it. **No response is sent.**
- Any other request to unit 0 is ignored and left unanswered.
- The trace has one record with `"unitId": 0` and `"response": {"ok": true, "noResponse": true}`; each entry of `stateChanges` names the `unitId` it changed.
- Exception and fault rules belong to a device and are not applied to a broadcast.

Without it (the default), unit 0 is an unknown unit and is answered with `illegal_function`.

## TLS

Give `run` a server certificate, its key and a client CA to serve Modbus/TCP Security instead of plain TCP:

```bash
modbus-simulator run --fixture device.json --listen :8020 \
  --tls-cert server.pem --tls-key server.key --tls-client-ca ca.pem
```

All three are required: clients must present a certificate that chains to `--tls-client-ca` (mutual TLS). The trace records `"transport": "tcp+tls"`.

## Limitations

- **No RTU/serial server.** The listener is TCP, plain or with TLS.
- **Codec fields are register areas only** (no coil/bit codecs). Writes need not align with codec field boundaries; `decoded[]` (`--trace-decoded`) lists only fields fully covered by the request.
- **`--trace-raw` PDUs are re-encoded**, not captured: the request as the simulator decoded it and the response it returned, encoded as the specification prescribes. Padding bits of an FC15 request are zero, and the response of FC43 is not included.

See [RELEASE.md](./RELEASE.md) for per-release notes.

## Examples

Ready-to-run fixtures in [`examples/`](./examples/) — **21 total** (12 function-code, 9 codec bundles). Each file has a TCP integration test in `TestExamplesIntegration` (`make test`). Codec bundles are checked against `modbus-simulator codecs list` by `TestExamples_AllCodecsHaveExample` (195 IDs).

**Function codes**

| Fixture | FC | Try it |
|---------|-----|--------|
| [fc01-read-coils.json](./examples/fc01-read-coils.json) | 1 — Read coils | `modbus-simulator run --fixture examples/fc01-read-coils.json` |
| [fc02-read-discrete-inputs.json](./examples/fc02-read-discrete-inputs.json) | 2 — Read discrete inputs | `modbus-simulator run --fixture examples/fc02-read-discrete-inputs.json` |
| [fc03-read-holding-registers.json](./examples/fc03-read-holding-registers.json) | 3 — Read holding registers | `modbus-simulator run --fixture examples/fc03-read-holding-registers.json` |
| [fc04-read-input-registers.json](./examples/fc04-read-input-registers.json) | 4 — Read input registers | `modbus-simulator run --fixture examples/fc04-read-input-registers.json` |
| [fc05-write-single-coil.json](./examples/fc05-write-single-coil.json) | 5 — Write single coil | `modbus-simulator run --fixture examples/fc05-write-single-coil.json` |
| [fc06-write-single-register.json](./examples/fc06-write-single-register.json) | 6 — Write single register | `modbus-simulator run --fixture examples/fc06-write-single-register.json` |
| [fc15-write-multiple-coils.json](./examples/fc15-write-multiple-coils.json) | 15 — Write multiple coils | `modbus-simulator run --fixture examples/fc15-write-multiple-coils.json` |
| [fc16-write-multiple-registers.json](./examples/fc16-write-multiple-registers.json) | 16 — Write multiple registers | `modbus-simulator run --fixture examples/fc16-write-multiple-registers.json` |
| [fc22-mask-write-register.json](./examples/fc22-mask-write-register.json) | 22 — Mask write register | `modbus-simulator run --fixture examples/fc22-mask-write-register.json` |
| [fc23-read-write-multiple-registers.json](./examples/fc23-read-write-multiple-registers.json) | 23 — Read/write multiple registers | `modbus-simulator run --fixture examples/fc23-read-write-multiple-registers.json` |
| [fc43-device-identification.json](./examples/fc43-device-identification.json) | 43/14 — Device identification | `modbus-simulator run --fixture examples/fc43-device-identification.json` |
| [all-function-codes.json](./examples/all-function-codes.json) | 1–6, 15, 16 and 43 in one fixture | `modbus-simulator run --fixture examples/all-function-codes.json` |

**Codecs** — nine bundled fixtures cover every codec from `modbus-simulator codecs list` (195 IDs). Each file lists one example per family member with non-overlapping addresses.

| Fixture | Family | Codecs | Try it |
|---------|--------|--------|--------|
| [codec-integers.json](./examples/codec-integers.json) | integer | 29 | `modbus-simulator validate examples/codec-integers.json` |
| [codec-floats.json](./examples/codec-floats.json) | float | 8 | `modbus-simulator validate examples/codec-floats.json` |
| [codec-text.json](./examples/codec-text.json) | text | 60 | `modbus-simulator validate examples/codec-text.json` |
| [codec-bcd.json](./examples/codec-bcd.json) | bcd | 48 | `modbus-simulator validate examples/codec-bcd.json` |
| [codec-bytes.json](./examples/codec-bytes.json) | bytes | 26 | `modbus-simulator validate examples/codec-bytes.json` |
| [codec-decimal-limb.json](./examples/codec-decimal-limb.json) | decimal_limb | 12 | `modbus-simulator validate examples/codec-decimal-limb.json` |
| [codec-datetime.json](./examples/codec-datetime.json) | time | 8 | `modbus-simulator validate examples/codec-datetime.json` |
| [codec-network.json](./examples/codec-network.json) | network | 2 | `modbus-simulator validate examples/codec-network.json` |
| [codec-hardware.json](./examples/codec-hardware.json) | hardware_address | 2 | `modbus-simulator validate examples/codec-hardware.json` |

Regenerate bundled examples after adding codecs: `make generate-codec-examples`

Each fixture uses unit ID `1` on the default listen address `:5020`. The `description` field in every file summarizes the registers or coils to exercise.

## Development

```bash
make help                      # list targets
make test                      # unit + race tests (includes example fixtures)
make check                     # fmt, lint, vet, test, coverage
make soak                      # loop the chaos and stress tests (SOAKTIME=2m)
make fuzz                      # run the fuzz targets (FUZZTIME=30s each)
make build                     # build bin/modbus-simulator
make build-all                 # cross-compile release binaries
make generate-codec-examples   # regenerate bundled codec fixtures
```

Shell completion setup is documented under [`completion`](#completion).

### Chaos, stress and fuzz tests

`make test` includes a short run of all of them:

- **Stress** (`stress_test.go`) — parallel clients check every answer against a model of their own registers while they all contend for a shared block; connection churn, the client limit and shutdown with requests in flight.
- **Chaos** (`chaos_test.go`) — misbehaving raw TCP clients (garbage, truncated and oversized frames, random PDUs, illegal quantities, slow loris, pipelining, half-closed and abandoned connections) run next to the well-behaved ones, which must not notice.
- **Fuzz** (`fuzz_test.go`) — fixtures, expectation files and arbitrary request sequences.

After each run the trace must be schema-valid, numbered `1, 2, 3, …` in file order, and replay as a sequential history: every read returns what the earlier writes left behind. Randomized tests log their seed; replay one with `MODBUS_SIMULATOR_TEST_SEED=<seed>`.

## License

MIT License. Copyright (c) 2026 OTFabric. See [LICENSE](./LICENSE).
