package simulator

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/otfabric/go-modbus"
	"github.com/stretchr/testify/require"
)

func TestTrace_RecordsAreSchemaValidAndWellFormed(t *testing.T) {
	fx := `{
      "schemaVersion": "modbus-simulator.fixture.v1",
      "devices": [{
        "unitId": 1,
        "identity": {"vendorName":"OTFabric","productCode":"GWTEST","majorMinorRevision":"1.0.0","objects":[{"id":128,"value":"x"}]},
        "state": {
          "holdingRegisters": [{"address":0,"value":2301,"writable":false},{"address":1,"value":501,"writable":true}]
        },
        "behavior": {"exceptions":[{"id":"hr-999","match":{"fc":3,"address":999},"exceptionCode":"illegal_data_address"}]}
      }]
    }`

	var buf bytes.Buffer
	jw := NewJSONLWriter(&buf)
	f, err := ValidateFixtureBytes([]byte(fx))
	require.NoError(t, err)
	store, err := BuildStore(f)
	require.NoError(t, err)
	h := NewHandler(store, jw, HandlerOptions{})
	ctx := context.Background()

	// read, write, read-back, fc43, configured exception
	_, err = h.HandleHoldingRegisters(ctx, &modbus.HoldingRegistersRequest{UnitID: 1, FunctionCode: 3, Addr: 0, Quantity: 2})
	require.NoError(t, err)
	_, err = h.HandleHoldingRegisters(ctx, &modbus.HoldingRegistersRequest{UnitID: 1, FunctionCode: 6, Addr: 1, IsWrite: true, Args: []uint16{777}})
	require.NoError(t, err)
	_, err = h.HandleHoldingRegisters(ctx, &modbus.HoldingRegistersRequest{UnitID: 1, FunctionCode: 3, Addr: 1, Quantity: 1})
	require.NoError(t, err)
	_, _ = h.HandleDeviceIdentification(ctx, &modbus.DeviceIdentificationRequest{UnitID: 1, MEIType: modbus.MEIReadDeviceIdentification, Category: modbus.DeviceIDBasic})
	_, _ = h.HandleHoldingRegisters(ctx, &modbus.HoldingRegistersRequest{UnitID: 1, FunctionCode: 3, Addr: 999, Quantity: 1})
	require.NoError(t, jw.Close())

	lines := splitJSONL(buf.String())
	require.Len(t, lines, 5)
	for i, line := range lines {
		require.NoErrorf(t, validateBytesAgainstSchema(TraceSchemaVersion, []byte(line)),
			"line %d not schema-valid: %s", i, line)
		require.Contains(t, line, `"stateChanges"`, "line %d must always include stateChanges", i)
		require.Contains(t, line, `"area"`, "line %d request must include area", i)
	}

	// Parse into records for field-level assertions.
	recs := decodeRecords(t, lines)
	require.Equal(t, uint64(1), recs[0].Seq)
	require.Equal(t, uint64(5), recs[4].Seq)

	// Write record (seq 2) has a state change 501 -> 777.
	require.Len(t, recs[1].StateChanges, 1)
	require.Equal(t, AreaHoldingRegisters, recs[1].StateChanges[0].Area)
	require.EqualValues(t, 501, toInt(recs[1].StateChanges[0].Before))
	require.EqualValues(t, 777, toInt(recs[1].StateChanges[0].After))

	// Read-back (seq 3) returns 777.
	require.Empty(t, recs[2].StateChanges)
	require.Equal(t, "read_holding_registers", recs[2].Request.Name)

	// Configured exception (seq 5) records matchedRule + exceptionCode, empty stateChanges.
	require.Equal(t, "hr-999", recs[4].MatchedRule)
	require.False(t, recs[4].Response.OK)
	require.Equal(t, ExcIllegalDataAddress, recs[4].Response.ExceptionCode)
	require.Empty(t, recs[4].StateChanges)
}

func decodeRecords(t *testing.T, lines []string) []TraceRecord {
	t.Helper()
	recs := make([]TraceRecord, 0, len(lines))
	for _, l := range lines {
		var rec TraceRecord
		require.NoError(t, json.Unmarshal([]byte(l), &rec))
		recs = append(recs, rec)
	}
	return recs
}

func toInt(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	default:
		return 0
	}
}

func splitJSONL(s string) []string {
	var out []string
	for _, l := range strings.Split(strings.TrimRight(s, "\n"), "\n") {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return out
}

func TestJSONLWriter_FlushesEachRecord(t *testing.T) {
	var buf bytes.Buffer
	w := NewJSONLWriter(&buf)
	require.NoError(t, w.Write(&TraceRecord{SchemaVersion: TraceSchemaVersion, Seq: 1}))
	// Readable before Close.
	require.Len(t, splitJSONL(buf.String()), 1)
	require.NoError(t, w.Close())
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("disk full") }

func TestJSONLWriter_WriteErrorIsReportedByClose(t *testing.T) {
	w := NewJSONLWriter(failingWriter{})
	require.Error(t, w.Write(&TraceRecord{Seq: 1}))
	require.ErrorContains(t, w.Close(), "disk full")
}
