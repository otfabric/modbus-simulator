# ADR 0004: Configured rules before built-in validation

- Status: Accepted
- Date: recorded in `REQUIREMENTS.md` by v1.0.0 (2026-07-05)
- Specified in: `REQUIREMENTS.md`, resolution 22 and Rule evaluation order

## Context

The precedence of configured rules and built-in validation was undefined in the draft.

## Decision

Each request is resolved in a fixed order, and the first stage with an outcome wins: exception rules, fault rules, built-in validation, normal state operation.

## Consequences

A test can force an exception or a fault on an otherwise valid address. `delay` faults wait and then continue to the later stages.
