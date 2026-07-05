package simulator

import (
	"context"
	"errors"
	"testing"

	"github.com/otfabric/go-modbus"
	"github.com/stretchr/testify/require"
)

// captureWriter records emitted trace records for assertions.
type captureWriter struct{ recs []*TraceRecord }

func (c *captureWriter) Write(rec *TraceRecord) error { c.recs = append(c.recs, rec); return nil }
func (c *captureWriter) Close() error                 { return nil }

func buildHandler(t *testing.T, fixtureJSON string, w TraceWriter) *Handler {
	t.Helper()
	f, err := ValidateFixtureBytes([]byte(fixtureJSON))
	require.NoError(t, err)
	store, err := BuildStore(f)
	require.NoError(t, err)
	return NewHandler(store, w)
}

const persistFixture = `{
  "schemaVersion": "modbus-simulator.fixture.v1",
  "devices": [{
    "unitId": 1,
    "state": {
      "holdingRegisters": [
        {"address": 0, "value": 2301, "writable": false},
        {"address": 1, "value": 501, "writable": true}
      ]
    }
  }]
}`

func readHolding(t *testing.T, h *Handler, addr, qty uint16) ([]uint16, error) {
	t.Helper()
	return h.HandleHoldingRegisters(context.Background(), &modbus.HoldingRegistersRequest{
		UnitID: 1, FunctionCode: modbus.FunctionCode(3), Addr: addr, Quantity: qty,
	})
}

func writeHolding(t *testing.T, h *Handler, addr uint16, vals ...uint16) error {
	t.Helper()
	fc := modbus.FunctionCode(16)
	if len(vals) == 1 {
		fc = modbus.FunctionCode(6)
	}
	_, err := h.HandleHoldingRegisters(context.Background(), &modbus.HoldingRegistersRequest{
		UnitID: 1, FunctionCode: fc, Addr: addr, IsWrite: true, Args: vals,
	})
	return err
}

func TestHandler_WriteThenReadPersists(t *testing.T) {
	h := buildHandler(t, persistFixture, nil)
	require.NoError(t, writeHolding(t, h, 1, 777))
	got, err := readHolding(t, h, 1, 1)
	require.NoError(t, err)
	require.Equal(t, []uint16{777}, got)
}

func TestHandler_ReadOnlyWriteRejected(t *testing.T) {
	h := buildHandler(t, persistFixture, nil)
	err := writeHolding(t, h, 0, 42)
	require.Error(t, err)
	require.True(t, errors.Is(err, modbus.ErrIllegalDataAddress))
}

func TestHandler_UnknownWriteRejectedBothModes(t *testing.T) {
	for _, mode := range []string{"exception", "zeroForReads"} {
		fx := `{"schemaVersion":"modbus-simulator.fixture.v1","defaults":{"unknownAddress":"` + mode + `"},
		  "devices":[{"unitId":1,"state":{"holdingRegisters":[{"address":1,"value":1,"writable":true}]}}]}`
		h := buildHandler(t, fx, nil)
		err := writeHolding(t, h, 5, 9)
		require.Errorf(t, err, "mode %s", mode)
		require.True(t, errors.Is(err, modbus.ErrIllegalDataAddress))
	}
}

func TestHandler_ZeroForReadsVsException(t *testing.T) {
	zero := buildHandler(t, `{"schemaVersion":"modbus-simulator.fixture.v1","defaults":{"unknownAddress":"zeroForReads"},
	  "devices":[{"unitId":1,"state":{"holdingRegisters":[{"address":1,"value":1}]}}]}`, nil)
	got, err := readHolding(t, zero, 5, 2)
	require.NoError(t, err)
	require.Equal(t, []uint16{0, 0}, got)

	exc := buildHandler(t, `{"schemaVersion":"modbus-simulator.fixture.v1","defaults":{"unknownAddress":"exception"},
	  "devices":[{"unitId":1,"state":{"holdingRegisters":[{"address":1,"value":1}]}}]}`, nil)
	_, err = readHolding(t, exc, 5, 2)
	require.True(t, errors.Is(err, modbus.ErrIllegalDataAddress))
}

func TestHandler_ExceptionRulePrecedesValidAddress(t *testing.T) {
	fx := `{"schemaVersion":"modbus-simulator.fixture.v1",
	  "devices":[{"unitId":1,"state":{"holdingRegisters":[{"address":1,"value":501}]},
	  "behavior":{"exceptions":[{"id":"hr1","match":{"fc":3,"address":1},"exceptionCode":"slave_device_failure"}]}}]}`
	w := &captureWriter{}
	h := buildHandler(t, fx, w)
	_, err := readHolding(t, h, 1, 1)
	require.True(t, errors.Is(err, modbus.ErrServerDeviceFailure))
	require.Len(t, w.recs, 1)
	require.Equal(t, "hr1", w.recs[0].MatchedRule)
	require.False(t, w.recs[0].Response.OK)
	require.Equal(t, ExcSlaveDeviceFailure, w.recs[0].Response.ExceptionCode)
}

func TestHandler_DelayFaultSucceedsWithMatchedRule(t *testing.T) {
	fx := `{"schemaVersion":"modbus-simulator.fixture.v1",
	  "devices":[{"unitId":1,"state":{"holdingRegisters":[{"address":1,"value":501}]},
	  "behavior":{"faults":[{"id":"lat","match":{"fc":3,"address":1},"fault":"delay","delayMs":1}]}}]}`
	w := &captureWriter{}
	h := buildHandler(t, fx, w)
	got, err := readHolding(t, h, 1, 1)
	require.NoError(t, err)
	require.Equal(t, []uint16{501}, got)
	require.Len(t, w.recs, 1)
	require.True(t, w.recs[0].Response.OK)
	require.Equal(t, "lat", w.recs[0].MatchedRule)
}

func TestHandler_ResetOnConnection(t *testing.T) {
	fx := `{"schemaVersion":"modbus-simulator.fixture.v1","defaults":{"stateMode":"resetOnConnection"},
	  "devices":[{"unitId":1,"state":{"holdingRegisters":[{"address":1,"value":501,"writable":true}]}}]}`
	h := buildHandler(t, fx, nil)
	ctx := context.Background()
	conn1 := "127.0.0.1:10001"
	conn2 := "127.0.0.1:10002"

	_, err := h.HandleHoldingRegisters(ctx, &modbus.HoldingRegistersRequest{
		UnitID: 1, FunctionCode: 6, Addr: 1, IsWrite: true, Args: []uint16{777}, ClientAddr: conn1,
	})
	require.NoError(t, err)
	got, err := h.HandleHoldingRegisters(ctx, &modbus.HoldingRegistersRequest{
		UnitID: 1, FunctionCode: 3, Addr: 1, Quantity: 1, ClientAddr: conn1,
	})
	require.NoError(t, err)
	require.Equal(t, []uint16{777}, got)

	// A new client connection resets state to the fixture values.
	got, err = h.HandleHoldingRegisters(ctx, &modbus.HoldingRegistersRequest{
		UnitID: 1, FunctionCode: 3, Addr: 1, Quantity: 1, ClientAddr: conn2,
	})
	require.NoError(t, err)
	require.Equal(t, []uint16{501}, got)
}

func TestHandler_DeviceIdentification(t *testing.T) {
	fx := `{"schemaVersion":"modbus-simulator.fixture.v1",
	  "devices":[{"unitId":1,"state":{},"identity":{"vendorName":"OTFabric","productCode":"GWTEST","majorMinorRevision":"1.0.0","objects":[{"id":128,"value":"x"}]}}]}`
	h := buildHandler(t, fx, nil)
	resp, err := h.HandleDeviceIdentification(context.Background(), &modbus.DeviceIdentificationRequest{
		UnitID: 1, MEIType: modbus.MEIReadDeviceIdentification, Category: modbus.DeviceIDBasic, ObjectID: 0,
	})
	require.NoError(t, err)
	require.Len(t, resp.Objects, 4)
	require.Equal(t, "OTFabric", resp.Objects[0].Value)

	// Device without identity → illegal function.
	h2 := buildHandler(t, `{"schemaVersion":"modbus-simulator.fixture.v1","devices":[{"unitId":1,"state":{}}]}`, nil)
	_, err = h2.HandleDeviceIdentification(context.Background(), &modbus.DeviceIdentificationRequest{UnitID: 1})
	require.True(t, errors.Is(err, modbus.ErrIllegalFunction))
}

func TestHandler_CoilWriteReadAndStateChange(t *testing.T) {
	fx := `{"schemaVersion":"modbus-simulator.fixture.v1",
	  "devices":[{"unitId":1,"state":{"coils":[{"address":0,"value":false,"writable":true}]}}]}`
	w := &captureWriter{}
	h := buildHandler(t, fx, w)
	_, err := h.HandleCoils(context.Background(), &modbus.CoilsRequest{
		UnitID: 1, FunctionCode: modbus.FunctionCode(5), Addr: 0, IsWrite: true, Args: []bool{true},
	})
	require.NoError(t, err)
	require.Len(t, w.recs, 1)
	require.Len(t, w.recs[0].StateChanges, 1)
	require.Equal(t, AreaCoils, w.recs[0].StateChanges[0].Area)
	require.Equal(t, false, w.recs[0].StateChanges[0].Before)
	require.Equal(t, true, w.recs[0].StateChanges[0].After)
}

func TestHandler_ReadDiscreteInputs(t *testing.T) {
	fx := `{"schemaVersion":"modbus-simulator.fixture.v1",
	  "devices":[{"unitId":1,"state":{"discreteInputs":[{"address":0,"value":false},{"address":1,"value":true}]}}]}`
	h := buildHandler(t, fx, nil)
	got, err := h.HandleDiscreteInputs(context.Background(), &modbus.DiscreteInputsRequest{
		UnitID: 1, FunctionCode: modbus.FunctionCode(2), Addr: 0, Quantity: 2,
	})
	require.NoError(t, err)
	require.Equal(t, []bool{false, true}, got)

	// Unknown address under the exception policy.
	_, err = h.HandleDiscreteInputs(context.Background(), &modbus.DiscreteInputsRequest{
		UnitID: 1, FunctionCode: modbus.FunctionCode(2), Addr: 5, Quantity: 1,
	})
	require.True(t, errors.Is(err, modbus.ErrIllegalDataAddress))

	// zeroForReads returns false for unknown discrete inputs.
	zero := buildHandler(t, `{"schemaVersion":"modbus-simulator.fixture.v1","defaults":{"unknownAddress":"zeroForReads"},
	  "devices":[{"unitId":1,"state":{"discreteInputs":[{"address":0,"value":true}]}}]}`, nil)
	got, err = zero.HandleDiscreteInputs(context.Background(), &modbus.DiscreteInputsRequest{
		UnitID: 1, FunctionCode: modbus.FunctionCode(2), Addr: 3, Quantity: 2,
	})
	require.NoError(t, err)
	require.Equal(t, []bool{false, false}, got)
}

func TestHandler_ReadInputRegisters(t *testing.T) {
	fx := `{"schemaVersion":"modbus-simulator.fixture.v1",
	  "devices":[{"unitId":1,"state":{"inputRegisters":[{"address":0,"value":1234},{"address":2,"value":5678}]}}]}`
	h := buildHandler(t, fx, nil)
	got, err := h.HandleInputRegisters(context.Background(), &modbus.InputRegistersRequest{
		UnitID: 1, FunctionCode: modbus.FunctionCode(4), Addr: 0, Quantity: 1,
	})
	require.NoError(t, err)
	require.Equal(t, []uint16{1234}, got)

	got, err = h.HandleInputRegisters(context.Background(), &modbus.InputRegistersRequest{
		UnitID: 1, FunctionCode: modbus.FunctionCode(4), Addr: 2, Quantity: 1,
	})
	require.NoError(t, err)
	require.Equal(t, []uint16{5678}, got)

	// Unknown address under the exception policy.
	_, err = h.HandleInputRegisters(context.Background(), &modbus.InputRegistersRequest{
		UnitID: 1, FunctionCode: modbus.FunctionCode(4), Addr: 99, Quantity: 1,
	})
	require.True(t, errors.Is(err, modbus.ErrIllegalDataAddress))

	// zeroForReads returns 0 for unknown input registers.
	zero := buildHandler(t, `{"schemaVersion":"modbus-simulator.fixture.v1","defaults":{"unknownAddress":"zeroForReads"},
	  "devices":[{"unitId":1,"state":{"inputRegisters":[{"address":0,"value":42}]}}]}`, nil)
	got, err = zero.HandleInputRegisters(context.Background(), &modbus.InputRegistersRequest{
		UnitID: 1, FunctionCode: modbus.FunctionCode(4), Addr: 5, Quantity: 2,
	})
	require.NoError(t, err)
	require.Equal(t, []uint16{0, 0}, got)
}

func TestHandler_WriteMultipleCoils(t *testing.T) {
	fx := `{"schemaVersion":"modbus-simulator.fixture.v1",
	  "devices":[{"unitId":1,"state":{"coils":[
	    {"address":0,"value":false,"writable":true},
	    {"address":1,"value":true,"writable":true},
	    {"address":2,"value":false,"writable":true}
	  ]}}]}`
	w := &captureWriter{}
	h := buildHandler(t, fx, w)
	ctx := context.Background()

	_, err := h.HandleCoils(ctx, &modbus.CoilsRequest{
		UnitID: 1, FunctionCode: modbus.FunctionCode(15), Addr: 0, IsWrite: true,
		Args: []bool{true, false, true},
	})
	require.NoError(t, err)
	require.Len(t, w.recs, 1)
	require.EqualValues(t, 15, w.recs[0].Request.FC)
	require.Equal(t, "write_multiple_coils", w.recs[0].Request.Name)
	require.Equal(t, AreaCoils, w.recs[0].Request.Area)
	require.Len(t, w.recs[0].StateChanges, 3)
	require.EqualValues(t, 0, w.recs[0].StateChanges[0].Address)
	require.EqualValues(t, 1, w.recs[0].StateChanges[1].Address)
	require.EqualValues(t, 2, w.recs[0].StateChanges[2].Address)
	require.Equal(t, true, w.recs[0].StateChanges[0].After)
	require.Equal(t, false, w.recs[0].StateChanges[1].After)
	require.Equal(t, true, w.recs[0].StateChanges[2].After)

	got, err := h.HandleCoils(ctx, &modbus.CoilsRequest{
		UnitID: 1, FunctionCode: modbus.FunctionCode(1), Addr: 0, Quantity: 3,
	})
	require.NoError(t, err)
	require.Equal(t, []bool{true, false, true}, got)
}

func TestHandler_ExceptionRuleAddressRange(t *testing.T) {
	fx := `{"schemaVersion":"modbus-simulator.fixture.v1",
	  "devices":[{"unitId":1,"state":{"holdingRegisters":[{"address":12,"value":100},{"address":20,"value":200}]},
	  "behavior":{"exceptions":[{"id":"hr-range","match":{"fc":3,"area":"holdingRegisters","addressRange":{"start":10,"end":15}},"exceptionCode":"illegal_data_address"}]}}]}`
	w := &captureWriter{}
	h := buildHandler(t, fx, w)

	// Address inside the configured range triggers the exception rule.
	_, err := readHolding(t, h, 12, 1)
	require.True(t, errors.Is(err, modbus.ErrIllegalDataAddress))
	require.Len(t, w.recs, 1)
	require.Equal(t, "hr-range", w.recs[0].MatchedRule)
	require.Equal(t, ExcIllegalDataAddress, w.recs[0].Response.ExceptionCode)

	// Partial overlap with a multi-register read also matches.
	_, err = readHolding(t, h, 14, 2)
	require.True(t, errors.Is(err, modbus.ErrIllegalDataAddress))

	// Address outside the range is served normally.
	got, err := readHolding(t, h, 20, 1)
	require.NoError(t, err)
	require.Equal(t, []uint16{200}, got)
}

func TestHandler_TimeoutFaultRecordsTrace(t *testing.T) {
	fx := `{"schemaVersion":"modbus-simulator.fixture.v1",
	  "devices":[{"unitId":1,"state":{"holdingRegisters":[{"address":1,"value":501}]},
	  "behavior":{"faults":[{"id":"to","match":{"fc":3,"address":1},"fault":"timeout","delayMs":1}]}}]}`
	w := &captureWriter{}
	h := buildHandler(t, fx, w)
	_, err := readHolding(t, h, 1, 1)
	require.True(t, errors.Is(err, modbus.ErrServerDeviceFailure))
	require.Len(t, w.recs, 1)
	require.False(t, w.recs[0].Response.OK)
	require.Equal(t, FaultTimeout, w.recs[0].Response.Fault)
	require.Equal(t, "to", w.recs[0].MatchedRule)
	require.Empty(t, w.recs[0].StateChanges)
}

func TestHandler_ConnectionCloseFaultRecordsTrace(t *testing.T) {
	fx := `{"schemaVersion":"modbus-simulator.fixture.v1",
	  "devices":[{"unitId":1,"state":{"holdingRegisters":[{"address":1,"value":501}]},
	  "behavior":{"faults":[{"id":"close","match":{"fc":3,"address":1},"fault":"connectionClose"}]}}]}`
	w := &captureWriter{}
	h := buildHandler(t, fx, w)
	_, err := readHolding(t, h, 1, 1)
	require.True(t, errors.Is(err, modbus.ErrServerDeviceFailure))
	require.Len(t, w.recs, 1)
	require.False(t, w.recs[0].Response.OK)
	require.Equal(t, FaultConnectionClose, w.recs[0].Response.Fault)
	require.Equal(t, "close", w.recs[0].MatchedRule)
	require.Empty(t, w.recs[0].StateChanges)
}
