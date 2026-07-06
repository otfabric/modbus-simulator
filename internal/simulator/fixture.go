// Package simulator implements a deterministic, stateful Modbus TCP device
// simulator: fixture parsing/validation, an in-memory device store, a go-modbus
// request handler, JSONL trace output, and trace assertions.
//
// See REQUIREMENTS.md for the authoritative specification.
package simulator

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
)

// FixtureSchemaVersion is the schemaVersion identifier for fixtures.
const FixtureSchemaVersion = "modbus-simulator.fixture.v1"

// Modbus memory area names used throughout fixtures, traces and assertions.
const (
	AreaCoils            = "coils"
	AreaDiscreteInputs   = "discreteInputs"
	AreaHoldingRegisters = "holdingRegisters"
	AreaInputRegisters   = "inputRegisters"
	AreaIdentity         = "identity"
)

// unknownAddress policies.
const (
	UnknownAddressException    = "exception"
	UnknownAddressZeroForReads = "zeroForReads"
)

// stateMode values.
const (
	StateModePersistent        = "persistent"
	StateModeResetOnConnection = "resetOnConnection"
)

// Modbus exception codes expressible in a fixture (mapped to go-modbus sentinels at runtime).
const (
	ExcIllegalFunction    = "illegal_function"
	ExcIllegalDataAddress = "illegal_data_address"
	ExcIllegalDataValue   = "illegal_data_value"
	ExcSlaveDeviceFailure = "slave_device_failure"
)

// Fault types (transport/runtime behavior, not Modbus exceptions).
const (
	FaultDelay           = "delay"
	FaultTimeout         = "timeout"
	FaultConnectionClose = "connectionClose"
)

// MaxRegisterValue is the largest value a 16-bit register or address can hold.
const MaxRegisterValue = 65535

// Fixture is the top-level fixture document (schemaVersion modbus-simulator.fixture.v1).
type Fixture struct {
	Schema        string         `json:"$schema,omitempty"`
	SchemaVersion string         `json:"schemaVersion"`
	Name          string         `json:"name,omitempty"`
	Description   string         `json:"description,omitempty"`
	Defaults      *Defaults      `json:"defaults,omitempty"`
	Metadata      map[string]any `json:"metadata,omitempty"`
	Devices       []Device       `json:"devices"`
}

// Defaults are fixture-wide behavior defaults overridden per device.
type Defaults struct {
	UnknownAddress string `json:"unknownAddress,omitempty"`
	StateMode      string `json:"stateMode,omitempty"`
}

// Device is a single simulated Modbus unit.
type Device struct {
	UnitID   uint8          `json:"unitId"`
	Name     string         `json:"name,omitempty"`
	Metadata map[string]any `json:"metadata,omitempty"`
	Identity *Identity      `json:"identity,omitempty"`
	State    State          `json:"state"`
	Behavior *Behavior      `json:"behavior,omitempty"`
}

// Identity holds FC43 device identification data.
type Identity struct {
	VendorName         string           `json:"vendorName"`
	ProductCode        string           `json:"productCode"`
	MajorMinorRevision string           `json:"majorMinorRevision"`
	Objects            []IdentityObject `json:"objects,omitempty"`
}

// IdentityObject is an additional FC43 object (ids 0x03-0xFF; 0x00-0x02 are the named fields).
type IdentityObject struct {
	ID    uint8  `json:"id"`
	Value string `json:"value"`
}

// State holds the initial contents of the four Modbus memory areas.
type State struct {
	Coils            []Point `json:"coils,omitempty"`
	DiscreteInputs   []Point `json:"discreteInputs,omitempty"`
	HoldingRegisters []Point `json:"holdingRegisters,omitempty"`
	InputRegisters   []Point `json:"inputRegisters,omitempty"`
}

// Point is one register or coil entry. Value is a bool for coils/discrete inputs
// and a json.Number or typed JSON value for register areas (decoded with UseNumber).
// Codec is set for codec-backed multi-register fields (go-modbus stable ID).
type Point struct {
	Address     uint16 `json:"address"`
	Codec       string `json:"codec,omitempty"`
	Value       any    `json:"value"`
	Writable    *bool  `json:"writable,omitempty"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
}

// Behavior configures per-device policies and rules.
type Behavior struct {
	UnknownAddress string          `json:"unknownAddress,omitempty"`
	StateMode      string          `json:"stateMode,omitempty"`
	Exceptions     []ExceptionRule `json:"exceptions,omitempty"`
	Faults         []FaultRule     `json:"faults,omitempty"`
}

// Match selects requests for an exception or fault rule. All present fields must match.
type Match struct {
	FC           *uint8        `json:"fc,omitempty"`
	Area         string        `json:"area,omitempty"`
	Address      *uint16       `json:"address,omitempty"`
	AddressRange *AddressRange `json:"addressRange,omitempty"`
	Quantity     *uint16       `json:"quantity,omitempty"`
}

// AddressRange is an inclusive [Start, End] address window.
type AddressRange struct {
	Start uint16 `json:"start"`
	End   uint16 `json:"end"`
}

// ExceptionRule forces a Modbus exception for matching requests.
type ExceptionRule struct {
	ID            string `json:"id,omitempty"`
	Match         Match  `json:"match"`
	ExceptionCode string `json:"exceptionCode"`
}

// FaultRule injects transport/runtime behavior for matching requests.
type FaultRule struct {
	ID      string `json:"id,omitempty"`
	Match   Match  `json:"match"`
	Fault   string `json:"fault"`
	DelayMs int    `json:"delayMs,omitempty"`
}

// ResolvedUnknownAddress returns the effective unknownAddress policy for the device,
// falling back to fixture defaults and finally the built-in default (exception).
func (f *Fixture) ResolvedUnknownAddress(d *Device) string {
	if d.Behavior != nil && d.Behavior.UnknownAddress != "" {
		return d.Behavior.UnknownAddress
	}
	if f.Defaults != nil && f.Defaults.UnknownAddress != "" {
		return f.Defaults.UnknownAddress
	}
	return UnknownAddressException
}

// ResolvedStateMode returns the effective stateMode for the device.
func (f *Fixture) ResolvedStateMode(d *Device) string {
	if d.Behavior != nil && d.Behavior.StateMode != "" {
		return d.Behavior.StateMode
	}
	if f.Defaults != nil && f.Defaults.StateMode != "" {
		return f.Defaults.StateMode
	}
	return StateModePersistent
}

// decodeFixtureStrict decodes fixture JSON with unknown-field rejection and UseNumber,
// so register values stay exact and typos surface early (mirrors the schema's
// additionalProperties:false).
func decodeFixtureStrict(data []byte) (*Fixture, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	dec.UseNumber()
	var f Fixture
	if err := dec.Decode(&f); err != nil {
		return nil, fmt.Errorf("decode fixture JSON: %w", err)
	}
	if dec.More() {
		return nil, fmt.Errorf("decode fixture JSON: unexpected trailing content after top-level object")
	}
	return &f, nil
}

// ReadFixtureFile reads a fixture file from disk (no validation).
func ReadFixtureFile(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read fixture file: %w", err)
	}
	return data, nil
}
