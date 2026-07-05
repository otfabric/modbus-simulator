package simulator

import (
	"fmt"
	"strings"
)

// FixtureSummary is the structured result of `modbus-simulator validate`.
type FixtureSummary struct {
	Valid         bool            `json:"valid"`
	SchemaVersion string          `json:"schemaVersion"`
	Name          string          `json:"name,omitempty"`
	Devices       []DeviceSummary `json:"devices"`
}

// DeviceSummary reports per-device point counts and enabled features.
type DeviceSummary struct {
	UnitID           uint8  `json:"unitId"`
	Name             string `json:"name,omitempty"`
	Coils            int    `json:"coils"`
	DiscreteInputs   int    `json:"discreteInputs"`
	HoldingRegisters int    `json:"holdingRegisters"`
	InputRegisters   int    `json:"inputRegisters"`
	Identity         bool   `json:"identity"`
	Exceptions       int    `json:"exceptions"`
	Faults           int    `json:"faults"`
}

// Summarize builds a FixtureSummary from a validated fixture.
func Summarize(f *Fixture) FixtureSummary {
	s := FixtureSummary{
		Valid:         true,
		SchemaVersion: f.SchemaVersion,
		Name:          f.Name,
		Devices:       make([]DeviceSummary, 0, len(f.Devices)),
	}
	for i := range f.Devices {
		d := &f.Devices[i]
		ds := DeviceSummary{
			UnitID:           d.UnitID,
			Name:             d.Name,
			Coils:            len(d.State.Coils),
			DiscreteInputs:   len(d.State.DiscreteInputs),
			HoldingRegisters: len(d.State.HoldingRegisters),
			InputRegisters:   len(d.State.InputRegisters),
			Identity:         d.Identity != nil,
		}
		if d.Behavior != nil {
			ds.Exceptions = len(d.Behavior.Exceptions)
			ds.Faults = len(d.Behavior.Faults)
		}
		s.Devices = append(s.Devices, ds)
	}
	return s
}

// MarshalTextOutput renders the summary for --format text.
func (s FixtureSummary) MarshalTextOutput() (string, error) {
	var b strings.Builder
	name := s.Name
	if name == "" {
		name = "(unnamed)"
	}
	fmt.Fprintf(&b, "✅ Fixture valid: %s [%s]\n", name, s.SchemaVersion)
	fmt.Fprintf(&b, "   %d device(s)\n", len(s.Devices))
	for _, d := range s.Devices {
		dn := d.Name
		if dn == "" {
			dn = "(unnamed)"
		}
		fmt.Fprintf(&b, "   - unit %d %q: coils=%d discreteInputs=%d holdingRegisters=%d inputRegisters=%d identity=%t exceptions=%d faults=%d\n",
			d.UnitID, dn, d.Coils, d.DiscreteInputs, d.HoldingRegisters, d.InputRegisters, d.Identity, d.Exceptions, d.Faults)
	}
	return b.String(), nil
}
