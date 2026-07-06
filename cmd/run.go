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
		maxClients   uint
	)
	c := &cobra.Command{
		Use:   "run",
		Short: "Run the stateful Modbus TCP simulator from a fixture",
		Example: `
  # Serve a fixture on the default port (:5020)
  modbus-simulator run --fixture device.json

  # Serve on a specific address and write a trace
  modbus-simulator run --fixture device.json --listen :1502 --trace trace.jsonl

  # Environment variables instead of flags
  MODBUS_SIMULATOR_FIXTURE=device.json MODBUS_SIMULATOR_LISTEN=:5020 modbus-simulator run`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(fixture) == "" {
				return invalidInput(fmt.Errorf("fixture path is required (--fixture / MODBUS_SIMULATOR_FIXTURE)"))
			}
			err := simulator.Run(cmd.Context(), simulator.RunOptions{
				FixturePath:  fixture,
				Listen:       listen,
				TracePath:    trace,
				TraceDecoded: traceDecoded,
				MaxClients:   maxClients,
				Debug:        debugEnabled(cmd),
			}, cmd.ErrOrStderr())
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
	c.Flags().UintVar(&maxClients, "max-clients", envUint("MODBUS_SIMULATOR_MAX_CLIENTS", 0), "Max concurrent clients (0 = unlimited)")
	return c
}

func init() { rootCmd.AddCommand(newRunCmd()) }
