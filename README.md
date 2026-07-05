# modbus-simulator

[![Go](https://img.shields.io/badge/Go-1.25+-00ADD8?style=flat&logo=go)](https://go.dev/)
[![License](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
[![Go Report Card](https://goreportcard.com/badge/github.com/otfabric/modbus-simulator)](https://goreportcard.com/report/github.com/otfabric/modbus-simulator)
[![CI](https://github.com/otfabric/modbus-simulator/actions/workflows/ci.yml/badge.svg)](https://github.com/otfabric/modbus-simulator/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/otfabric/modbus-simulator?label=release)](https://github.com/otfabric/modbus-simulator/releases)

Deterministic, stateful Modbus **TCP device simulator** for integration and end-to-end testing. Load a declarative JSON fixture, serve one or more Modbus devices from a single listener, optionally record a JSONL trace of every request, and assert traces in CI.

Repository: [github.com/otfabric/modbus-simulator](https://github.com/otfabric/modbus-simulator)

See [REQUIREMENTS.md](./REQUIREMENTS.md) for the full specification.

## Table of contents

- [Install](#install)
- [Quick start](#quick-start)
- [Commands](#commands)
  - [Global flags](#global-flags)
  - [`validate`](#validate)
  - [`run`](#run)
  - [`assert-trace`](#assert-trace)
  - [`version`](#version)
  - [`completion`](#completion)
  - [Exit codes](#exit-codes)
- [Artifacts](#artifacts)
- [Supported function codes (v1)](#supported-function-codes-v1)
- [Examples](#examples)
- [Development](#development)
- [License](#license)

## Install

Requires **Go 1.25+** to build from source.

```bash
go install github.com/otfabric/modbus-simulator@latest
```

Or build from source:

```bash
make build          # bin/modbus-simulator
make install        # copies to /usr/local/bin
```

## Quick start

Validate a fixture and start the simulator:

```bash
FIXTURE=examples/all-function-codes.json

modbus-simulator validate "$FIXTURE"
modbus-simulator run --fixture "$FIXTURE" --listen :1502
```

While the simulator runs, point your Modbus client or gateway at `tcp://127.0.0.1:1502` (unit ID `1`).

For trace recording and CI assertions, add `--trace trace.jsonl`, generate traffic, stop the server (Ctrl+C), then run `assert-trace` with an expectation file — see [`assert-trace`](#assert-trace) and [Artifacts](#artifacts).

Per–function-code fixtures are in [Examples](#examples). Command details and flags are in [Commands](#commands) below.

## Commands

```
modbus-simulator [--debug]
├── validate <fixture.json>
├── run
├── assert-trace <trace.jsonl>
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
| `--max-clients` | `MODBUS_SIMULATOR_MAX_CLIENTS` | `0` (unlimited) | Max concurrent clients |

#### Examples

```bash
# Serve on the default port (:5020)
modbus-simulator run --fixture device.json

# Custom listen address and trace output
modbus-simulator run --fixture device.json --listen :1502 --trace trace.jsonl

# Environment variables instead of flags
MODBUS_SIMULATOR_FIXTURE=device.json MODBUS_SIMULATOR_LISTEN=:5020 modbus-simulator run
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
├── fixture-v1.json    # device state, identity, behavior rules
├── trace-v1.json      # one JSON object per served request (JSONL)
└── expect-v1.json     # trace assertions (contains, count, ordered, exact, …)
```

- **Fixture** — declarative initial Modbus world; loaded once at startup.
- **Trace** — append-only execution log; optional output from `run --trace`.
- **Expectation** — subset matchers evaluated by `assert-trace`.

Schemas are [embedded](./schemas/embed.go) in the binary so `validate` and `assert-trace` work offline. Set `"$schema"` in your JSON to the published `$id` or a relative path (e.g. `"schemas/fixture-v1.json"`) for editor validation; the CLI selects the schema from `schemaVersion`, not from `$schema`.

## Supported function codes (v1)

| FC | Area |
|----|------|
| 1, 5, 15 | Coils |
| 2 | Discrete inputs (read-only) |
| 3, 6, 16 | Holding registers |
| 4 | Input registers (read-only) |
| 43 / 14 | Device identification |

TCP only in v1 (RTU server deferred). All devices in a fixture share one TCP listener and are routed by unit ID (`1–247`).

## Examples

Ready-to-run fixtures in [`examples/`](./examples/). Each fixture is exercised by a TCP integration test in `TestExamplesIntegration` (`make test`).

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
| [fc43-device-identification.json](./examples/fc43-device-identification.json) | 43/14 — Device identification | `modbus-simulator run --fixture examples/fc43-device-identification.json` |
| [all-function-codes.json](./examples/all-function-codes.json) | All of the above | `modbus-simulator run --fixture examples/all-function-codes.json` |

Each fixture uses unit ID `1` on the default listen address `:5020`. The `description` field in every file summarizes the registers or coils to exercise.

## Development

```bash
make help       # list targets
make test       # unit + race tests
make check      # fmt, lint, vet, test, coverage
make build      # build bin/modbus-simulator
make build-all  # cross-compile release binaries
```

Shell completion setup is documented under [`completion`](#completion).

## License

MIT License. Copyright (c) 2026 OTFabric. See [LICENSE](./LICENSE).
