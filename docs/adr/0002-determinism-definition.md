# ADR 0002: What determinism means

- Status: Accepted
- Date: recorded in `REQUIREMENTS.md` by v1.0.0 (2026-07-05)
- Specified in: `REQUIREMENTS.md`, resolution 2 and Design Principles

## Context

The draft promised deterministic traces, but traces carry wall-clock `ts` and `durationMs`.

## Decision

Determinism covers responses, `stateChanges`, `matchedRule` and `seq` order, given the same fixture and the same ordered requests. `ts` and `durationMs` are informational and excluded from assertions.

## Consequences

`Handler.commitMu` makes a state operation, its `seq` and its trace record one step, so the order in the trace is the order in which requests took effect. `TestDeterministicTrace` and the replay check in the stress and chaos suites hold this.
