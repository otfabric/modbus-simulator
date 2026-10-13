package cmd

import (
	"fmt"
	"strings"

	"github.com/otfabric/modbus-simulator/internal/simulator"
	"github.com/spf13/cobra"
)

func newValidateCmd() *cobra.Command {
	var format string
	c := &cobra.Command{
		Use:   "validate <fixture.json>",
		Short: "Validate a fixture against the schema and semantic rules",
		Args:  cobra.ExactArgs(1),
		Example: `
  # Validate a fixture (exit 0 on success, 3 on any validation failure)
  modbus-simulator validate device.json

  # Machine-readable result
  modbus-simulator validate device.json --format json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := checkFormat(format); err != nil {
				return err
			}
			path := strings.TrimSpace(args[0])
			if path == "" {
				return invalidInput(fmt.Errorf("fixture path is required"))
			}
			f, err := simulator.ValidateFixtureFile(path)
			if err != nil {
				return invalidInput(fmt.Errorf("invalid fixture %q: %w", path, err))
			}
			if err := render(cmd.OutOrStdout(), format, simulator.Summarize(f)); err != nil {
				return &exitError{code: exitInternal, err: err}
			}
			return nil
		},
	}
	c.Flags().StringVar(&format, "format", envOr("MODBUS_SIMULATOR_FORMAT", "text"), "Output format: text or json")
	return c
}
