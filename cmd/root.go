// Package cmd implements the modbus-simulator CLI: a dedicated, stateful Modbus TCP
// device simulator driven by a declarative JSON fixture, with JSONL trace output and
// trace assertions. See REQUIREMENTS.md for the authoritative specification.
package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"github.com/spf13/cobra"
)

// Process exit codes.
const (
	exitOK              = 0
	exitUsage           = 2
	exitInvalidInput    = 3
	exitTransport       = 4
	exitAssertionFailed = 8
	exitInternal        = 10
)

// exitError carries an explicit process exit code out of a command's RunE. When err is
// nil the process exits with code without printing anything (e.g. a failed assertion whose
// report was already rendered).
type exitError struct {
	code int
	err  error
}

func (e *exitError) Error() string {
	if e.err != nil {
		return e.err.Error()
	}
	return ""
}

func (e *exitError) Unwrap() error { return e.err }

func invalidInput(err error) error { return &exitError{code: exitInvalidInput, err: err} }

// buildMeta holds link-time metadata set from main via SetBuildMeta.
var buildMeta struct {
	version, tag, commit, buildDate string
}

// SetBuildMeta is called from main before Execute; values come from ldflags -X main.*.
func SetBuildMeta(version, tag, commit, buildDate string) {
	buildMeta.version = version
	buildMeta.tag = tag
	buildMeta.commit = commit
	buildMeta.buildDate = buildDate
}

var rootCmd = &cobra.Command{
	Use:   "modbus-simulator",
	Short: "Deterministic, stateful Modbus TCP device simulator",
	Long: `modbus-simulator emulates one or more stateful Modbus TCP devices from a
declarative JSON fixture, optionally recording a JSONL trace of every served request
for later assertions.

  validate      validate a fixture (JSON Schema + semantic checks)
  run           start the stateful Modbus TCP simulator from a fixture
  assert-trace  assert a generated trace against expectations
  codecs        list go-modbus codec IDs (for fixture codec fields)`,
}

func init() {
	rootCmd.PersistentFlags().Bool("debug", false, "Print debug information")
	rootCmd.SilenceUsage = true
	rootCmd.SilenceErrors = true
	rootCmd.AddCommand(versionCmd)
	rootCmd.AddCommand(completionCmd)
}

var completionCmd = &cobra.Command{
	Use:   "completion [bash|zsh|fish|powershell]",
	Short: "Generate shell completion scripts",
	Long: `Generate a shell completion script for modbus-simulator.

To load completions:

Bash:
  source <(modbus-simulator completion bash)

Zsh:
  source <(modbus-simulator completion zsh)

Fish:
  modbus-simulator completion fish | source

PowerShell:
  modbus-simulator completion powershell | Out-String | Invoke-Expression`,
	DisableFlagsInUseLine: true,
	ValidArgs:             []string{"bash", "zsh", "fish", "powershell"},
	Args:                  cobra.MatchAll(cobra.ExactArgs(1), cobra.OnlyValidArgs),
	RunE: func(cmd *cobra.Command, args []string) error {
		switch args[0] {
		case "bash":
			return rootCmd.GenBashCompletion(cmd.OutOrStdout())
		case "zsh":
			return rootCmd.GenZshCompletion(cmd.OutOrStdout())
		case "fish":
			return rootCmd.GenFishCompletion(cmd.OutOrStdout(), true)
		case "powershell":
			return rootCmd.GenPowerShellCompletionWithDesc(cmd.OutOrStdout())
		default:
			return fmt.Errorf("unsupported shell %q", args[0])
		}
	},
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the version of modbus-simulator",
	Run: func(cmd *cobra.Command, args []string) {
		out := cmd.OutOrStdout()
		ver := strings.TrimSpace(buildMeta.version)
		if ver == "" {
			ver = "(unknown)"
		}
		_, _ = fmt.Fprintf(out, "modbus-simulator version: %s\n", ver)
		if s := strings.TrimSpace(buildMeta.tag); s != "" {
			_, _ = fmt.Fprintf(out, "tag:        %s\n", s)
		}
		if s := strings.TrimSpace(buildMeta.commit); s != "" {
			_, _ = fmt.Fprintf(out, "commit:     %s\n", s)
		}
		if s := strings.TrimSpace(buildMeta.buildDate); s != "" {
			_, _ = fmt.Fprintf(out, "build date: %s\n", s)
		}
	},
}

// Execute runs the root command and returns the process exit code (main calls os.Exit).
func Execute() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	err := rootCmd.ExecuteContext(ctx)
	if err == nil {
		return exitOK
	}

	var ee *exitError
	if errors.As(err, &ee) {
		if ee.err != nil {
			fmt.Fprintln(os.Stderr, "Error:", ee.err)
		}
		return ee.code
	}
	// Untyped errors here are cobra flag/arg parsing failures.
	fmt.Fprintln(os.Stderr, "Error:", err)
	return exitUsage
}

// debugEnabled reports whether the persistent --debug flag is set.
func debugEnabled(cmd *cobra.Command) bool {
	v, _ := cmd.Flags().GetBool("debug")
	return v
}

// textMarshaler renders a human-oriented string for --format text.
type textMarshaler interface {
	MarshalTextOutput() (string, error)
}

// render writes v to w according to format ("text" default, or "json").
func render(w io.Writer, format string, v any) error {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "json":
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(v)
	case "", "text":
		tm, ok := v.(textMarshaler)
		if !ok {
			return fmt.Errorf("text output is not supported for this result")
		}
		s, err := tm.MarshalTextOutput()
		if err != nil {
			return err
		}
		_, err = io.WriteString(w, strings.TrimSuffix(s, "\n")+"\n")
		return err
	default:
		return fmt.Errorf("invalid format %q (expected: text, json)", format)
	}
}

// envOr returns the value of the environment variable key, or def if unset/empty.
func envOr(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

// envUint parses an unsigned integer env var, returning def on unset/invalid.
func envUint(key string, def uint) uint {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	n, err := strconv.ParseUint(v, 10, 64)
	if err != nil {
		return def
	}
	return uint(n)
}

// envBool parses a boolean env var, returning def on unset/invalid.
func envBool(key string, def bool) bool {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return def
	}
	return b
}
