package simulator

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/otfabric/go-modbus"
)

// conformityLevel advertised for FC43 (extended id, stream + individual access).
const conformityLevel = 0x83

// Handler implements the go-modbus RequestHandler and DeviceIdentificationHandler
// interfaces, applying the fixed rule-evaluation order and (optionally) emitting a
// trace record per served request.
type Handler struct {
	store     *Store
	writer    TraceWriter
	now       func() time.Time
	transport string
	seq       atomic.Uint64
	seenConns sync.Map // tracks remote addrs that already triggered resetOnConnection
}

// NewHandler builds a handler over store. writer may be nil (no trace).
func NewHandler(store *Store, writer TraceWriter) *Handler {
	return &Handler{store: store, writer: writer, now: time.Now, transport: "tcp"}
}

// reqInfo is the transport-neutral description of one request.
type reqInfo struct {
	fc         uint8
	name       string
	area       string
	unit       uint8
	clientAddr string
	address    uint16
	quantity   uint16
	isWrite    bool
	single     bool
	coilValues []bool
	regValues  []uint16
	// FC43 fields
	identity bool
	mei      int
	category int
	objectID int
}

func (r reqInfo) count() int {
	switch {
	case r.identity:
		return 0
	case r.isWrite && r.area == AreaCoils:
		return len(r.coilValues)
	case r.isWrite:
		return len(r.regValues)
	default:
		return int(r.quantity)
	}
}

func (r reqInfo) matchQuantity() uint16 {
	return uint16(r.count())
}

func (r reqInfo) covers(addr uint16) bool {
	n := r.count()
	if n <= 0 {
		return false
	}
	lo := uint32(r.address)
	hi := lo + uint32(n) - 1
	return uint32(addr) >= lo && uint32(addr) <= hi
}

func (r reqInfo) coversRange(start, end uint16) bool {
	n := r.count()
	if n <= 0 {
		return false
	}
	lo := uint32(r.address)
	hi := lo + uint32(n) - 1
	return uint32(start) <= hi && uint32(end) >= lo
}

// txResult carries both the wire result and the trace fields for one request.
type txResult struct {
	bools         []bool
	regs          []uint16
	identity      *modbus.DeviceIdentificationResponse
	traceIdentity *TraceIdentity
	err           error
	ok            bool
	exceptionCode string
	fault         string
	matchedRule   string
	stateChanges  []StateChange
}

func (h *Handler) serve(ctx context.Context, r reqInfo) txResult {
	h.maybeResetOnConnection(r.clientAddr)
	start := h.now()
	res := h.evaluate(ctx, r)
	dur := float64(h.now().Sub(start).Microseconds()) / 1000.0
	h.emit(r, res, dur)
	return res
}

func (h *Handler) maybeResetOnConnection(clientAddr string) {
	if clientAddr == "" {
		return
	}
	if _, loaded := h.seenConns.LoadOrStore(clientAddr, struct{}{}); loaded {
		return
	}
	h.store.ResetOnNewConnection()
}

func (h *Handler) evaluate(ctx context.Context, r reqInfo) txResult {
	_, _, exceptions, faults, ok := h.store.deviceMeta(r.unit)
	if !ok {
		// No such simulated unit: report illegal function (device not present).
		return txResult{ok: false, exceptionCode: ExcIllegalFunction, err: modbus.ErrIllegalFunction}
	}

	// 1. configured exception rules (first match wins).
	for i := range exceptions {
		if matchRequest(exceptions[i].Match, r) {
			code := exceptions[i].ExceptionCode
			return txResult{
				ok:            false,
				exceptionCode: code,
				matchedRule:   ruleID(exceptions[i].ID, "exception", i),
				err:           exceptionErr(code),
			}
		}
	}

	// 2. configured fault rules (first match wins).
	for i := range faults {
		if matchRequest(faults[i].Match, r) {
			return h.applyFault(ctx, r, faults[i], i)
		}
	}

	// 3. + 4. built-in validation and normal state operation.
	return h.operate(r)
}

func (h *Handler) applyFault(ctx context.Context, r reqInfo, ft FaultRule, idx int) txResult {
	id := ruleID(ft.ID, "fault", idx)
	switch ft.Fault {
	case FaultDelay:
		if err := sleepCtx(ctx, time.Duration(ft.DelayMs)*time.Millisecond); err != nil {
			return txResult{ok: false, fault: FaultDelay, matchedRule: id, err: modbus.ErrServerDeviceFailure}
		}
		res := h.operate(r)
		res.matchedRule = id
		return res
	case FaultTimeout:
		// go-modbus does not let a handler suppress its response, so we approximate a
		// client-visible timeout by delaying, then returning a device-failure. The trace
		// records the intended fault semantics (ok:false, fault:timeout).
		_ = sleepCtx(ctx, time.Duration(ft.DelayMs)*time.Millisecond)
		return txResult{ok: false, fault: FaultTimeout, matchedRule: id, err: modbus.ErrServerDeviceFailure}
	case FaultConnectionClose:
		// The handler cannot close the connection; approximate with a device-failure while
		// recording the intended fault in the trace.
		return txResult{ok: false, fault: FaultConnectionClose, matchedRule: id, err: modbus.ErrServerDeviceFailure}
	default:
		return h.operate(r)
	}
}

func (h *Handler) operate(r reqInfo) txResult {
	switch r.area {
	case AreaCoils:
		if r.isWrite {
			ch, err := h.store.WriteCoils(r.unit, r.address, r.coilValues)
			return writeResult(ch, err, nil, r.coilValues)
		}
		vals, err := h.store.ReadCoils(r.unit, r.address, r.quantity)
		return boolReadResult(vals, err)
	case AreaDiscreteInputs:
		vals, err := h.store.ReadDiscrete(r.unit, r.address, r.quantity)
		return boolReadResult(vals, err)
	case AreaHoldingRegisters:
		if r.isWrite {
			ch, err := h.store.WriteHolding(r.unit, r.address, r.regValues)
			return writeResult(ch, err, r.regValues, nil)
		}
		vals, err := h.store.ReadHolding(r.unit, r.address, r.quantity)
		return regReadResult(vals, err)
	case AreaInputRegisters:
		vals, err := h.store.ReadInput(r.unit, r.address, r.quantity)
		return regReadResult(vals, err)
	case AreaIdentity:
		return h.operateIdentity(r)
	default:
		return txResult{ok: false, exceptionCode: ExcIllegalFunction, err: modbus.ErrIllegalFunction}
	}
}

func (h *Handler) operateIdentity(r reqInfo) txResult {
	id, ok := h.store.Identity(r.unit)
	if !ok {
		return txResult{ok: false, exceptionCode: ExcIllegalFunction, err: modbus.ErrIllegalFunction}
	}
	objs := []modbus.DeviceIdentificationObject{
		{ID: 0x00, Name: "VendorName", Value: id.VendorName},
		{ID: 0x01, Name: "ProductCode", Value: id.ProductCode},
		{ID: 0x02, Name: "MajorMinorRevision", Value: id.MajorMinorRevision},
	}
	for _, o := range id.Objects {
		objs = append(objs, modbus.DeviceIdentificationObject{ID: modbus.DeviceIDObjectID(o.ID), Value: o.Value})
	}
	return txResult{
		ok:       true,
		identity: &modbus.DeviceIdentificationResponse{ConformityLevel: conformityLevel, Objects: objs},
		traceIdentity: &TraceIdentity{
			VendorName:         id.VendorName,
			ProductCode:        id.ProductCode,
			MajorMinorRevision: id.MajorMinorRevision,
			Objects:            id.Objects,
		},
	}
}

func writeResult(ch []StateChange, err error, regEcho []uint16, coilEcho []bool) txResult {
	if err != nil {
		return txResult{ok: false, exceptionCode: ExcIllegalDataAddress, err: modbus.ErrIllegalDataAddress}
	}
	return txResult{ok: true, stateChanges: ch, regs: regEcho, bools: coilEcho}
}

func boolReadResult(vals []bool, err error) txResult {
	if err != nil {
		return txResult{ok: false, exceptionCode: ExcIllegalDataAddress, matchedRule: "unknownAddress", err: modbus.ErrIllegalDataAddress}
	}
	return txResult{ok: true, bools: vals}
}

func regReadResult(vals []uint16, err error) txResult {
	if err != nil {
		return txResult{ok: false, exceptionCode: ExcIllegalDataAddress, matchedRule: "unknownAddress", err: modbus.ErrIllegalDataAddress}
	}
	return txResult{ok: true, regs: vals}
}

func matchRequest(m Match, r reqInfo) bool {
	if m.FC != nil && *m.FC != r.fc {
		return false
	}
	if m.Area != "" && m.Area != r.area {
		return false
	}
	if m.Quantity != nil && *m.Quantity != r.matchQuantity() {
		return false
	}
	if m.Address != nil && !r.covers(*m.Address) {
		return false
	}
	if m.AddressRange != nil && !r.coversRange(m.AddressRange.Start, m.AddressRange.End) {
		return false
	}
	return true
}

func ruleID(id, prefix string, idx int) string {
	if id != "" {
		return id
	}
	return fmt.Sprintf("%s[%d]", prefix, idx)
}

func exceptionErr(code string) error {
	switch code {
	case ExcIllegalFunction:
		return modbus.ErrIllegalFunction
	case ExcIllegalDataAddress:
		return modbus.ErrIllegalDataAddress
	case ExcIllegalDataValue:
		return modbus.ErrIllegalDataValue
	case ExcSlaveDeviceFailure:
		return modbus.ErrServerDeviceFailure
	default:
		return modbus.ErrServerDeviceFailure
	}
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// --- go-modbus RequestHandler / DeviceIdentificationHandler ---

// HandleCoils serves FC01 (read) and FC05/FC15 (write).
func (h *Handler) HandleCoils(ctx context.Context, req *modbus.CoilsRequest) ([]bool, error) {
	r := reqInfo{fc: uint8(req.FunctionCode), area: AreaCoils, unit: req.UnitID, clientAddr: req.ClientAddr, address: req.Addr}
	if req.IsWrite {
		r.isWrite = true
		r.coilValues = req.Args
		if req.FunctionCode == 5 {
			r.name, r.single = "write_single_coil", true
		} else {
			r.name = "write_multiple_coils"
		}
	} else {
		r.quantity = req.Quantity
		r.name = "read_coils"
	}
	res := h.serve(ctx, r)
	if res.err != nil {
		return nil, res.err
	}
	return res.bools, nil
}

// HandleDiscreteInputs serves FC02 (read-only).
func (h *Handler) HandleDiscreteInputs(ctx context.Context, req *modbus.DiscreteInputsRequest) ([]bool, error) {
	r := reqInfo{fc: uint8(req.FunctionCode), name: "read_discrete_inputs", area: AreaDiscreteInputs, unit: req.UnitID, clientAddr: req.ClientAddr, address: req.Addr, quantity: req.Quantity}
	res := h.serve(ctx, r)
	if res.err != nil {
		return nil, res.err
	}
	return res.bools, nil
}

// HandleHoldingRegisters serves FC03 (read) and FC06/FC16 (write).
func (h *Handler) HandleHoldingRegisters(ctx context.Context, req *modbus.HoldingRegistersRequest) ([]uint16, error) {
	r := reqInfo{fc: uint8(req.FunctionCode), area: AreaHoldingRegisters, unit: req.UnitID, clientAddr: req.ClientAddr, address: req.Addr}
	if req.IsWrite {
		r.isWrite = true
		r.regValues = req.Args
		if req.FunctionCode == 6 {
			r.name, r.single = "write_single_register", true
		} else {
			r.name = "write_multiple_registers"
		}
	} else {
		r.quantity = req.Quantity
		r.name = "read_holding_registers"
	}
	res := h.serve(ctx, r)
	if res.err != nil {
		return nil, res.err
	}
	return res.regs, nil
}

// HandleInputRegisters serves FC04 (read-only).
func (h *Handler) HandleInputRegisters(ctx context.Context, req *modbus.InputRegistersRequest) ([]uint16, error) {
	r := reqInfo{fc: uint8(req.FunctionCode), name: "read_input_registers", area: AreaInputRegisters, unit: req.UnitID, clientAddr: req.ClientAddr, address: req.Addr, quantity: req.Quantity}
	res := h.serve(ctx, r)
	if res.err != nil {
		return nil, res.err
	}
	return res.regs, nil
}

// HandleDeviceIdentification serves FC43/14 (MEI 0x0E).
func (h *Handler) HandleDeviceIdentification(ctx context.Context, req *modbus.DeviceIdentificationRequest) (*modbus.DeviceIdentificationResponse, error) {
	r := reqInfo{
		fc: 43, name: "read_device_identification", area: AreaIdentity, unit: req.UnitID, clientAddr: req.ClientAddr,
		identity: true, mei: int(req.MEIType), category: int(req.Category), objectID: int(req.ObjectID),
	}
	res := h.serve(ctx, r)
	if res.err != nil {
		return nil, res.err
	}
	return res.identity, nil
}

func (h *Handler) emit(r reqInfo, res txResult, durationMs float64) {
	if h.writer == nil {
		return
	}
	seq := h.seq.Add(1)
	rec := &TraceRecord{
		SchemaVersion: TraceSchemaVersion,
		TS:            h.now().UTC().Format(time.RFC3339Nano),
		Seq:           seq,
		Conn:          &TraceConn{Transport: h.transport, Remote: r.clientAddr},
		UnitID:        r.unit,
		Request:       buildTraceRequest(r),
		Response:      buildTraceResponse(r, res),
		StateChanges:  res.stateChanges,
		MatchedRule:   res.matchedRule,
		DurationMs:    durationMs,
	}
	if rec.StateChanges == nil {
		rec.StateChanges = []StateChange{}
	}
	_ = h.writer.Write(rec)
}

func buildTraceRequest(r reqInfo) TraceRequest {
	tr := TraceRequest{FC: r.fc, Name: r.name, Area: r.area}
	if r.identity {
		tr.MEIType = intPtr(r.mei)
		tr.ReadDeviceIDCode = intPtr(r.category)
		tr.ObjectID = intPtr(r.objectID)
		return tr
	}
	addr := r.address
	tr.Address = &addr
	switch {
	case !r.isWrite:
		tr.Quantity = r.quantity
	case r.area == AreaCoils && r.single:
		tr.Value = firstBool(r.coilValues)
	case r.area == AreaCoils:
		tr.Values = boolSlice(r.coilValues)
	case r.single:
		tr.Value = firstRegInt(r.regValues)
	default:
		tr.Values = intSlice(r.regValues)
	}
	return tr
}

func buildTraceResponse(r reqInfo, res txResult) TraceResponse {
	if res.fault != "" {
		return TraceResponse{OK: false, Fault: res.fault}
	}
	if !res.ok {
		return TraceResponse{OK: false, ExceptionCode: res.exceptionCode}
	}
	resp := TraceResponse{OK: true}
	if r.identity {
		resp.Identity = res.traceIdentity
		return resp
	}
	if !r.isWrite {
		switch r.area {
		case AreaCoils, AreaDiscreteInputs:
			resp.Values = boolSlice(res.bools)
		default:
			resp.Values = intSlice(res.regs)
		}
	}
	return resp
}

func firstBool(v []bool) any {
	if len(v) == 0 {
		return false
	}
	return v[0]
}

func firstRegInt(v []uint16) any {
	if len(v) == 0 {
		return 0
	}
	return int(v[0])
}

func boolSlice(v []bool) []bool {
	out := make([]bool, len(v))
	copy(out, v)
	return out
}

func intSlice(v []uint16) []int {
	out := make([]int, len(v))
	for i := range v {
		out[i] = int(v[i])
	}
	return out
}
