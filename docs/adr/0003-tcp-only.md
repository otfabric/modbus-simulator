# ADR 0003: TCP and TLS only

- Status: Accepted
- Date: recorded in `REQUIREMENTS.md` by v1.0.0 (2026-07-05)
- Specified in: `REQUIREMENTS.md`, resolution 1 and Transport (v1)

## Context

The draft said the simulator serves TCP and RTU. go-modbus provides only TCP and TLS server transports.

## Decision

The listener is TCP, plain or with TLS and client certificates. All devices of a fixture share it and are routed by Unit ID.

## Consequences

An RTU server would need new transport work, and is deferred. `README.md`, Limitations, states this.
