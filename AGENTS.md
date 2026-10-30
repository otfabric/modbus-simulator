# AGENTS.md — modbus-simulator

## What this is

A deterministic, stateful Modbus TCP device simulator for integration and end-to-end testing: it loads a declarative JSON fixture, serves one or more devices from one listener (plain TCP or TLS), can record a JSONL trace of every request, and asserts traces in CI. Latest release: see `RELEASE.md`. Profile: application.

## Organisation standards

This repository follows **otfabric/engineering policy 1.1**, profile **application** (`.otfabric/policy.yaml`). Organisation skills come from the `otfabric` plugin in the maintainer's user settings. Where this file and the organisation standards disagree, this file wins for this repository; say so in your report.

## Where the truth lives

| Question | Source of truth |
|---|---|
| What the simulator must do | `REQUIREMENTS.md` |
| Commands, exit codes, fixture format, limitations | `README.md` |
| Fixture and trace schemas | `schemas/` |
| Code layout, concurrency | `ARCHITECTURE.md` |
| Why it is built this way | `docs/adr/` |
| How to test | `TESTING.md` |
| Release history | `RELEASE.md` |
| Security reporting | `SECURITY.md` |

## Hard rules

1. Do not skip or delete tests, weaken the CI gates, or add lint excludes to make CI pass.
2. Never invent Modbus semantics; cite the specification, `REQUIREMENTS.md` or go-modbus behaviour.
3. Never claim conformance or certification beyond recorded evidence.
4. Exit codes, flags and the trace format are a contract for scripts: no incidental breaking changes.
5. No new third-party runtime dependency without saying why.
6. Bundled codec fixtures are generated: change them with `make generate-codec-examples`, not by hand.

## Commands

| When you change… | Run |
|---|---|
| anything | `make check` |
| the handler, listener or state | `make soak` (chaos and stress under the race detector; `SOAKTIME`, default 2m) |
| fixture, expectation or request decoding | `make fuzz` (`FUZZTIME`, default 30s per target) |
| dependencies | `make vuln` |
| codec examples | `make generate-codec-examples` |

A failing randomized test logs its seed; replay it with `MODBUS_SIMULATOR_TEST_SEED=<seed>`.

## Definition of Done

- Tests for new behaviour, including error paths; `make check` green.
- A fuzz target for any new decoder of untrusted input.
- `README.md` and `REQUIREMENTS.md` updated in the same change.
- `RELEASE.md` section for user-visible changes (`## vX.Y.Z` … `---`).
- Failing or skipped steps reported explicitly.

## Git and releases

Work on a branch and open a PR. Do not push to `main`, create tags or run the Release workflow unless the maintainer asks for it in the session.
