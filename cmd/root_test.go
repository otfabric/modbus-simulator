package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

const (
	testFixture = "../examples/all-function-codes.json"
	testTrace   = `{"schemaVersion":"modbus-simulator.trace.v1","ts":"2026-07-05T10:00:00.100Z","seq":1,"unitId":1,` +
		`"request":{"fc":3,"name":"read_holding_registers","area":"holdingRegisters","address":0,"quantity":2},` +
		`"response":{"ok":true,"values":[2301,501]},"stateChanges":[],"durationMs":2}` + "\n"
)

// run executes one command line and returns the exit code with captured stdout/stderr.
func run(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	for _, key := range []string{
		"MODBUS_SIMULATOR_FORMAT", "MODBUS_SIMULATOR_FIXTURE", "MODBUS_SIMULATOR_EXPECT",
		"MODBUS_SIMULATOR_TRACE", "MODBUS_SIMULATOR_LISTEN", "MODBUS_SIMULATOR_CONTROL",
		"MODBUS_SIMULATOR_TLS_CERT", "MODBUS_SIMULATOR_TLS_KEY", "MODBUS_SIMULATOR_TLS_CLIENT_CA",
	} {
		if _, set := os.LookupEnv(key); set {
			t.Setenv(key, "")
		}
	}
	return runCtx(t, context.Background(), args...)
}

func runCtx(t *testing.T, ctx context.Context, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := execute(ctx, args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func writeFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	return path
}

func TestVersion(t *testing.T) {
	SetBuildMeta("1.2.3", "v1.2.3", "abc1234", "2026-01-01T00:00:00Z")
	t.Cleanup(func() { SetBuildMeta("", "", "", "") })
	code, out, _ := run(t, "version")
	require.Equal(t, exitOK, code)
	require.Contains(t, out, "modbus-simulator version: 1.2.3")
	require.Contains(t, out, "abc1234")
}

func TestUnknownFlagIsUsageError(t *testing.T) {
	code, _, errOut := run(t, "validate", "--nope")
	require.Equal(t, exitUsage, code)
	require.Contains(t, errOut, "Error:")
}

func TestValidate(t *testing.T) {
	code, out, _ := run(t, "validate", testFixture)
	require.Equal(t, exitOK, code)
	require.Contains(t, out, "Fixture valid")

	code, out, _ = run(t, "validate", testFixture, "--format", "json")
	require.Equal(t, exitOK, code)
	var summary struct {
		Valid bool `json:"valid"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &summary))
	require.True(t, summary.Valid)
}

func TestValidate_FormatFromEnv(t *testing.T) {
	t.Setenv("MODBUS_SIMULATOR_FORMAT", "json")
	code, out, _ := runCtx(t, context.Background(), "validate", testFixture)
	require.Equal(t, exitOK, code)
	require.True(t, json.Valid([]byte(out)))
}

func TestValidate_Invalid(t *testing.T) {
	code, _, errOut := run(t, "validate", filepath.Join(t.TempDir(), "missing.json"))
	require.Equal(t, exitInvalidInput, code)
	require.Contains(t, errOut, "invalid fixture")

	bad := writeFile(t, "bad.json", `{"schemaVersion":"modbus-simulator.fixture.v1","devices":[]}`)
	code, _, _ = run(t, "validate", bad)
	require.Equal(t, exitInvalidInput, code)
}

func TestInvalidFormatIsUsageError(t *testing.T) {
	trace := writeFile(t, "trace.jsonl", testTrace)
	for _, args := range [][]string{
		{"validate", testFixture, "--format", "xml"},
		{"assert-trace", trace, "--expect", "unused.json", "--format", "xml"},
		{"codecs", "list", "--format", "xml"},
	} {
		code, out, errOut := run(t, args...)
		require.Equal(t, exitUsage, code, args)
		require.Empty(t, out, args)
		require.Contains(t, errOut, "invalid format", args)
	}
}

func TestAssertTrace(t *testing.T) {
	trace := writeFile(t, "trace.jsonl", testTrace)
	pass := writeFile(t, "pass.json", `{"schemaVersion":"modbus-simulator.expect.v1","assertions":[
	  {"kind":"contains","match":{"request":{"fc":3},"response":{"values":[2301,501]}}}]}`)
	fail := writeFile(t, "fail.json", `{"schemaVersion":"modbus-simulator.expect.v1","assertions":[
	  {"kind":"contains","match":{"request":{"fc":6}}}]}`)

	code, out, _ := run(t, "assert-trace", trace, "--expect", pass)
	require.Equal(t, exitOK, code)
	require.Contains(t, out, "1/1 assertions passed")

	code, out, errOut := run(t, "assert-trace", trace, "--expect", fail, "--format", "json")
	require.Equal(t, exitAssertionFailed, code)
	require.Empty(t, errOut)
	var report struct {
		Passed bool `json:"passed"`
		Failed int  `json:"failed"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &report))
	require.False(t, report.Passed)
	require.Equal(t, 1, report.Failed)

	code, _, errOut = run(t, "assert-trace", trace)
	require.Equal(t, exitInvalidInput, code)
	require.Contains(t, errOut, "--expect")

	code, _, _ = run(t, "assert-trace", writeFile(t, "garbage.jsonl", "{}\n"), "--expect", pass)
	require.Equal(t, exitInvalidInput, code)
}

func TestCodecsList(t *testing.T) {
	code, out, _ := run(t, "codecs", "list", "--family", "float", "--format", "json")
	require.Equal(t, exitOK, code)
	var items []codecListItem
	require.NoError(t, json.Unmarshal([]byte(out), &items))
	require.NotEmpty(t, items)
	for _, item := range items {
		require.Equal(t, "float", item.Family)
	}

	code, out, _ = run(t, "codecs", "list")
	require.Equal(t, exitOK, code)
	require.Contains(t, out, "float32/layout:4321")
}

func TestRun(t *testing.T) {
	code, _, errOut := run(t, "run")
	require.Equal(t, exitInvalidInput, code)
	require.Contains(t, errOut, "--fixture")

	bad := writeFile(t, "bad.json", `{"schemaVersion":"nope"}`)
	code, _, errOut = run(t, "run", "--fixture", bad)
	require.Equal(t, exitInvalidInput, code)
	require.Contains(t, errOut, "invalid fixture")

	// TLS needs all three files.
	code, _, errOut = run(t, "run", "--fixture", testFixture, "--tls-cert", "server.pem")
	require.Equal(t, exitUsage, code)
	require.Contains(t, errOut, "--tls-client-ca")

	// A TLS file that cannot be read is a failure to start.
	code, _, errOut = run(t, "run", "--fixture", testFixture, "--tls-cert", "none.pem", "--tls-key", "none.key", "--tls-client-ca", "none-ca.pem")
	require.Equal(t, exitTransport, code)
	require.Contains(t, errOut, "load TLS certificate")

	// An interrupted run is a clean exit.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	code, _, errOut = runCtx(t, ctx, "run", "--fixture", testFixture)
	require.Equal(t, exitOK, code)
	require.Empty(t, errOut)
}

func TestCompletion(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish", "powershell"} {
		code, out, _ := run(t, "completion", shell)
		require.Equal(t, exitOK, code, shell)
		require.Contains(t, out, "modbus-simulator", shell)
	}
	code, _, _ := run(t, "completion", "tcsh")
	require.Equal(t, exitUsage, code)
}
