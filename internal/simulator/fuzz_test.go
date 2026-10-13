package simulator

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/otfabric/go-modbus"
	"github.com/stretchr/testify/require"
)

// A fixture is either rejected or can be loaded: validation must not let anything
// through that the store cannot build, and neither may panic.
func FuzzValidateFixture(f *testing.F) {
	paths, err := filepath.Glob("../../examples/*.json")
	require.NoError(f, err)
	require.NotEmpty(f, paths)
	for _, path := range append(paths, "testdata/valid_device.json") {
		data, err := os.ReadFile(path)
		require.NoError(f, err)
		f.Add(data)
	}
	f.Add([]byte(`{"schemaVersion":"modbus-simulator.fixture.v1","devices":[{"unitId":1,"state":{"holdingRegisters":[` +
		`{"address":65535,"codec":"uint64/layout:12345678","value":1}]}}]}`))

	f.Fuzz(func(t *testing.T, data []byte) {
		fx, err := ValidateFixtureBytes(data)
		if err != nil {
			return
		}
		if _, err := BuildStore(fx); err != nil {
			t.Fatalf("fixture passed validation but cannot be loaded: %v", err)
		}
		if _, err := Summarize(fx).MarshalTextOutput(); err != nil {
			t.Fatal(err)
		}
	})
}

// An expectation file is either rejected or can be evaluated.
func FuzzExpectation(f *testing.F) {
	f.Add([]byte(`{"schemaVersion":"modbus-simulator.expect.v1","assertions":[
	  {"kind":"contains","match":{"request":{"fc":3},"response":{"ok":true,"values":[1,2]}}},
	  {"kind":"notContains","match":{"stateChange":{"after":7}}},
	  {"kind":"count","match":{"request":{"area":"coils"}},"atLeast":1},
	  {"kind":"ordered","sequence":[{"request":{"fc":6}},{"request":{"fc":3}}]},
	  {"kind":"exact","sequence":[{"response":{"ok":false}}]}]}`))
	f.Add([]byte(`{"schemaVersion":"modbus-simulator.expect.v1","assertions":[{"kind":"count"}]}`))

	var records []map[string]any
	for _, line := range []string{
		`{"seq":1,"request":{"fc":6,"area":"holdingRegisters","address":1,"value":7},"response":{"ok":true},"stateChanges":[{"area":"holdingRegisters","address":1,"before":1,"after":7}]}`,
		`{"seq":2,"request":{"fc":3,"area":"holdingRegisters","address":1,"quantity":2},"response":{"ok":true,"values":[1,2]},"stateChanges":[]}`,
		`{"seq":3,"request":{"fc":1,"area":"coils","address":9,"quantity":1},"response":{"ok":false,"exceptionCode":"illegal_data_address"},"stateChanges":[]}`,
	} {
		var rec map[string]any
		require.NoError(f, json.Unmarshal([]byte(line), &rec))
		records = append(records, rec)
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		exp, err := parseExpectation(data)
		if err != nil {
			return
		}
		report := EvaluateAssertions(records, exp)
		if report.Total != len(exp.Assertions) || len(report.Results) != report.Total {
			t.Fatalf("report covers %d of %d assertions", len(report.Results), len(exp.Assertions))
		}
		if _, err := report.MarshalTextOutput(); err != nil {
			t.Fatal(err)
		}
	})
}

// Any sequence of requests, with any unit, address and quantity, is answered without a
// panic and leaves a trace that replays as a sequential history of the fixture.
func FuzzHandlerRequests(f *testing.F) {
	fx := stressFixture()
	fx.Broadcast = true
	fx.Devices[1].Behavior.Faults[0].DelayMs = 0
	raw, err := json.Marshal(fx)
	require.NoError(f, err)
	parsed, err := ValidateFixtureBytes(raw)
	require.NoError(f, err)

	// One request is 6 bytes: kind, unit, address (2), quantity, value.
	f.Add([]byte{5, 1, 0, 3, 1, 42, 2, 1, 0, 0, 14, 0})            // write register 3, read the block
	f.Add([]byte{7, 1, 0x13, 0x88, 8, 9, 2, 1, 0x13, 0x88, 8, 0})  // write and read the hot block
	f.Add([]byte{2, 2, 0xFF, 0xFA, 12, 0, 7, 2, 0xFF, 0xFF, 2, 1}) // run past address 65535
	f.Add([]byte{2, 2, 0, 100, 1, 0, 2, 2, 0, 200, 1, 0, 8, 2, 0, 0, 0, 0, 3, 9, 0, 0, 1, 0})
	f.Add([]byte{5, 0, 0, 3, 1, 42, 2, 1, 0, 3, 1, 0, 2, 0, 0, 3, 1, 0})                           // broadcast write, then reads
	f.Add([]byte{9, 1, 0, 3, 0x0F, 0xF0, 10, 1, 0, 0, 4, 2, 10, 2, 0, 0, 12, 1, 9, 0, 0, 1, 1, 1}) // mask write, read/write

	f.Fuzz(func(t *testing.T, data []byte) {
		store, err := BuildStore(parsed)
		require.NoError(t, err)
		var buf bytes.Buffer
		w := NewJSONLWriter(&buf)
		h := NewHandler(store, w, HandlerOptions{TraceDecoded: true, TraceRaw: true})
		ctx := context.Background()

		n := 0
		for ; len(data) >= 6 && n < 64; data, n = data[6:], n+1 {
			kind, unit, val := data[0]%11, data[1], data[5]
			addr := uint16(data[2])<<8 | uint16(data[3])
			qty := uint16(data[4])
			regs, bits := make([]uint16, qty), make([]bool, qty)
			for i := range regs {
				regs[i], bits[i] = uint16(val)+uint16(i), (int(val)+i)%2 == 0
			}
			var got int
			var herr error
			isRead := kind <= 3 || kind == 10
			switch kind {
			case 9:
				_ = h.HandleMaskWrite(ctx, &modbus.MaskWriteRequest{UnitID: unit, FunctionCode: 22, Addr: addr, AndMask: uint16(val) << 8, OrMask: uint16(qty)})
			case 10:
				var res []uint16
				res, herr = h.HandleReadWriteRegisters(ctx, &modbus.ReadWriteRegistersRequest{
					UnitID: unit, FunctionCode: 23, ReadAddr: addr, ReadQty: qty, WriteAddr: addr + uint16(val%8), WriteValues: regs[:(len(regs)+1)/2],
				})
				got = len(res)
			case 0:
				var res []bool
				res, herr = h.HandleCoils(ctx, &modbus.CoilsRequest{UnitID: unit, FunctionCode: 1, Addr: addr, Quantity: qty})
				got = len(res)
			case 1:
				var res []bool
				res, herr = h.HandleDiscreteInputs(ctx, &modbus.DiscreteInputsRequest{UnitID: unit, FunctionCode: 2, Addr: addr, Quantity: qty})
				got = len(res)
			case 2:
				var res []uint16
				res, herr = h.HandleHoldingRegisters(ctx, &modbus.HoldingRegistersRequest{UnitID: unit, FunctionCode: 3, Addr: addr, Quantity: qty})
				got = len(res)
			case 3:
				var res []uint16
				res, herr = h.HandleInputRegisters(ctx, &modbus.InputRegistersRequest{UnitID: unit, FunctionCode: 4, Addr: addr, Quantity: qty})
				got = len(res)
			case 4:
				_, _ = h.HandleCoils(ctx, &modbus.CoilsRequest{UnitID: unit, FunctionCode: 5, Addr: addr, IsWrite: true, Args: []bool{val%2 == 0}})
			case 5:
				_, _ = h.HandleHoldingRegisters(ctx, &modbus.HoldingRegistersRequest{UnitID: unit, FunctionCode: 6, Addr: addr, IsWrite: true, Args: []uint16{uint16(val)}})
			case 6:
				_, _ = h.HandleCoils(ctx, &modbus.CoilsRequest{UnitID: unit, FunctionCode: 15, Addr: addr, IsWrite: true, Args: bits})
			case 7:
				_, _ = h.HandleHoldingRegisters(ctx, &modbus.HoldingRegistersRequest{UnitID: unit, FunctionCode: 16, Addr: addr, IsWrite: true, Args: regs})
			default:
				_, _ = h.HandleDeviceIdentification(ctx, &modbus.DeviceIdentificationRequest{
					UnitID: unit, MEIType: modbus.MEIReadDeviceIdentification, Category: modbus.DeviceIDCategory(val), ObjectID: modbus.DeviceIDObjectID(qty),
				})
			}
			if errors.Is(herr, modbus.ErrNoResponse) {
				continue
			}
			if isRead && herr == nil && got != int(qty) {
				t.Fatalf("request %d: %d values for quantity %d", n, got, qty)
			}
		}
		require.NoError(t, w.Close())

		lines := splitJSONL(buf.String())
		require.Len(t, lines, n)
		for i, line := range lines {
			if err := validateBytesAgainstSchema(TraceSchemaVersion, []byte(line)); err != nil {
				t.Fatalf("trace line %d is not valid: %v\n%s", i+1, err, line)
			}
		}
		recs := decodeRecords(t, lines)
		requireContiguousSeq(t, recs)
		requireReplayable(t, fx, recs)
	})
}
