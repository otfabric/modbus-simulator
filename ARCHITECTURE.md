# Architecture

`modbus-simulator` is one Go binary: a Modbus TCP device simulator driven by a JSON fixture. [REQUIREMENTS.md](REQUIREMENTS.md) is the specification; this file says how the code is arranged. Decisions and their reasons are in [docs/adr/](docs/adr/).

## Layers

```
main.go
  cmd/                     cobra commands, flags, exit codes, signal handling
    internal/simulator/    fixture, store, handler, trace, expectations, control API
      schemas/             embedded JSON Schemas
      go-modbus            Modbus TCP and TLS server, codecs
```

`tools/generate-codec-examples` generates the bundled codec fixtures in `examples/` (`make generate-codec-examples`). It is not part of the binary.

## Packages and main files

| Path | Responsibility |
|---|---|
| `cmd` | `run`, `validate`, `assert-trace`, `codecs list`, `version`, `completion`; maps errors to exit codes (`README.md`, Exit codes) |
| `internal/simulator/fixture.go`, `validate.go`, `schema.go` | Load a fixture; schema and semantic validation |
| `internal/simulator/codecfield.go` | go-modbus codec fields to and from registers |
| `internal/simulator/store.go` | `Store`: in-memory state of all devices, one per Unit ID, with `persistent` and `resetOnConnection` state |
| `internal/simulator/handler.go` | `Handler`: the go-modbus request handlers; applies the rule evaluation order and emits a trace record per request |
| `internal/simulator/rawpdu.go`, `trace.go` | Trace records and `JSONLWriter` |
| `internal/simulator/expect.go` | Expectation files and `assert-trace` |
| `internal/simulator/control.go` | The optional HTTP control API: `/reset`, `/reload`, `/healthz` |
| `internal/simulator/server.go` | `Run`: wires the above and blocks until the context is cancelled |
| `schemas` | The fixture and trace JSON Schemas, embedded |

## Request flow

`Run` validates the fixture, builds the `Store`, opens the trace file if one was asked for, and starts a go-modbus server with a `Handler`. All devices share one listener and are routed by Unit ID.

For each request, `Handler.serve` first evaluates configured exception and fault rules, then takes `commitMu` and, in one step, applies the state operation, assigns `seq` and writes the trace record. The rule evaluation order is specified in `REQUIREMENTS.md`, Rule evaluation order.

## Concurrency and ownership

- go-modbus runs the client connections and calls the handler concurrently.
- `Handler.commitMu` makes a state operation, its `seq` and its trace record one step, so the trace lists requests in the order in which they took effect. `ResetState` and `ReplaceStore` take the same lock.
- `Store.mu` guards the device state. `JSONLWriter.mu` guards the trace; every record is flushed as it is written, and the first write error is sticky and reported by `Close`.
- The control API runs in one goroutine started by `Run` and is closed when `Run` returns. `control.mu` allows one control request at a time. It has no authentication: bind it to a loopback address.
- `cmd` cancels the context on SIGINT or SIGTERM. `Run` then stops the server and closes the trace.

## Extending it

A new fixture or trace field changes `schemas/`, `REQUIREMENTS.md` and `README.md` together. Modbus transport work belongs in go-modbus; this repository has no RTU server (`README.md`, Limitations).
