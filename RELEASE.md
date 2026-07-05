# modbus-simulator Releases

## v1.0.0

**Date:** 2026-07-05  
**Previous release:** none

### Summary

First release of the Modbus TCP device simulator for gateway integration and CI testing.

- **Fixture-driven simulation** — `modbus-simulator.fixture.v1` JSON defines one or more devices (coils, discrete inputs, holding/input registers, FC43 identity, exception/fault rules).
- **Stateful runtime** — writes persist in memory; optional `resetOnConnection` per device.
- **JSONL traces** — `run --trace` records `modbus-simulator.trace.v1` logs with deterministic `seq`, responses, and `stateChanges`.
- **CI assertions** — `assert-trace` evaluates `modbus-simulator.expect.v1` files (`contains`, `notContains`, `count`, `ordered`, `exact`).
- **CLI** — `validate`, `run`, `assert-trace`, `version`, and shell `completion`.
- **Schemas** — canonical JSON Schemas in [`schemas/`](./schemas/) (`fixture-v1.json`, `trace-v1.json`, `expect-v1.json`), embedded in the binary for offline validation.
- **Examples** — ten ready-to-run fixtures in [`examples/`](./examples/) (one per supported FC plus an all-in-one fixture), each covered by TCP integration tests.
- **License** — MIT ([LICENSE](./LICENSE)).

### Install

Download the binary for your platform from [GitHub Releases](https://github.com/otfabric/modbus-simulator/releases/tag/v1.0.0), or:

```bash
go install github.com/otfabric/modbus-simulator@v1.0.0
```

Requires Go 1.25+ when building from source.

Pre-built binaries are published for `linux/amd64`, `linux/arm64`, `linux/arm/v7`, `darwin/amd64`, `darwin/arm64`, `windows/amd64`, and `windows/arm64`.

### Breaking changes

None (initial release).

### Known limitations (v1)

- TCP only; RTU/serial server and TCP+TLS listen are deferred.
- `timeout` and `connectionClose` fault rules are recorded in traces but approximated on the wire (go-modbus handler constraints).
- `resetViaControl` and `reloadFixture` state modes are deferred.
