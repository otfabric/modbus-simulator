package simulator

import (
	"encoding/json"
	"fmt"
	"sync"
)

// StateChange records a single mutated point (emitted in the trace for writes).
// Before/After are bool for coil areas and int for register areas.
type StateChange struct {
	Area    string `json:"area"`
	Address uint16 `json:"address"`
	Before  any    `json:"before"`
	After   any    `json:"after"`
}

// errUnknownAddress is a sentinel meaning "address not present in the fixture".
// The handler translates it per the device unknownAddress policy.
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

type runtimeDevice struct {
	unitID         uint8
	unknownAddress string
	stateMode      string
	identity       *Identity
	exceptions     []ExceptionRule
	faults         []FaultRule

	coils    map[uint16]*boolCell
	discrete map[uint16]bool
	holding  map[uint16]*regCell
	input    map[uint16]uint16

	initCoils   map[uint16]boolCell
	initHolding map[uint16]regCell
}

// Store is the concurrent-safe in-memory state of all simulated devices.
type Store struct {
	mu      sync.RWMutex
	devices map[uint8]*runtimeDevice
}

// BuildStore constructs a runtime store from a validated fixture.
func BuildStore(f *Fixture) (*Store, error) {
	s := &Store{devices: make(map[uint8]*runtimeDevice, len(f.Devices))}
	for i := range f.Devices {
		d := &f.Devices[i]
		rd := &runtimeDevice{
			unitID:         d.UnitID,
			unknownAddress: f.ResolvedUnknownAddress(d),
			stateMode:      f.ResolvedStateMode(d),
			identity:       d.Identity,
			coils:          map[uint16]*boolCell{},
			discrete:       map[uint16]bool{},
			holding:        map[uint16]*regCell{},
			input:          map[uint16]uint16{},
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
			rd.coils[p.Address] = &boolCell{value: b, writable: writableOrDefault(p, true)}
		}
		for _, p := range d.State.DiscreteInputs {
			b, err := pointBool(p)
			if err != nil {
				return nil, fmt.Errorf("unit %d discreteInputs address %d: %w", d.UnitID, p.Address, err)
			}
			rd.discrete[p.Address] = b
		}
		for _, p := range d.State.HoldingRegisters {
			v, err := pointReg(p)
			if err != nil {
				return nil, fmt.Errorf("unit %d holdingRegisters address %d: %w", d.UnitID, p.Address, err)
			}
			rd.holding[p.Address] = &regCell{value: v, writable: writableOrDefault(p, true)}
		}
		for _, p := range d.State.InputRegisters {
			v, err := pointReg(p)
			if err != nil {
				return nil, fmt.Errorf("unit %d inputRegisters address %d: %w", d.UnitID, p.Address, err)
			}
			rd.input[p.Address] = v
		}
		rd.snapshot()
		s.devices[d.UnitID] = rd
	}
	return s, nil
}

// snapshot captures the writable areas' initial state for resetOnConnection.
func (rd *runtimeDevice) snapshot() {
	rd.initCoils = make(map[uint16]boolCell, len(rd.coils))
	for a, c := range rd.coils {
		rd.initCoils[a] = *c
	}
	rd.initHolding = make(map[uint16]regCell, len(rd.holding))
	for a, c := range rd.holding {
		rd.initHolding[a] = *c
	}
}

// HasDevice reports whether a unit id is served by this store.
func (s *Store) HasDevice(unit uint8) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.devices[unit]
	return ok
}

// Units returns the served unit ids (unsorted).
func (s *Store) Units() []uint8 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]uint8, 0, len(s.devices))
	for u := range s.devices {
		out = append(out, u)
	}
	return out
}

func (s *Store) device(unit uint8) (*runtimeDevice, bool) {
	rd, ok := s.devices[unit]
	return rd, ok
}

// ResetOnNewConnection restores writable areas for devices configured with
// stateMode resetOnConnection (called when a new TCP client is first seen).
func (s *Store) ResetOnNewConnection() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, rd := range s.devices {
		if rd.stateMode != StateModeResetOnConnection {
			continue
		}
		rd.resetWritable()
	}
}

func (rd *runtimeDevice) resetWritable() {
	rd.coils = make(map[uint16]*boolCell, len(rd.initCoils))
	for a, c := range rd.initCoils {
		cp := c
		rd.coils[a] = &cp
	}
	rd.holding = make(map[uint16]*regCell, len(rd.initHolding))
	for a, c := range rd.initHolding {
		cp := c
		rd.holding[a] = &cp
	}
}

// ResetDevice restores a device's writable areas to their fixture values.
func (s *Store) ResetDevice(unit uint8) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rd, ok := s.devices[unit]
	if !ok {
		return
	}
	rd.resetWritable()
}

// ResetAll restores every device (used for a global reset on new connection).
func (s *Store) ResetAll() {
	for _, u := range s.Units() {
		s.ResetDevice(u)
	}
}

func (s *Store) deviceMeta(unit uint8) (unknownAddress, stateMode string, exceptions []ExceptionRule, faults []FaultRule, ok bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rd, found := s.device(unit)
	if !found {
		return "", "", nil, nil, false
	}
	return rd.unknownAddress, rd.stateMode, rd.exceptions, rd.faults, true
}

// Identity returns the identity for a unit, if configured.
func (s *Store) Identity(unit uint8) (*Identity, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rd, ok := s.device(unit)
	if !ok || rd.identity == nil {
		return nil, false
	}
	return rd.identity, true
}

// ReadCoils reads quantity coils starting at start for unit.
func (s *Store) ReadCoils(unit uint8, start, quantity uint16) ([]bool, error) {
	return s.readBits(unit, start, quantity, true)
}

// ReadDiscrete reads quantity discrete inputs starting at start for unit.
func (s *Store) ReadDiscrete(unit uint8, start, quantity uint16) ([]bool, error) {
	return s.readBits(unit, start, quantity, false)
}

func (s *Store) readBits(unit uint8, start, quantity uint16, coilArea bool) ([]bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rd, ok := s.device(unit)
	if !ok {
		return nil, ErrIllegalAddress
	}
	out := make([]bool, 0, quantity)
	for i := uint16(0); i < quantity; i++ {
		addr := uint32(start) + uint32(i)
		var val bool
		var present bool
		if addr <= MaxRegisterValue {
			a := uint16(addr)
			if coilArea {
				if c, e := rd.coils[a]; e {
					val, present = c.value, true
				}
			} else {
				if b, e := rd.discrete[a]; e {
					val, present = b, true
				}
			}
		}
		if !present {
			if rd.unknownAddress == UnknownAddressZeroForReads {
				val = false
			} else {
				return nil, ErrIllegalAddress
			}
		}
		out = append(out, val)
	}
	return out, nil
}

// ReadHolding reads quantity holding registers starting at start for unit.
func (s *Store) ReadHolding(unit uint8, start, quantity uint16) ([]uint16, error) {
	return s.readRegs(unit, start, quantity, true)
}

// ReadInput reads quantity input registers starting at start for unit.
func (s *Store) ReadInput(unit uint8, start, quantity uint16) ([]uint16, error) {
	return s.readRegs(unit, start, quantity, false)
}

func (s *Store) readRegs(unit uint8, start, quantity uint16, holdingArea bool) ([]uint16, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rd, ok := s.device(unit)
	if !ok {
		return nil, ErrIllegalAddress
	}
	out := make([]uint16, 0, quantity)
	for i := uint16(0); i < quantity; i++ {
		addr := uint32(start) + uint32(i)
		var val uint16
		var present bool
		if addr <= MaxRegisterValue {
			a := uint16(addr)
			if holdingArea {
				if c, e := rd.holding[a]; e {
					val, present = c.value, true
				}
			} else {
				if v, e := rd.input[a]; e {
					val, present = v, true
				}
			}
		}
		if !present {
			if rd.unknownAddress == UnknownAddressZeroForReads {
				val = 0
			} else {
				return nil, ErrIllegalAddress
			}
		}
		out = append(out, val)
	}
	return out, nil
}

// WriteCoils writes values to coils starting at start, validating all targets first
// (validate-all-then-apply). Unknown or read-only targets fail with ErrIllegalAddress
// and mutate nothing.
func (s *Store) WriteCoils(unit uint8, start uint16, values []bool) ([]StateChange, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rd, ok := s.device(unit)
	if !ok {
		return nil, ErrIllegalAddress
	}
	for i := range values {
		addr := uint32(start) + uint32(i)
		if addr > MaxRegisterValue {
			return nil, ErrIllegalAddress
		}
		c, present := rd.coils[uint16(addr)]
		if !present || !c.writable {
			return nil, ErrIllegalAddress
		}
	}
	changes := make([]StateChange, 0, len(values))
	for i, v := range values {
		a := uint16(uint32(start) + uint32(i))
		c := rd.coils[a]
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
	s.mu.Lock()
	defer s.mu.Unlock()
	rd, ok := s.device(unit)
	if !ok {
		return nil, ErrIllegalAddress
	}
	for i := range values {
		addr := uint32(start) + uint32(i)
		if addr > MaxRegisterValue {
			return nil, ErrIllegalAddress
		}
		c, present := rd.holding[uint16(addr)]
		if !present || !c.writable {
			return nil, ErrIllegalAddress
		}
	}
	changes := make([]StateChange, 0, len(values))
	for i, v := range values {
		a := uint16(uint32(start) + uint32(i))
		c := rd.holding[a]
		if c.value != v {
			changes = append(changes, StateChange{Area: AreaHoldingRegisters, Address: a, Before: int(c.value), After: int(v)})
			c.value = v
		}
	}
	return changes, nil
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
