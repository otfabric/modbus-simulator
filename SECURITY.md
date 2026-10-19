# Security Policy

## Reporting a Vulnerability

If you discover a security vulnerability in `modbus-simulator`, please report it responsibly.

**Do not open a public GitHub issue for security vulnerabilities.**

Instead, please email **security@otfabric.io**, or open a private advisory on GitHub (the [Security](https://github.com/otfabric/modbus-simulator/security) tab → **Advisories** → **Report a vulnerability**), with:

- A description of the vulnerability
- Steps to reproduce the issue, ideally a fixture and the requests that trigger it
- The affected version(s)
- Any potential impact you've identified

We will acknowledge your report within 48 hours and aim to provide a fix or mitigation within 7 days for critical issues.

## Supported Versions

| Version | Supported |
|---------|-----------|
| Latest release | Yes |
| Older releases | Best effort |

## Security Considerations

### This is a test tool, not a device

The simulator serves Modbus TCP, which has no authentication and no encryption. Without TLS, anyone who can reach the listener can read every point and write every writable one; coils and holding registers are writable unless the fixture sets `"writable": false`.

- **Run it on a development machine or an isolated test network**, never on a plant or control network
- **Bind it to loopback when you can**: the default `--listen :5020` listens on all interfaces (`0.0.0.0:5020`). Use `--listen 127.0.0.1:5020` unless other hosts need to reach it
- Point clients under test at the simulator, not at production equipment; a test that writes will write to whatever it is aimed at
- `--max-clients` defaults to `0`, which here means no limit; set it when the listener is reachable by others

### TLS

- `--tls-cert`, `--tls-key` and `--tls-client-ca` must be given together; the simulator refuses to start TLS without a client CA, so a TLS listener only serves clients with a certificate that chains to it
- Every client that passes the TLS handshake gets the same access; there are no per-client roles

### Control API

- `--control` starts an HTTP API (`POST /reset`, `POST /reload`, `GET /healthz`) with **no authentication** and no TLS. It is off by default; when you enable it, bind it to a loopback address such as `127.0.0.1:5021`

### Trace files

- `--trace` writes every served request to a JSONL file, including written values and, with `--trace-raw`, the PDU bytes. The file is created with the process's default permissions and overwritten if it exists

### Input handling

- Fixtures, expectation files and request sequences are fuzzed (`make fuzz`), and the chaos tests run clients that send garbage, truncated frames, bad length fields and slow-loris traffic alongside well-behaved ones
