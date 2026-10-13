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

// HandlerOptions configures a Handler.
type HandlerOptions struct {
	// TraceDecoded adds the typed codec overlay to trace records.
	TraceDecoded bool
	// TraceRaw adds the request and response PDU bytes to trace records.
	TraceRaw bool
	// Transport is the transport name recorded in the trace; "tcp" when empty.
	Transport string
}

// Handler implements the go-modbus RequestHandler, MaskWriteHandler, ReadWriteHandler
// and DeviceIdentificationHandler interfaces, applying the fixed rule-evaluation order
// and (optionally) emitting a trace record per served request.
type Handler struct {
	store  *Store
	writer TraceWriter
	opts   HandlerOptions
	now    func() time.Time
	seq    atomic.Uint64
	// commitMu makes a state operation, its seq and its trace record one step, so that
	// the trace lists the requests in the order in which they took effect.
	commitMu sync.Mutex
	conns    sync.Map // remote addrs of the connections seen and not yet ended
}

// NewHandler builds a handler over store. writer may be nil (no trace).
func NewHandler(store *Store, writer TraceWriter, opts HandlerOptions) *Handler {
	if opts.Transport == "" {
		opts.Transport = "tcp"
	}
	return &Handler{store: store, writer: writer, opts: opts, now: time.Now}
}

// Function codes that the fixed-size go-modbus request structs do not carry.
const (
	fcMaskWrite = 22
	fcReadWrite = 23
	fcIdentity  = 43
)

// reqInfo is the transport-neutral description of one request.
type reqInfo struct {
	fc         uint8
	name       string
	area       string
	unit       uint8
	clientAddr string
	address    uint16 // start address; the read address for FC23
	quantity   uint16 // number of points read
	isWrite    bool
	single     bool
	coilValues []bool
	regValues  []uint16
	// FC22 fields
	andMask, orMask uint16
	// FC23 field: where regValues are written
	writeAddr uint16
	// FC43 fields
	identity bool
	mei      int
	category int
	objectID int
}

// span is an inclusive address range.
type span struct{ lo, hi uint32 }

func newSpan(start uint16, n int) (span, bool) {
	if n <= 0 {
		return span{}, false
	}
	return span{lo: uint32(start), hi: uint32(start) + uint32(n) - 1}, true
}

// spans returns the address ranges the request touches: two for FC23, none for FC43.
func (r reqInfo) spans() []span {
	var out []span
	add := func(start uint16, n int) {
		if sp, ok := newSpan(start, n); ok {
			out = append(out, sp)
		}
	}
	switch {
	case r.identity:
	case r.fc == fcReadWrite:
		add(r.address, int(r.quantity))
		add(r.writeAddr, len(r.regValues))
	case r.fc == fcMaskWrite:
		add(r.address, 1)
	case r.isWrite && r.area == AreaCoils:
		add(r.address, len(r.coilValues))
	case r.isWrite:
		add(r.address, len(r.regValues))
	default:
		add(r.address, int(r.quantity))
	}
	return out
}

// matchQuantity is what a rule's quantity is compared with: the number of points read
// for FC23, the number of points the request covers otherwise.
func (r reqInfo) matchQuantity() uint16 {
	if r.fc == fcReadWrite {
		return r.quantity
	}
	if sp := r.spans(); len(sp) > 0 {
		return uint16(sp[0].hi - sp[0].lo + 1)
	}
	return 0
}

func (r reqInfo) coversRange(start, end uint16) bool {
	for _, sp := range r.spans() {
		if uint32(start) <= sp.hi && uint32(end) >= sp.lo {
			return true
		}
	}
	return false
}

// returnsValues reports whether a successful response carries the points read.
func (r reqInfo) returnsValues() bool {
	return !r.identity && (!r.isWrite || r.fc == fcReadWrite)
}

// txResult carries both the wire result and the trace fields for one request.
type txResult struct {
	bools         []bool
	regs          []uint16
	identity      *modbus.DeviceIdentificationResponse
	traceIdentity *TraceIdentity
	err           error
	ok            bool
	noResponse    bool
	exceptionCode string
	fault         string
	matchedRule   string
	stateChanges  []StateChange
}

// errCloseConnection makes go-modbus close the connection without a response: its server
// does so for a request that violates the protocol, and takes a handler's
// ErrProtocolError the same way. TestE2E_FaultsOnTheWire holds it to that.
var errCloseConnection = modbus.ErrProtocolError

// action is what remains to be done for a request after the rules were applied.
type action int

const (
	actDone      action = iota // the result is final
	actOperate                 // run the request against the device's state
	actBroadcast               // run the request as a broadcast
)

func (h *Handler) serve(ctx context.Context, r reqInfo) txResult {
	h.trackConnection(ctx, r.clientAddr)
	start := h.now()
	res, act := h.evaluate(ctx, r)

	h.commitMu.Lock()
	switch act {
	case actOperate:
		rule := res.matchedRule
		res = h.operate(r)
		if rule != "" {
			res.matchedRule = rule
		}
	case actBroadcast:
		res = h.broadcast(r)
	case actDone:
	}
	dur := float64(h.now().Sub(start).Microseconds()) / 1000.0
	h.emit(r, res, dur)
	h.commitMu.Unlock()

	// A request that was already read is still served after its client has gone. It
	// must not leave state behind for a connection that no longer exists.
	if ctx.Err() != nil {
		h.endConnection(r.clientAddr)
	}
	return res
}

// trackConnection arranges for the state of a connection to be dropped when the
// connection ends. go-modbus passes the connection's context to every handler call and
// cancels it when the client disconnects.
func (h *Handler) trackConnection(ctx context.Context, clientAddr string) {
	if clientAddr == "" {
		return
	}
	if _, loaded := h.conns.LoadOrStore(clientAddr, struct{}{}); loaded {
		return
	}
	context.AfterFunc(ctx, func() { h.endConnection(clientAddr) })
}

func (h *Handler) endConnection(clientAddr string) {
	if clientAddr == "" {
		return
	}
	h.conns.Delete(clientAddr)
	h.store.EndSession(clientAddr)
}

// ResetState restores the fixture values, of one unit or of all when unit is nil, for
// every connection. It reports whether the unit exists.
func (h *Handler) ResetState(unit *uint8) bool {
	h.commitMu.Lock()
	defer h.commitMu.Unlock()
	if unit == nil {
		h.store.ResetAll()
		return true
	}
	return h.store.ResetDevice(*unit)
}

// ReplaceStore makes the handler serve the devices of a store built from another fixture.
func (h *Handler) ReplaceStore(other *Store) {
	h.commitMu.Lock()
	defer h.commitMu.Unlock()
	h.store.Replace(other)
}

// evaluate applies the configured rules. It returns either the final result, or what
// remains to be done: run the request against the state (steps 3 and 4 of the rule
// evaluation order; a matchedRule in the result is then kept) or run it as a broadcast.
func (h *Handler) evaluate(ctx context.Context, r reqInfo) (txResult, action) {
	if r.unit == 0 && h.store.Broadcast() {
		return txResult{}, actBroadcast
	}
	exceptions, faults, ok := h.store.deviceMeta(r.unit)
	if !ok {
		// No such simulated unit: report illegal function (device not present).
		return txResult{ok: false, exceptionCode: ExcIllegalFunction, err: modbus.ErrIllegalFunction}, actDone
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
			}, actDone
		}
	}

	// 2. configured fault rules (first match wins).
	for i := range faults {
		if matchRequest(faults[i].Match, r) {
			return h.applyFault(ctx, faults[i], i)
		}
	}

	// 3. + 4. built-in validation and normal state operation.
	return txResult{}, actOperate
}

func (h *Handler) applyFault(ctx context.Context, ft FaultRule, idx int) (txResult, action) {
	id := ruleID(ft.ID, "fault", idx)
	switch ft.Fault {
	case FaultDelay:
		if err := sleepCtx(ctx, time.Duration(ft.DelayMs)*time.Millisecond); err != nil {
			return txResult{ok: false, fault: FaultDelay, matchedRule: id, err: modbus.ErrServerDeviceFailure}, actDone
		}
		return txResult{matchedRule: id}, actOperate
	case FaultTimeout:
		// The request is left unanswered and the connection stays open.
		_ = sleepCtx(ctx, time.Duration(ft.DelayMs)*time.Millisecond)
		return txResult{ok: false, fault: FaultTimeout, matchedRule: id, err: modbus.ErrNoResponse}, actDone
	case FaultConnectionClose:
		return txResult{ok: false, fault: FaultConnectionClose, matchedRule: id, err: errCloseConnection}, actDone
	default:
		return txResult{}, actOperate
	}
}

func (h *Handler) operate(r reqInfo) txResult {
	conn := r.clientAddr
	switch r.area {
	case AreaCoils:
		if r.isWrite {
			ch, err := h.store.writeCoils(conn, r.unit, r.address, r.coilValues)
			return writeResult(ch, err, nil, r.coilValues)
		}
		vals, err := h.store.readBits(conn, r.unit, r.address, r.quantity, true)
		return boolReadResult(vals, err)
	case AreaDiscreteInputs:
		vals, err := h.store.readBits(conn, r.unit, r.address, r.quantity, false)
		return boolReadResult(vals, err)
	case AreaHoldingRegisters:
		switch {
		case r.fc == fcMaskWrite:
			ch, err := h.store.maskWrite(conn, r.unit, r.address, r.andMask, r.orMask)
			return writeResult(ch, err, nil, nil)
		case r.fc == fcReadWrite:
			vals, ch, err := h.store.readWriteHolding(conn, r.unit, r.address, r.quantity, r.writeAddr, r.regValues)
			return writeResult(ch, err, vals, nil)
		case r.isWrite:
			ch, err := h.store.writeHolding(conn, r.unit, r.address, r.regValues)
			return writeResult(ch, err, r.regValues, nil)
		}
		vals, err := h.store.readRegs(conn, r.unit, r.address, r.quantity, true)
		return regReadResult(vals, err)
	case AreaInputRegisters:
		vals, err := h.store.readRegs(conn, r.unit, r.address, r.quantity, false)
		return regReadResult(vals, err)
	case AreaIdentity:
		return h.operateIdentity(r)
	default:
		return txResult{ok: false, exceptionCode: ExcIllegalFunction, err: modbus.ErrIllegalFunction}
	}
}

// broadcast serves a request to unit 0 when the fixture enables broadcasts. A write is
// executed by every device that can take it; nothing else is executed. No response is
// sent either way, as on a serial line. Exception and fault rules belong to a device and
// are not applied.
func (h *Handler) broadcast(r reqInfo) txResult {
	silent := txResult{noResponse: true, err: modbus.ErrNoResponse}
	var write func(st *deviceState) ([]StateChange, error)
	switch {
	case r.fc == fcMaskWrite:
		write = func(st *deviceState) ([]StateChange, error) { return st.maskWrite(r.address, r.andMask, r.orMask) }
	case r.fc == fcReadWrite || !r.isWrite:
		return silent
	case r.area == AreaCoils:
		write = func(st *deviceState) ([]StateChange, error) { return st.writeCoils(r.address, r.coilValues) }
	default:
		write = func(st *deviceState) ([]StateChange, error) {
			if !st.holdingWritable(r.address, len(r.regValues)) {
				return nil, ErrIllegalAddress
			}
			return st.applyHolding(r.address, r.regValues), nil
		}
	}
	silent.ok = true
	silent.stateChanges = h.store.broadcastWrite(r.clientAddr, write)
	return silent
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
	if m.Address != nil && !r.coversRange(*m.Address, *m.Address) {
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

// HandleMaskWrite serves FC22.
func (h *Handler) HandleMaskWrite(ctx context.Context, req *modbus.MaskWriteRequest) error {
	r := reqInfo{
		fc: fcMaskWrite, name: "mask_write_register", area: AreaHoldingRegisters, unit: req.UnitID, clientAddr: req.ClientAddr,
		address: req.Addr, isWrite: true, single: true, andMask: req.AndMask, orMask: req.OrMask,
	}
	return h.serve(ctx, r).err
}

// HandleReadWriteRegisters serves FC23: the write is made first, then the read.
func (h *Handler) HandleReadWriteRegisters(ctx context.Context, req *modbus.ReadWriteRegistersRequest) ([]uint16, error) {
	r := reqInfo{
		fc: fcReadWrite, name: "read_write_multiple_registers", area: AreaHoldingRegisters, unit: req.UnitID, clientAddr: req.ClientAddr,
		address: req.ReadAddr, quantity: req.ReadQty, isWrite: true, writeAddr: req.WriteAddr, regValues: req.WriteValues,
	}
	res := h.serve(ctx, r)
	if res.err != nil {
		return nil, res.err
	}
	return res.regs, nil
}

// HandleDeviceIdentification serves FC43/14 (MEI 0x0E).
func (h *Handler) HandleDeviceIdentification(ctx context.Context, req *modbus.DeviceIdentificationRequest) (*modbus.DeviceIdentificationResponse, error) {
	r := reqInfo{
		fc: fcIdentity, name: "read_device_identification", area: AreaIdentity, unit: req.UnitID, clientAddr: req.ClientAddr,
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
		Conn:          &TraceConn{Transport: h.opts.Transport, Remote: r.clientAddr},
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
	if h.opts.TraceDecoded && res.ok && !res.noResponse && !r.identity {
		rec.Decoded = h.store.decodeFields(r.clientAddr, r.unit, r.area, r.address, decodeQuantity(r, res))
	}
	if h.opts.TraceRaw {
		rec.Raw = buildTraceRaw(r, res)
	}
	_ = h.writer.Write(rec)
}

func decodeQuantity(r reqInfo, res txResult) uint16 {
	if r.fc == fcReadWrite {
		return r.quantity
	}
	if r.isWrite {
		if r.single {
			return 1
		}
		if len(r.regValues) > 0 {
			return uint16(len(r.regValues))
		}
		return 1
	}
	if r.quantity > 0 {
		return r.quantity
	}
	if res.regs != nil {
		return uint16(len(res.regs))
	}
	return 0
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
	case r.fc == fcMaskWrite:
		tr.AndMask, tr.OrMask = intPtr(int(r.andMask)), intPtr(int(r.orMask))
	case r.fc == fcReadWrite:
		writeAddr := r.writeAddr
		tr.Quantity, tr.WriteAddress, tr.Values = r.quantity, &writeAddr, intSlice(r.regValues)
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
	if res.noResponse {
		return TraceResponse{OK: res.ok, NoResponse: true}
	}
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
	if r.returnsValues() {
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
