package simulator

import (
	"encoding/json"
	"fmt"
	"sort"
	"sync"
)

// StateChange records a single mutated point (emitted in the trace for writes).
// Before/After are bool for coil areas and int for register areas.
type StateChange struct {
	// UnitID is set only in the record of a broadcast, which changes several units.
	UnitID  *uint8 `json:"unitId,omitempty"`
	Area    string `json:"area"`
	Address uint16 `json:"address"`
	Before  any    `json:"before"`
	After   any    `json:"after"`
}

// addrError is the error type of ErrIllegalAddress.
type addrError struct{ msg string }

func (e *addrError) Error() string { return e.msg }

// ErrIllegalAddress indicates a read/write to an address that must fail with
// illegal_data_address (read-only write, unknown write, or unknown read under the
// exception policy).
var ErrIllegalAddress = &addrError{msg: "illegal data address"}

type boolCell struct {
	value    bool
	writable bool
}

type regCell struct {
	value    uint16
	writable bool
}

// deviceState is the mutable part of a device: the areas a client can write.
type deviceState struct {
	coils   map[uint16]*boolCell
	holding map[uint16]*regCell
}

func (st *deviceState) clone() *deviceState {
	cp := &deviceState{
		coils:   make(map[uint16]*boolCell, len(st.coils)),
		holding: make(map[uint16]*regCell, len(st.holding)),
	}
	for a, c := range st.coils {
		cell := *c
		cp.coils[a] = &cell
	}
	for a, c := range st.holding {
		cell := *c
		cp.holding[a] = &cell
	}
	return cp
}

type runtimeDevice struct {
	unitID         uint8
	unknownAddress string
	stateMode      string
	identity       *Identity
	exceptions     []ExceptionRule
	faults         []FaultRule

	discrete map[uint16]bool
	input    map[uint16]uint16

	registerFields []registerField

	// initial holds the fixture values. shared is the state of a persistent device.
	// A resetOnConnection device has no shared state: every connection works on a
	// copy of initial of its own, kept in sessions until the connection ends.
	initial  *deviceState
	shared   *deviceState
	sessions map[string]*deviceState
}

// state returns the state a connection works on.
func (rd *runtimeDevice) state(conn string) *deviceState {
	if rd.stateMode != StateModeResetOnConnection {
		return rd.shared
	}
	st, ok := rd.sessions[conn]
	if !ok {
		st = rd.initial.clone()
		rd.sessions[conn] = st
	}
	return st
}

// reset restores the fixture values and ends every session.
func (rd *runtimeDevice) reset() {
	rd.shared = rd.initial.clone()
	rd.sessions = map[string]*deviceState{}
}

// Store is the concurrent-safe in-memory state of all simulated devices.
type Store struct {
	mu        sync.Mutex
	devices   map[uint8]*runtimeDevice
	broadcast bool
}

// BuildStore constructs a runtime store from a validated fixture.
func BuildStore(f *Fixture) (*Store, error) {
	s := &Store{devices: make(map[uint8]*runtimeDevice, len(f.Devices)), broadcast: f.Broadcast}
	for i := range f.Devices {
		d := &f.Devices[i]
		rd := &runtimeDevice{
			unitID:         d.UnitID,
			unknownAddress: f.ResolvedUnknownAddress(d),
			stateMode:      f.ResolvedStateMode(d),
			identity:       d.Identity,
			discrete:       map[uint16]bool{},
			input:          map[uint16]uint16{},
			initial:        &deviceState{coils: map[uint16]*boolCell{}, holding: map[uint16]*regCell{}},
		}
		if d.Behavior != nil {
			rd.exceptions = d.Behavior.Exceptions
			rd.faults = d.Behavior.Faults
		}
		for _, p := range d.State.Coils {
			b, err := pointBool(p)
			if err != nil {
				return nil, fmt.Errorf("unit %d coils address %d: %w", d.UnitID, p.Address, err)
			}
			rd.initial.coils[p.Address] = &boolCell{value: b, writable: writableOrDefault(p, true)}
		}
		for _, p := range d.State.DiscreteInputs {
			b, err := pointBool(p)
			if err != nil {
				return nil, fmt.Errorf("unit %d discreteInputs address %d: %w", d.UnitID, p.Address, err)
			}
			rd.discrete[p.Address] = b
		}
		for _, p := range d.State.HoldingRegisters {
			if err := rd.addRegisterPoints(AreaHoldingRegisters, p, true); err != nil {
				return nil, fmt.Errorf("unit %d holdingRegisters address %d: %w", d.UnitID, p.Address, err)
			}
		}
		for _, p := range d.State.InputRegisters {
			if err := rd.addRegisterPoints(AreaInputRegisters, p, false); err != nil {
				return nil, fmt.Errorf("unit %d inputRegisters address %d: %w", d.UnitID, p.Address, err)
			}
		}
		rd.reset()
		s.devices[d.UnitID] = rd
	}
	return s, nil
}

// HasDevice reports whether a unit id is served by this store.
func (s *Store) HasDevice(unit uint8) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.devices[unit]
	return ok
}

// Units returns the served unit ids in ascending order.
func (s *Store) Units() []uint8 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.units()
}

func (s *Store) units() []uint8 {
	out := make([]uint8, 0, len(s.devices))
	for u := range s.devices {
		out = append(out, u)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Broadcast reports whether requests to unit 0 are executed as broadcasts.
func (s *Store) Broadcast() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.broadcast
}

func (s *Store) device(unit uint8) (*runtimeDevice, bool) {
	rd, ok := s.devices[unit]
	return rd, ok
}

// EndSession discards the state a connection had on resetOnConnection devices.
func (s *Store) EndSession(conn string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, rd := range s.devices {
		delete(rd.sessions, conn)
	}
}

// Sessions returns the number of connections that hold state of their own.
func (s *Store) Sessions() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	conns := map[string]struct{}{}
	for _, rd := range s.devices {
		for conn := range rd.sessions {
			conns[conn] = struct{}{}
		}
	}
	return len(conns)
}

// ResetDevice restores a device to its fixture values, for every connection. It
// reports whether the unit exists.
func (s *Store) ResetDevice(unit uint8) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	rd, ok := s.devices[unit]
	if ok {
		rd.reset()
	}
	return ok
}

// ResetAll restores every device to its fixture values.
func (s *Store) ResetAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, rd := range s.devices {
		rd.reset()
	}
}

// Replace makes the store serve the devices of other, a store built from another
// fixture. All state and every session of the old devices is dropped.
func (s *Store) Replace(other *Store) {
	other.mu.Lock()
	devices, broadcast := other.devices, other.broadcast
	other.mu.Unlock()

	s.mu.Lock()
	defer s.mu.Unlock()
	s.devices, s.broadcast = devices, broadcast
}

func (s *Store) deviceMeta(unit uint8) (exceptions []ExceptionRule, faults []FaultRule, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rd, found := s.device(unit)
	if !found {
		return nil, nil, false
	}
	return rd.exceptions, rd.faults, true
}

// Identity returns the identity for a unit, if configured.
func (s *Store) Identity(unit uint8) (*Identity, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rd, ok := s.device(unit)
	if !ok || rd.identity == nil {
		return nil, false
	}
	return rd.identity, true
}

// The exported accessors below work on the state of the anonymous connection "",
// which is the shared state for every persistent device. The handler passes the
// client's address instead.

// ReadCoils reads quantity coils starting at start for unit.
func (s *Store) ReadCoils(unit uint8, start, quantity uint16) ([]bool, error) {
	return s.readBits("", unit, start, quantity, true)
}

// ReadDiscrete reads quantity discrete inputs starting at start for unit.
func (s *Store) ReadDiscrete(unit uint8, start, quantity uint16) ([]bool, error) {
	return s.readBits("", unit, start, quantity, false)
}

func (s *Store) readBits(conn string, unit uint8, start, quantity uint16, coilArea bool) ([]bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rd, ok := s.device(unit)
	if !ok {
		return nil, ErrIllegalAddress
	}
	// A request that runs past the last address is illegal under every unknownAddress policy.
	if uint32(start)+uint32(quantity) > MaxRegisterValue+1 {
		return nil, ErrIllegalAddress
	}
	st := rd.state(conn)
	out := make([]bool, 0, quantity)
	for i := uint16(0); i < quantity; i++ {
		a := start + i
		var val, present bool
		if coilArea {
			if c, e := st.coils[a]; e {
				val, present = c.value, true
			}
		} else {
			val, present = rd.discrete[a]
		}
		if !present && rd.unknownAddress != UnknownAddressZeroForReads {
			return nil, ErrIllegalAddress
		}
		out = append(out, val)
	}
	return out, nil
}

// ReadHolding reads quantity holding registers starting at start for unit.
func (s *Store) ReadHolding(unit uint8, start, quantity uint16) ([]uint16, error) {
	return s.readRegs("", unit, start, quantity, true)
}

// ReadInput reads quantity input registers starting at start for unit.
func (s *Store) ReadInput(unit uint8, start, quantity uint16) ([]uint16, error) {
	return s.readRegs("", unit, start, quantity, false)
}

func (s *Store) readRegs(conn string, unit uint8, start, quantity uint16, holdingArea bool) ([]uint16, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rd, ok := s.device(unit)
	if !ok {
		return nil, ErrIllegalAddress
	}
	return rd.readRegs(rd.state(conn), start, quantity, holdingArea)
}

func (rd *runtimeDevice) readRegs(st *deviceState, start, quantity uint16, holdingArea bool) ([]uint16, error) {
	// A request that runs past the last address is illegal under every unknownAddress policy.
	if uint32(start)+uint32(quantity) > MaxRegisterValue+1 {
		return nil, ErrIllegalAddress
	}
	out := make([]uint16, 0, quantity)
	for i := uint16(0); i < quantity; i++ {
		a := start + i
		var val uint16
		var present bool
		if holdingArea {
			if c, e := st.holding[a]; e {
				val, present = c.value, true
			}
		} else {
			val, present = rd.input[a]
		}
		if !present && rd.unknownAddress != UnknownAddressZeroForReads {
			return nil, ErrIllegalAddress
		}
		out = append(out, val)
	}
	return out, nil
}

// WriteCoils writes values to coils starting at start, validating all targets first
// (validate-all-then-apply). Unknown or read-only targets fail with ErrIllegalAddress
// and mutate nothing.
func (s *Store) WriteCoils(unit uint8, start uint16, values []bool) ([]StateChange, error) {
	return s.writeCoils("", unit, start, values)
}

func (s *Store) writeCoils(conn string, unit uint8, start uint16, values []bool) ([]StateChange, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rd, ok := s.device(unit)
	if !ok {
		return nil, ErrIllegalAddress
	}
	return rd.state(conn).writeCoils(start, values)
}

func (st *deviceState) writeCoils(start uint16, values []bool) ([]StateChange, error) {
	for i := range values {
		addr := uint32(start) + uint32(i)
		if addr > MaxRegisterValue {
			return nil, ErrIllegalAddress
		}
		c, present := st.coils[uint16(addr)]
		if !present || !c.writable {
			return nil, ErrIllegalAddress
		}
	}
	changes := make([]StateChange, 0, len(values))
	for i, v := range values {
		a := start + uint16(i)
		c := st.coils[a]
		if c.value != v {
			changes = append(changes, StateChange{Area: AreaCoils, Address: a, Before: c.value, After: v})
			c.value = v
		}
	}
	return changes, nil
}

// WriteHolding writes values to holding registers starting at start, validating all
// targets first. Unknown or read-only targets fail with ErrIllegalAddress.
func (s *Store) WriteHolding(unit uint8, start uint16, values []uint16) ([]StateChange, error) {
	return s.writeHolding("", unit, start, values)
}

func (s *Store) writeHolding(conn string, unit uint8, start uint16, values []uint16) ([]StateChange, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rd, ok := s.device(unit)
	if !ok {
		return nil, ErrIllegalAddress
	}
	st := rd.state(conn)
	if !st.holdingWritable(start, len(values)) {
		return nil, ErrIllegalAddress
	}
	return st.applyHolding(start, values), nil
}

// holdingWritable reports whether n holding registers from start exist and are writable.
func (st *deviceState) holdingWritable(start uint16, n int) bool {
	for i := 0; i < n; i++ {
		addr := uint32(start) + uint32(i)
		if addr > MaxRegisterValue {
			return false
		}
		if c, present := st.holding[uint16(addr)]; !present || !c.writable {
			return false
		}
	}
	return true
}

// applyHolding writes values that holdingWritable has accepted.
func (st *deviceState) applyHolding(start uint16, values []uint16) []StateChange {
	changes := make([]StateChange, 0, len(values))
	for i, v := range values {
		a := start + uint16(i)
		c := st.holding[a]
		if c.value != v {
			changes = append(changes, StateChange{Area: AreaHoldingRegisters, Address: a, Before: int(c.value), After: int(v)})
			c.value = v
		}
	}
	return changes
}

// maskWrite serves FC22: the register becomes (current AND andMask) OR (orMask AND NOT andMask).
func (s *Store) maskWrite(conn string, unit uint8, addr, andMask, orMask uint16) ([]StateChange, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rd, ok := s.device(unit)
	if !ok {
		return nil, ErrIllegalAddress
	}
	return rd.state(conn).maskWrite(addr, andMask, orMask)
}

func (st *deviceState) maskWrite(addr, andMask, orMask uint16) ([]StateChange, error) {
	if !st.holdingWritable(addr, 1) {
		return nil, ErrIllegalAddress
	}
	cur := st.holding[addr].value
	return st.applyHolding(addr, []uint16{(cur & andMask) | (orMask &^ andMask)}), nil
}

// readWriteHolding serves FC23: the write is applied first, then the read is made. Both
// are checked before anything is written, so a request that fails changes nothing.
func (s *Store) readWriteHolding(conn string, unit uint8, readStart, quantity, writeStart uint16, values []uint16) ([]uint16, []StateChange, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rd, ok := s.device(unit)
	if !ok {
		return nil, nil, ErrIllegalAddress
	}
	st := rd.state(conn)
	if !st.holdingWritable(writeStart, len(values)) {
		return nil, nil, ErrIllegalAddress
	}
	if _, err := rd.readRegs(st, readStart, quantity, true); err != nil {
		return nil, nil, err
	}
	changes := st.applyHolding(writeStart, values)
	read, _ := rd.readRegs(st, readStart, quantity, true)
	return read, changes, nil
}

// broadcastWrite applies one write to every device, in ascending unit order. A device
// that cannot take the write (unknown or read-only address) ignores it, as a device on a
// serial line would. Every state change names its unit.
func (s *Store) broadcastWrite(conn string, write func(st *deviceState) ([]StateChange, error)) []StateChange {
	s.mu.Lock()
	defer s.mu.Unlock()
	changes := []StateChange{}
	for _, unit := range s.units() {
		ch, err := write(s.devices[unit].state(conn))
		if err != nil {
			continue
		}
		for i := range ch {
			u := unit
			ch[i].UnitID = &u
		}
		changes = append(changes, ch...)
	}
	return changes
}

func (rd *runtimeDevice) addRegisterPoints(area string, p Point, holdingArea bool) error {
	regs, err := encodePointRegisters(p)
	if err != nil {
		return err
	}
	writable := writableOrDefault(p, true)
	for i, v := range regs {
		addr := p.Address + uint16(i)
		if holdingArea {
			rd.initial.holding[addr] = &regCell{value: v, writable: writable}
		} else {
			rd.input[addr] = v
		}
	}
	if p.Codec != "" {
		rc, err := resolveCodec(p.Codec)
		if err != nil {
			return err
		}
		rd.registerFields = append(rd.registerFields, registerField{
			area:         area,
			startAddress: p.Address,
			codecID:      p.Codec,
			name:         p.Name,
			description:  p.Description,
			codec:        rc,
		})
	}
	return nil
}

// DecodeFieldsFullyContained returns decoded codec fields fully contained in the
// request window [start, start+quantity). Partial overlaps are omitted.
func (s *Store) DecodeFieldsFullyContained(unit uint8, area string, start, quantity uint16) []DecodedField {
	return s.decodeFields("", unit, area, start, quantity)
}

func (s *Store) decodeFields(conn string, unit uint8, area string, start, quantity uint16) []DecodedField {
	if quantity == 0 {
		return nil
	}
	reqEnd := uint32(start) + uint32(quantity)

	s.mu.Lock()
	defer s.mu.Unlock()
	rd, ok := s.device(unit)
	if !ok {
		return nil
	}
	st := rd.state(conn)

	var out []DecodedField
	for _, f := range rd.registerFields {
		if f.area != area {
			continue
		}
		fieldEnd := uint32(f.startAddress) + uint32(f.registerCount())
		if uint32(f.startAddress) < uint32(start) || fieldEnd > reqEnd {
			continue
		}
		regs, ok := rd.fieldRegisters(st, f)
		if !ok {
			continue
		}
		entry := DecodedField{
			Area:    area,
			Address: f.startAddress,
			Codec:   f.codecID,
			Name:    f.name,
		}
		decoded, err := f.codec.DecodeRegistersAny(regs)
		if err != nil {
			entry.Error = err.Error()
		} else {
			entry.Value = valueForTrace(decoded, f.codec.ValueKind())
		}
		out = append(out, entry)
	}
	return out
}

// fieldRegisters returns the current registers of a codec field, or false when any of
// them is missing from the device.
func (rd *runtimeDevice) fieldRegisters(st *deviceState, f registerField) ([]uint16, bool) {
	regs := make([]uint16, f.registerCount())
	for i := range regs {
		addr := f.startAddress + uint16(i)
		switch f.area {
		case AreaHoldingRegisters:
			c, ok := st.holding[addr]
			if !ok {
				return nil, false
			}
			regs[i] = c.value
		case AreaInputRegisters:
			v, ok := rd.input[addr]
			if !ok {
				return nil, false
			}
			regs[i] = v
		default:
			return nil, false
		}
	}
	return regs, true
}

func writableOrDefault(p Point, def bool) bool {
	if p.Writable != nil {
		return *p.Writable
	}
	return def
}

func pointBool(p Point) (bool, error) {
	b, ok := p.Value.(bool)
	if !ok {
		return false, fmt.Errorf("value must be a boolean")
	}
	return b, nil
}

func pointReg(p Point) (uint16, error) {
	n, ok := p.Value.(json.Number)
	if !ok {
		return 0, fmt.Errorf("value must be an integer")
	}
	iv, err := n.Int64()
	if err != nil {
		return 0, fmt.Errorf("value must be an integer: %w", err)
	}
	if iv < 0 || iv > MaxRegisterValue {
		return 0, fmt.Errorf("value %d out of range 0-%d", iv, MaxRegisterValue)
	}
	return uint16(iv), nil
}
