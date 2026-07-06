package simulator

import (
	"context"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidateFixture_AcceptsCodecBundle(t *testing.T) {
	f, err := ValidateFixtureFile(exampleFixturePath("codec-floats.json"))
	require.NoError(t, err)
	require.Equal(t, FixtureSchemaVersion, f.SchemaVersion)
}

func TestValidateFixture_RejectsOverlap(t *testing.T) {
	json := `{
	  "schemaVersion": "modbus-simulator.fixture.v1",
	  "devices": [{
	    "unitId": 1,
	    "state": {
	      "holdingRegisters": [
	        {"address": 10, "codec": "float32/layout:4321", "value": 1.0},
	        {"address": 11, "value": 2}
	      ]
	    }
	  }]
	}`
	_, err := ValidateFixtureBytes([]byte(json))
	require.Error(t, err)
	require.Contains(t, err.Error(), "overlapping")
}

func TestValidateFixture_RejectsUnknownCodec(t *testing.T) {
	json := `{
	  "schemaVersion": "modbus-simulator.fixture.v1",
	  "devices": [{
	    "unitId": 1,
	    "state": {
	      "holdingRegisters": [
	        {"address": 0, "codec": "no/such/codec", "value": 1}
	      ]
	    }
	  }]
	}`
	_, err := ValidateFixtureBytes([]byte(json))
	require.Error(t, err)
	require.Contains(t, err.Error(), "unknown codec")
}

func TestValidateFixture_AcceptsCodecPoint(t *testing.T) {
	json := minimalFixture(`{"unitId":1,"state":{"holdingRegisters":[{"address":0,"codec":"float32/layout:4321","value":1.0}]}}`)
	_, err := ValidateFixtureBytes([]byte(json))
	require.NoError(t, err)
}

func TestTraceDecodedOverlay(t *testing.T) {
	tracePath := filepath.Join(t.TempDir(), "trace.jsonl")
	fixturePath := exampleFixturePath("codec-floats.json")

	port := freePort(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, RunOptions{
			FixturePath:  fixturePath,
			Listen:       "127.0.0.1:" + strconv.Itoa(port),
			TracePath:    tracePath,
			TraceDecoded: true,
		}, nil)
	}()

	client := dialClient(t, port)
	_, err := client.ReadHoldingRegisters(context.Background(), exampleUnitID, 0, 2)
	require.NoError(t, err)
	require.NoError(t, client.Close())
	cancel()
	waitServerDone(t, done)

	records, err := LoadTraceRecords(tracePath)
	require.NoError(t, err)
	require.NotEmpty(t, records)
	decoded, ok := records[0]["decoded"].([]any)
	require.True(t, ok, "expected decoded overlay")
	require.Len(t, decoded, 1)
	field := decoded[0].(map[string]any)
	require.Equal(t, "float32/layout:1234", field["codec"])
	require.InDelta(t, 230.5, field["value"], 0.001)
}
