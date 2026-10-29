# modbus-simulator Releases

## v1.2.1

**Date:** 2026-10-29
**Previous release:** v1.2.0

### Summary

Dependency update for Modbus RTU over serial lines: go-serial v0.1.8 fixes two POSIX serial I/O bugs. No command or flag changes.

### Dependencies

- go-modbus v1.4.0 → v1.7.2, go-serial v0.1.6 → v0.1.8.

### Fixes (from go-serial v0.1.8)

- `Write` no longer loses data when the serial port's output buffer is
  full. It used to return a short count with a nil error (or `n == -1`
  with `EAGAIN`); it now blocks until every byte is written.
- `Read` works when the port's file descriptor is 1024 or higher. It used
  to return a spurious `io.EOF` at once.

### Other

- go-modbus v1.4.0 → v1.7.2 also brings the go-modbus fixes of v1.4.1–v1.7.2; see go-modbus's release notes.
- Documentation: security policy with security@otfabric.io.

---

## v1.2.0

**Date:** 2026-10-13  
**Previous release:** v1.1.0

### Summary

Two things in one release.

**Robustness.** A code review and new chaos, stress and fuzz test suites exposed defects in how the simulator behaves with concurrent clients, with clients that misbehave, and with requests outside the configured range. All of them are fixed; several fixes change behaviour a user can observe and are listed under **Behaviour changes**.

**Fewer limitations.** TLS, FC22 and FC23, broadcast, a control API to reset and reload a running simulator, per-connection state and PDU bytes in the trace: see **Added**. Existing fixtures and expectation files keep working.

### Added

- **TLS listener** — `run --tls-cert server.pem --tls-key server.key --tls-client-ca ca.pem` serves Modbus/TCP Security instead of plain TCP. All three are required: clients must present a certificate that chains to the client CA. The trace records `"transport": "tcp+tls"`.
- **FC22 (mask write register) and FC23 (read/write multiple registers)** on holding registers. FC23 makes the write first and then the read, as one step; when either is not allowed nothing is written. Rule matches accept `"fc": 22` and `23`; for FC23 an `address` matches when the read or the write covers it. The trace request has `andMask`/`orMask` for FC22 and `writeAddress`/`values` next to `address`/`quantity` for FC23. Two new example fixtures.
- **Broadcast** — a fixture with `"broadcast": true` treats unit `0` as the broadcast address: a write (FC 5, 6, 15, 16, 22) is executed by every device that can take it and **no response is sent**; any other request to unit 0 is ignored. The trace has one record with `"unitId": 0`, `"response": {"ok": true, "noResponse": true}` and a `unitId` on each state change. Off by default, where unit 0 stays an unknown unit.
- **Control API** — `run --control 127.0.0.1:5021` serves `POST /reset`, `POST /reset?unit=N`, `POST /reload` and `GET /healthz` over HTTP, so a test suite can start the simulator once and reset or reload it between cases. A fixture that is not valid is refused and the simulator carries on unchanged. No authentication: use a loopback address. These replace the deferred `resetViaControl` and `reloadFixture` state modes.
- **Real `timeout` and `connectionClose` faults** — see the behaviour changes.
- **PDU bytes in the trace** — `run --trace-raw` adds `"raw": {"request": "<hex>", "response": "<hex>"}` to each record. The PDUs are encoded from the decoded request and the returned response, not captured from the wire.

### Behaviour changes

- **`timeout` and `connectionClose` faults do what their names say.** A `timeout` rule leaves the request unanswered and keeps the connection open, so the client runs into its own timeout; a `connectionClose` rule closes the connection without a response. Both used to be answered with a *Server Device Failure* exception, which never exercised a client's timeout or reconnect handling. The trace is unchanged (`ok: false`, `fault`).
- **`stateMode: resetOnConnection` gives every connection a state of its own.** Each connection works on its own copy of the fixture values, discarded when it disconnects. It used to reset one shared state whenever a client sent its first request, which wiped the work of a client that was in the middle of a session, and a reconnect from the same address and port was taken for the old connection. A client now never sees another client's writes on such a device.

- **The trace is written in `seq` order, and that is the order in which requests took effect.** With concurrent clients, lines could appear in the file out of `seq` order, and neither order was guaranteed to match the order of the writes on the state. A state operation, its `seq` and its trace record are now one step, so `ordered` and `exact` assertions see the real history.
- **Every trace record is flushed as it is written.** The trace can be read while `run` is still serving, and survives an abrupt stop. Records used to stay in a buffer until shutdown.
- **A trace record that cannot be written is an error.** `run` exits with code `4` ("write trace file") when it stops; write errors used to be dropped silently.
- **`--max-clients 0` means no limit**, as documented. It was passed to go-modbus as is, where `0` selects the default of 10 clients, so the 11th client was disconnected.
- **An unknown `--format` is a usage error (exit `2`) for every command**, reported before any work is done. `validate` and `assert-trace` used to do the work first and then exit `10`.
- **The trace schema accepts `unitId` `0–255`** (was `1–247`). A request to a unit that no fixture can configure, such as 0 or 255, was answered correctly with `illegal_function`, but its trace record was rejected by the trace schema, so `assert-trace` refused the whole file. Fixtures still accept `1–247` only.
- **A read that runs past address 65535 is `illegal_data_address` under every `unknownAddress` policy.** With `zeroForReads` the store answered with zeroes. go-modbus already refuses such a request on the wire, so this was reachable only through the handler.

### Fixed

- **Client addresses were remembered forever** under `resetOnConnection`, one entry per connection ever made.
- **`uint64` codec values above 9223372036854775807 were rejected.** The full range up to 18446744073709551615 is accepted.
- **`make clean` deleted every `*.json` file in the repository root**, including fixtures and expectation files created by following the README.
- A missing `match` in an expectation evaluated through the Go API caused a nil dereference; it is now a failed assertion.

### Dependencies

- `github.com/otfabric/go-modbus` v1.1.0 → v1.4.0. v1.3.0 brings server fixes that apply here: a `delay` fault longer than the idle timeout no longer loses its response, and a handler is cancelled when its client disconnects. v1.4.0 adds `ErrNoResponse`, which broadcast needs.
- `github.com/santhosh-tekuri/jsonschema/v6` v6.0.2 → v6.0.3, `github.com/stretchr/testify` v1.11.1 → v1.12.1, `github.com/otfabric/go-serial` v0.1.3 → v0.1.6.
- `golang.org/x/text` v0.14.0 → v0.41.0, the newest release that builds with Go 1.25.

### Tests

- **Stress suite** (`stress_test.go`): parallel clients that check every answer against a model of their own registers while contending for a shared block, the handler called from many goroutines, connection churn, the client limit, and shutdown with requests in flight.
- **Chaos suite** (`chaos_test.go`): misbehaving raw TCP clients (garbage, truncated and oversized frames, random PDUs, illegal quantities, slow loris, pipelining, half-closed and abandoned connections) next to well-behaved ones, which must not notice.
- **Fuzz targets** (`fuzz_test.go`): fixtures, expectation files and arbitrary request sequences, including broadcasts.
- **Feature tests** (`features_test.go`): FC22 and FC23, broadcast in the handler and over TCP, isolation of `resetOnConnection` clients, `timeout` and `connectionClose` faults on the wire for every function code, the control API, a TLS round trip with certificates generated at run time, and the PDU encoding.
- After each run the trace must be valid against its schema, numbered `1, 2, 3, …` in file order, and replay as a sequential history of the fixture. **`make soak`** loops the chaos and stress suites (`SOAKTIME`, 2m by default) and **`make fuzz`** runs the fuzz targets (`FUZZTIME`, 30s each); a failing run logs its seed, and `MODBUS_SIMULATOR_TEST_SEED=<seed>` replays it.
- The command line (`cmd/`) is covered by tests for every command and exit code, and CI now runs them.

### Documentation

- README: badges corrected (the retired Go Report Card badge removed, Code Coverage and Go Reference added), install from release binaries, sections on state, reset and reload, broadcast, TLS and limitations, and the chaos, stress and fuzz tests.
- REQUIREMENTS.md: a table of the earlier decisions this release revises, the new features, the `codecs list` command and `--trace-decoded` flag, the `seq` ordering guarantee, `matchedRule` absent rather than `null`, and what `timeout` and `connectionClose` faults do on the wire.

### Install

```bash
go install github.com/otfabric/modbus-simulator@v1.2.0
```

Or download a pre-built binary from [GitHub Releases](https://github.com/otfabric/modbus-simulator/releases/tag/v1.2.0).

Requires Go 1.25+ when building from source.

### Breaking changes

None for fixtures, expectation files and traces: every v1.1.0 fixture and expectation file is valid, and so is a trace written by v1.1.0. What can affect an existing setup:

- A test that expected a *Server Device Failure* exception from a `timeout` or `connectionClose` rule: the client now gets no answer, or a closed connection.
- A test that relied on one client's connection resetting the state another client sees on a `resetOnConnection` device: see the first behaviour change.
- Scripts that relied on exit code `10` for an unknown `--format`, or on `--max-clients 0` limiting clients to 10.
- The trace schema types `raw` as an object (it was a string that was never written).

### Known limitations

- No RTU/serial server; the listener is TCP, plain or with TLS.
- Codec fields are register areas only (no coil/bit codecs). Trace `decoded[]` entries are omitted when a request only partially overlaps a multi-register codec field.
- `--trace-raw` PDUs are re-encoded rather than captured; the response of FC43 is not included.
- Control API actions are not written to the trace.

---

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
