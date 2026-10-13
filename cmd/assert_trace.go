package cmd

import (
	"fmt"
	"strings"

	"github.com/otfabric/modbus-simulator/internal/simulator"
	"github.com/spf13/cobra"
)

func newAssertTraceCmd() *cobra.Command {
	var (
		expect string
		format string
	)
	c := &cobra.Command{
		Use:   "assert-trace <trace.jsonl>",
		Short: "Assert a generated trace against an expectation file",
		Args:  cobra.ExactArgs(1),
		Example: `
  # Assert a trace against expectations (exit 8 if any assertion fails)
  modbus-simulator assert-trace trace.jsonl --expect expected.json

  # Machine-readable result
  modbus-simulator assert-trace trace.jsonl --expect expected.json --format json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := checkFormat(format); err != nil {
				return err
			}
			tracePath := strings.TrimSpace(args[0])
			if tracePath == "" {
				return invalidInput(fmt.Errorf("trace path is required"))
			}
			if strings.TrimSpace(expect) == "" {
				return invalidInput(fmt.Errorf("expectation file is required (--expect / MODBUS_SIMULATOR_EXPECT)"))
			}
			report, err := simulator.AssertTraceFiles(tracePath, expect)
			if err != nil {
				return invalidInput(err)
			}
			if rerr := render(cmd.OutOrStdout(), format, report); rerr != nil {
				return &exitError{code: exitInternal, err: rerr}
			}
			if !report.Passed {
				return &exitError{code: exitAssertionFailed}
			}
			return nil
		},
	}
	c.Flags().StringVar(&expect, "expect", envOr("MODBUS_SIMULATOR_EXPECT", ""), "Path to the expectation JSON file (required)")
	c.Flags().StringVar(&format, "format", envOr("MODBUS_SIMULATOR_FORMAT", "text"), "Output format: text or json")
	return c
}
