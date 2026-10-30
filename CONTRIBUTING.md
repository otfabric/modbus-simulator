# Contributing to modbus-simulator

Read [`AGENTS.md`](AGENTS.md) first: it has the hard rules and says what to run for which change. The specification is [`REQUIREMENTS.md`](REQUIREMENTS.md).

## Development setup

- Go 1.25+ (see [go.mod](go.mod))
- [staticcheck](https://staticcheck.dev/), [golangci-lint](https://golangci-lint.run/) and [govulncheck](https://pkg.go.dev/golang.org/x/vuln/cmd/govulncheck) for the checks
- No devices: tests start the simulator in-process

```sh
make              # list every target
make check        # format, lint, vet, race tests, coverage profile
make build        # bin/modbus-simulator (runs make check first)
```

[TESTING.md](TESTING.md) describes the test tiers. [ARCHITECTURE.md](ARCHITECTURE.md) describes the code.

## Making changes

- Exit codes, flags and the trace format are a contract for scripts: no incidental breaking changes.
- Do not invent Modbus semantics: cite the specification, `REQUIREMENTS.md` or go-modbus behaviour.
- Tests for new behaviour, error paths included. A fuzz target for any new decoder of untrusted input.
- Update `README.md` and `REQUIREMENTS.md` in the same change, and `schemas/` when a fixture or trace field changes.
- Bundled codec fixtures are generated: change them with `make generate-codec-examples`.
- A user-visible change needs a `RELEASE.md` section (`## vX.Y.Z` … `---`).

## Pull requests

Work on a branch and open a pull request against `main`; CI must pass. Releases are made by the maintainer from the Release workflow. Report security issues as described in [SECURITY.md](SECURITY.md).
