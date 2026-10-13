package cmd

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/otfabric/modbus-simulator/internal/simulator"
	"github.com/spf13/cobra"
)

func newRunCmd() *cobra.Command {
	var (
		fixture      string
		listen       string
		trace        string
		traceDecoded bool
		traceRaw     bool
		maxClients   uint
		control      string
		tlsCert      string
		tlsKey       string
		tlsClientCA  string
	)
	c := &cobra.Command{
		Use:   "run",
		Short: "Run the stateful Modbus TCP simulator from a fixture",
		Example: `
  # Serve a fixture on the default port (:5020)
  modbus-simulator run --fixture device.json

  # Serve on a specific address and write a trace
  modbus-simulator run --fixture device.json --listen :1502 --trace trace.jsonl

  # Reset or reload the fixture of a running simulator over HTTP
  modbus-simulator run --fixture device.json --control 127.0.0.1:5021

  # Modbus/TCP Security (TLS with client certificates)
  modbus-simulator run --fixture device.json --tls-cert server.pem --tls-key server.key --tls-client-ca ca.pem

  # Environment variables instead of flags
  MODBUS_SIMULATOR_FIXTURE=device.json MODBUS_SIMULATOR_LISTEN=:5020 modbus-simulator run`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(fixture) == "" {
				return invalidInput(fmt.Errorf("fixture path is required (--fixture / MODBUS_SIMULATOR_FIXTURE)"))
			}
			opts := simulator.RunOptions{
				FixturePath:  fixture,
				Listen:       listen,
				TracePath:    trace,
				TraceDecoded: traceDecoded,
				TraceRaw:     traceRaw,
				MaxClients:   maxClients,
				Debug:        debugEnabled(cmd),
				TLSCert:      strings.TrimSpace(tlsCert),
				TLSKey:       strings.TrimSpace(tlsKey),
				TLSClientCA:  strings.TrimSpace(tlsClientCA),
				Control:      strings.TrimSpace(control),
			}
			if opts.TLSEnabled() && (opts.TLSCert == "" || opts.TLSKey == "" || opts.TLSClientCA == "") {
				return &exitError{code: exitUsage, err: fmt.Errorf("--tls-cert, --tls-key and --tls-client-ca must be given together")}
			}
			err := simulator.Run(cmd.Context(), opts, cmd.ErrOrStderr())
			if err != nil {
				if errors.Is(err, context.Canceled) {
					return nil
				}
				var invalid *simulator.ErrFixtureInvalid
				if errors.As(err, &invalid) {
					return invalidInput(fmt.Errorf("invalid fixture: %w", invalid))
				}
				return &exitError{code: exitTransport, err: fmt.Errorf("simulator failed: %w", err)}
			}
			return nil
		},
	}
	c.Flags().StringVar(&fixture, "fixture", envOr("MODBUS_SIMULATOR_FIXTURE", ""), "Path to the fixture JSON file (required)")
	c.Flags().StringVar(&listen, "listen", envOr("MODBUS_SIMULATOR_LISTEN", ":5020"), "TCP listen address host:port (e.g. :5020)")
	c.Flags().StringVar(&trace, "trace", envOr("MODBUS_SIMULATOR_TRACE", ""), "Trace JSONL output path (omitted = no trace)")
	c.Flags().BoolVar(&traceDecoded, "trace-decoded", envBool("MODBUS_SIMULATOR_TRACE_DECODED", false), "Include typed codec decode overlay in trace records")
	c.Flags().BoolVar(&traceRaw, "trace-raw", envBool("MODBUS_SIMULATOR_TRACE_RAW", false), "Include the request and response PDU bytes (hex) in trace records")
	c.Flags().StringVar(&control, "control", envOr("MODBUS_SIMULATOR_CONTROL", ""), "HTTP control API address host:port for reset and reload (omitted = disabled; no authentication, use a loopback address)")
	c.Flags().StringVar(&tlsCert, "tls-cert", envOr("MODBUS_SIMULATOR_TLS_CERT", ""), "Server certificate (PEM); enables Modbus/TCP Security together with --tls-key and --tls-client-ca")
	c.Flags().StringVar(&tlsKey, "tls-key", envOr("MODBUS_SIMULATOR_TLS_KEY", ""), "Server private key (PEM)")
	c.Flags().StringVar(&tlsClientCA, "tls-client-ca", envOr("MODBUS_SIMULATOR_TLS_CLIENT_CA", ""), "CA certificates (PEM) that client certificates must chain to")
	c.Flags().UintVar(&maxClients, "max-clients", envUint("MODBUS_SIMULATOR_MAX_CLIENTS", 0), "Max concurrent clients (0 = unlimited)")
	return c
}
