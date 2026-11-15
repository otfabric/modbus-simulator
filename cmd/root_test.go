package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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
		"MODBUS_SIMULATOR_MAX_CLIENTS", "MODBUS_SIMULATOR_TRACE_RAW", "MODBUS_SIMULATOR_TRACE_DECODED",
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

// An environment variable whose value does not parse is a usage error that names it, as
// the same value given as a flag is. A flag on the command line takes precedence.
func TestRun_EnvironmentValueNotValid(t *testing.T) {
	stopped, cancel := context.WithCancel(context.Background())
	cancel() // a run that starts ends at once, with exit 0
	for _, tc := range []struct {
		key, value string
		flag       []string
		want       int
	}{
		{key: "MODBUS_SIMULATOR_MAX_CLIENTS", value: "4", want: exitOK},
		{key: "MODBUS_SIMULATOR_MAX_CLIENTS", value: "1O", want: exitUsage},
		{key: "MODBUS_SIMULATOR_MAX_CLIENTS", value: "-1", want: exitUsage},
		{key: "MODBUS_SIMULATOR_MAX_CLIENTS", value: "18446744073709551616", want: exitUsage},
		{key: "MODBUS_SIMULATOR_MAX_CLIENTS", value: "1O", flag: []string{"--max-clients", "4"}, want: exitOK},
		{key: "MODBUS_SIMULATOR_TRACE_RAW", value: "true", want: exitOK},
		{key: "MODBUS_SIMULATOR_TRACE_RAW", value: "yes", want: exitUsage},
		{key: "MODBUS_SIMULATOR_TRACE_RAW", value: "yes", flag: []string{"--trace-raw=false"}, want: exitOK},
		{key: "MODBUS_SIMULATOR_TRACE_DECODED", value: "0", want: exitOK},
		{key: "MODBUS_SIMULATOR_TRACE_DECODED", value: "on", want: exitUsage},
	} {
		name := tc.key + "=" + tc.value + " " + strings.Join(tc.flag, " ")
		t.Run(name, func(t *testing.T) {
			t.Setenv(tc.key, tc.value)
			args := append([]string{"run", "--fixture", testFixture}, tc.flag...)
			code, _, errOut := runCtx(t, stopped, args...)
			require.Equal(t, tc.want, code, errOut)
			if tc.want == exitUsage {
				require.Contains(t, errOut, "environment variable "+tc.key)
				require.Contains(t, errOut, strconv.Quote(tc.value))
			}
		})
	}
}

// The unsigned integer of an environment variable must fit in a uint of the platform.
func TestEnvDefaults_Uint(t *testing.T) {
	const key = "MODBUS_SIMULATOR_TEST_UINT"
	for _, tc := range []struct {
		value string
		want  uint
		ok    bool
	}{
		{"", 7, true}, // unset: the default
		{"0", 0, true},
		{"1", 1, true},
		{" 12 ", 12, true},
		{"4294967295", math.MaxUint32, true},
		{"4294967296", 1 << 32 & math.MaxUint, strconv.IntSize == 64},
		{strconv.FormatUint(math.MaxUint-1, 10), math.MaxUint - 1, true},
		{strconv.FormatUint(math.MaxUint, 10), math.MaxUint, true},
		{"18446744073709551616", 0, false},
		{"-1", 0, false},
		{"0x10", 0, false},
		{"1O", 0, false},
	} {
		t.Setenv(key, tc.value)
		var env envDefaults
		got := env.uintValue("flag", key, 7)
		if !tc.ok {
			require.Len(t, env.bad, 1, tc.value)
			require.Equal(t, uint(7), got, tc.value)
			continue
		}
		require.Empty(t, env.bad, tc.value)
		require.Equal(t, tc.want, got, tc.value)
	}
}
