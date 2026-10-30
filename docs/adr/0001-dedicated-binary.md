# ADR 0001: A dedicated binary with top-level commands

- Status: Accepted
- Date: recorded in `REQUIREMENTS.md` by v1.0.0 (2026-07-05)
- Specified in: `REQUIREMENTS.md`, resolution 28 and High-Level Architecture

## Context

The draft had a choice between nested commands (`fixture`, `trace`) and top-level commands.

## Decision

The simulator is a dedicated `modbus-simulator` binary with `run`, `validate`, `assert-trace` and `codecs list`.

## Consequences

Exit codes, flags and the trace format are a contract for scripts (AGENTS.md rule 4). Future commands sit next to the existing ones.
