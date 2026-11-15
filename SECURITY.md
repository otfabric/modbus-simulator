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
- A request that carries an `Origin` header is refused with `403`. Web browsers send that header with every `POST` and with the requests a page makes to another site, so a page open in a browser on the same machine cannot reset or reload the simulator; test scripts and `curl` do not send it

### Trace files

- `--trace` writes every served request to a JSONL file, including written values and, with `--trace-raw`, the PDU bytes. The file is created with the process's default permissions and overwritten if it exists

### Resource limits

What one client, or one run, can hold:

| Resource | Limit | Set by |
|---|---|---|
| Concurrent Modbus clients | `--max-clients`; the default `0` means no limit. A client over the limit is disconnected | flag |
| Modbus/TCP frame | 260 bytes (MBAP header and PDU), with the quantities per function code of the Modbus specification | go-modbus |
| Requests read ahead on one connection | 16; beyond that the server stops reading from the socket until it has answered | go-modbus |
| Idle connection | closed after 120 s in which no complete request arrives while none is being served; each response is written under a 120 s deadline | go-modbus default, not configurable here |
| TLS handshake | 10 s from accept; at `--max-clients` a new connection replaces the oldest one still in its handshake | go-modbus default, not configurable here |
| State per connection | each connection to a `resetOnConnection` device works on its own copy of that device's state until it disconnects, so memory grows with connections × fixture size; `--max-clients` bounds it | fixture (`stateMode`) |
| Fault delays | `delayMs` has no upper bound: a request under a `delay` or `timeout` rule is held that long, and the idle timeout does not end it | fixture |
| Trace file | grows by one line per request, without a size limit or rotation | `--trace` |
| Control API | 5 s to send the request header, idle keep-alive connections closed after 30 s, the header size limit of Go's `net/http` (1 MiB); one control request is served at a time | simulator |
| Shutdown | on SIGINT or SIGTERM, at most 10 s for the control API and the requests in progress; when they have not finished by then, `run` exits with code `4`. A second signal ends the process at once | simulator |
| Files read | fixtures and expectation files are read whole; `assert-trace` reads the whole trace, at most 8 MiB per line | simulator |

### Input handling

- Fixtures, expectation files and request sequences are fuzzed (`make fuzz` runs every `Fuzz` target that `go test -list` finds), and the chaos tests run clients that send garbage, truncated frames, bad length fields and slow-loris traffic alongside well-behaved ones
