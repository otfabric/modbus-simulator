# Testing

No devices or external services are needed: tests start the simulator in-process on a local port. All test code is in `internal/simulator` and `cmd`.

## Tiers

| Tier | Command | What | In CI |
|---|---|---|---|
| Unit, integration, e2e, short chaos and stress, with race | `make test` | All tests, every fixture in `examples/` included | yes |
| Coverage | `make coverage` writes `coverage.out`; `make cover` opens it | The Makefile target has no threshold | gate in CI |
| Fuzz | `make fuzz` (`FUZZTIME`, default 30s per target) | `FuzzValidateFixture`, `FuzzExpectation`, `FuzzHandlerRequests` | yes, `fuzz-time: 10s` |
| Soak | `make soak` (`SOAKTIME`, default 2m) | Loops `TestChaos_*` and `TestStress_*` under the race detector | no |
| Vulnerabilities | `make vuln` | govulncheck | yes |

`make check` runs fmt, lint, lint-ci, vet, test and coverage. CI calls the shared `go-ci` workflow on Go 1.25 and 1.26 (`.github/workflows/ci.yml`).

## Coverage policy

CI enforces `coverage-min: 85`. `tools/` and example paths are excluded through `test-exclude-regex` in `.github/workflows/ci.yml`: the generator is not part of the binary. `cmd/` has tests and is included.

## What the suites check

`README.md`, Chaos, stress and fuzz tests, describes these suites. After each run the trace must be schema-valid, numbered `1, 2, 3, …` in file order, and replay as a sequential history. `TestExamples_AllFixturesHaveIntegrationCase` requires an integration case for every example fixture, and `TestExamples_AllCodecsHaveExample` an example for every codec.

## Replaying a failure

- Randomized tests log their seed; replay one with `MODBUS_SIMULATOR_TEST_SEED=<seed>`.
- Fuzz: a failing input lands in `internal/simulator/testdata/fuzz/<target>/`. Commit it with the fix: `make test` replays that corpus.
