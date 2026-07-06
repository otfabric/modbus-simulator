# modbus-simulator Releases

## v1.1.0

**Date:** 2026-07-06  
**Previous release:** v1.0.0

### Summary

Codec-aware fixtures: write typed values in JSON instead of hand-encoding Modbus registers. Still `modbus-simulator.fixture.v1` — raw uint16/bool points remain fully supported.

- **Codec register fields** — optional `codec` (go-modbus stable ID) and typed `value` on holding/input register points. Register span is derived from the codec; values are encoded to wire registers at load time.
- **Two-tier validation** — JSON Schema (`oneOf` raw vs codec point) plus semantic checks: codec registry lookup, value coercion, encode/decode round-trip, non-overlapping address spans.
- **`codecs list`** — discover available codec IDs and metadata (`--family`, `--format json`). Wraps `github.com/otfabric/go-modbus/codec`.
- **`run --trace-decoded`** — optional `decoded[]` overlay on trace records when codec fields are fully contained in the request window.
- **Examples** — nineteen fixtures in [`examples/`](./examples/): ten function-code fixtures plus nine codec **bundles** (one per go-modbus codec family: integer, float, text, bcd, bytes, decimal_limb, time, network, hardware_address). Together they cover every stable ID from `codecs list` (195 codecs) with non-overlapping addresses and round-trip-validated sample values. Regenerate bundles with `make generate-codec-examples` (`tools/generate-codec-examples`). TCP integration tests exercise each fixture; `TestExamples_AllCodecsHaveExample` guards full codec coverage.

### Fixture codec example

```json
{
  "schemaVersion": "modbus-simulator.fixture.v1",
  "devices": [{
    "unitId": 1,
    "state": {
      "holdingRegisters": [
        { "address": 10, "codec": "float32/layout:4321", "value": 230.5, "name": "voltage" }
      ]
    }
  }]
}
```

Run `modbus-simulator codecs list` for all stable IDs. See [go-modbus CODECS.md](https://github.com/otfabric/go-modbus/blob/main/CODECS.md) for layout and value-kind details.

### Example fixtures (v1.1.0)

| Fixture | Family | Codecs |
|---------|--------|--------|
| `codec-integers.json` | integer | 29 |
| `codec-floats.json` | float | 8 |
| `codec-text.json` | text | 60 |
| `codec-bcd.json` | bcd | 48 |
| `codec-bytes.json` | bytes | 26 |
| `codec-decimal-limb.json` | decimal_limb | 12 |
| `codec-datetime.json` | time | 8 |
| `codec-network.json` | network | 2 |
| `codec-hardware.json` | hardware_address | 2 |

Function-code fixtures (`fc01`–`fc43`, `all-function-codes.json`) are unchanged from v1.0.0.

### Install

```bash
go install github.com/otfabric/modbus-simulator@v1.1.0
```

Or download a pre-built binary from [GitHub Releases](https://github.com/otfabric/modbus-simulator/releases/tag/v1.1.0).

Requires Go 1.25+ when building from source. Depends on `github.com/otfabric/go-modbus` v1.1.0+ for the codec registry.

### Breaking changes

None. Existing v1 fixtures with raw integer register values validate and run unchanged.

### Known limitations

Unchanged from v1.0.0 unless noted:

- TCP only; RTU/serial server and TCP+TLS listen are deferred.
- `timeout` and `connectionClose` fault rules are recorded in traces but approximated on the wire.
- `resetViaControl` and `reloadFixture` state modes are deferred.
- Codec fields are register areas only (no coil/bit codecs). FC16 writes need not align to codec field boundaries; traces still emit per-register `stateChanges`.
- Trace `decoded[]` entries are omitted when a request only partially overlaps a multi-register codec field.

---

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
- Register values in fixtures were raw unsigned integers only (no typed codecs — added in v1.1.0).
