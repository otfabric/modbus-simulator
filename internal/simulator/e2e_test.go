package simulator

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/otfabric/go-modbus"
	"github.com/stretchr/testify/require"
)

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := ln.Addr().(*net.TCPAddr).Port
	require.NoError(t, ln.Close())
	return port
}

func dialClient(t *testing.T, port int) *modbus.Client {
	t.Helper()
	url := fmt.Sprintf("tcp://127.0.0.1:%d", port)
	deadline := time.Now().Add(3 * time.Second)
	for {
		c, err := modbus.New(modbus.Config{URL: url, Timeout: 2 * time.Second})
		require.NoError(t, err)
		if err := c.Open(); err == nil {
			return c
		}
		_ = c.Close()
		if time.Now().After(deadline) {
			t.Fatalf("server did not become ready on %s", url)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func waitServerDone(t *testing.T, done <-chan error) {
	t.Helper()
	select {
	case rerr := <-done:
		require.True(t, rerr == nil || errors.Is(rerr, context.Canceled), "unexpected server error: %v", rerr)
	case <-time.After(3 * time.Second):
		t.Fatal("server did not shut down")
	}
}

// withFixtureServer validates the fixture, starts a TCP simulator, dials a client,
// runs fn, then shuts down cleanly.
func withFixtureServer(t *testing.T, fixturePath, tracePath string, fn func(ctx context.Context, client *modbus.Client)) {
	t.Helper()
	_, err := ValidateFixtureFile(fixturePath)
	require.NoError(t, err)

	port := freePort(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, RunOptions{
			FixturePath: fixturePath,
			Listen:      fmt.Sprintf("127.0.0.1:%d", port),
			TracePath:   tracePath,
		}, nil)
	}()

	client := dialClient(t, port)
	fn(context.Background(), client)
	require.NoError(t, client.Close())
	cancel()
	waitServerDone(t, done)
}

func TestE2E_SimulatorServeTraceAndAssert(t *testing.T) {
	dir := t.TempDir()
	fixturePath := filepath.Join(dir, "device.json")
	tracePath := filepath.Join(dir, "trace.jsonl")
	require.NoError(t, os.WriteFile(fixturePath, []byte(`{
      "schemaVersion":"modbus-simulator.fixture.v1",
      "defaults":{"unknownAddress":"exception"},
      "devices":[{
        "unitId":1,
        "identity":{"vendorName":"OTFabric","productCode":"GWTEST","majorMinorRevision":"1.0.0"},
        "state":{"holdingRegisters":[{"address":0,"value":2301,"writable":false},{"address":1,"value":501,"writable":true}]}
      }]
    }`), 0o644))

	withFixtureServer(t, fixturePath, tracePath, func(rctx context.Context, client *modbus.Client) {
		require.NoError(t, client.WriteRegister(rctx, 1, 1, 777))
		got, err := client.ReadHoldingRegisters(rctx, 1, 1, 1)
		require.NoError(t, err)
		require.Equal(t, []uint16{777}, got)

		// Unknown address under the exception policy → Modbus exception.
		_, err = client.ReadHoldingRegisters(rctx, 1, 50, 1)
		require.Error(t, err)

		// FC43 device identification.
		id, err := client.ReadDeviceIdentification(rctx, 1, modbus.DeviceIDBasic, 0)
		require.NoError(t, err)
		require.NotNil(t, id)
	})

	expectPath := filepath.Join(dir, "expect.json")
	require.NoError(t, os.WriteFile(expectPath, []byte(`{
      "schemaVersion":"modbus-simulator.expect.v1",
      "assertions":[
        {"kind":"contains","match":{"request":{"fc":6,"area":"holdingRegisters","address":1},"response":{"ok":true}}},
        {"kind":"contains","match":{"stateChange":{"area":"holdingRegisters","address":1,"before":501,"after":777}}},
        {"kind":"contains","match":{"request":{"fc":3,"area":"holdingRegisters","address":1},"response":{"ok":true,"values":[777]}}},
        {"kind":"contains","match":{"response":{"ok":false,"exceptionCode":"illegal_data_address"}}},
        {"kind":"contains","match":{"request":{"area":"identity"},"response":{"ok":true}}},
        {"kind":"ordered","sequence":[{"request":{"fc":6}},{"request":{"fc":3,"address":1}}]}
      ]
    }`), 0o644))

	report, err := AssertTraceFiles(tracePath, expectPath)
	require.NoError(t, err)
	require.Truef(t, report.Passed, "assertions failed: %+v", report.Results)
	require.GreaterOrEqual(t, report.Records, 4)
}
