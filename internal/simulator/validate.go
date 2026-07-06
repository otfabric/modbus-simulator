package simulator

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// ValidationError aggregates one or more human-readable validation problems.
type ValidationError struct {
	Problems []string
}

func (e *ValidationError) Error() string {
	if e == nil || len(e.Problems) == 0 {
		return "fixture is invalid"
	}
	if len(e.Problems) == 1 {
		return e.Problems[0]
	}
	return fmt.Sprintf("%d fixture problems:\n  - %s", len(e.Problems), strings.Join(e.Problems, "\n  - "))
}

// ValidateFixtureBytes validates raw fixture JSON against the embedded JSON Schema and
// the semantic rules that the schema cannot express, returning the decoded fixture on
// success.
func ValidateFixtureBytes(data []byte) (*Fixture, error) {
	version, err := peekFixtureSchemaVersion(data)
	if err != nil {
		return nil, err
	}
	if version != FixtureSchemaVersion {
		return nil, fmt.Errorf("unsupported schemaVersion %q (supported: %s)", version, FixtureSchemaVersion)
	}
	if err := validateBytesAgainstSchema(FixtureSchemaVersion, data); err != nil {
		return nil, err
	}
	f, err := decodeFixtureStrict(data)
	if err != nil {
		return nil, err
	}
	if err := validateFixtureSemantics(f); err != nil {
		return nil, err
	}
	return f, nil
}

// ValidateFixtureFile reads and validates a fixture file.
func ValidateFixtureFile(path string) (*Fixture, error) {
	data, err := ReadFixtureFile(path)
	if err != nil {
		return nil, err
	}
	return ValidateFixtureBytes(data)
}

func peekFixtureSchemaVersion(data []byte) (string, error) {
	var peek struct {
		SchemaVersion string `json:"schemaVersion"`
	}
	if err := json.Unmarshal(data, &peek); err != nil {
		return "", fmt.Errorf("decode fixture schemaVersion: %w", err)
	}
	if peek.SchemaVersion == "" {
		return "", fmt.Errorf("schemaVersion is required")
	}
	return peek.SchemaVersion, nil
}

// validateFixtureSemantics enforces rules beyond the JSON Schema: unique unit IDs,
// non-overlapping register spans, identity object-id rules, and codec checks.
func validateFixtureSemantics(f *Fixture) error {
	var problems []string

	if f.SchemaVersion != FixtureSchemaVersion {
		problems = append(problems, fmt.Sprintf("schemaVersion must be %q, got %q", FixtureSchemaVersion, f.SchemaVersion))
	}

	seenUnits := map[uint8]int{}
	for i := range f.Devices {
		d := &f.Devices[i]
		label := fmt.Sprintf("devices[%d] (unitId %d)", i, d.UnitID)
		if prev, ok := seenUnits[d.UnitID]; ok {
			problems = append(problems, fmt.Sprintf("%s: duplicate unitId (also devices[%d])", label, prev))
		} else {
			seenUnits[d.UnitID] = i
		}

		problems = append(problems, validateUniqueAddresses(label, AreaCoils, d.State.Coils)...)
		problems = append(problems, validateUniqueAddresses(label, AreaDiscreteInputs, d.State.DiscreteInputs)...)
		problems = append(problems, validateNonOverlappingSpans(label, AreaHoldingRegisters, d.State.HoldingRegisters)...)
		problems = append(problems, validateNonOverlappingSpans(label, AreaInputRegisters, d.State.InputRegisters)...)

		for _, area := range []struct {
			name   string
			points []Point
		}{
			{AreaHoldingRegisters, d.State.HoldingRegisters},
			{AreaInputRegisters, d.State.InputRegisters},
		} {
			for _, p := range area.points {
				problems = append(problems, validateCodecPoint(label, area.name, p)...)
			}
		}

		if d.Identity != nil {
			problems = append(problems, validateIdentity(label, d.Identity)...)
		}
		if d.Behavior != nil {
			for j := range d.Behavior.Exceptions {
				problems = append(problems, validateMatch(fmt.Sprintf("%s exceptions[%d]", label, j), d.Behavior.Exceptions[j].Match)...)
			}
			for j := range d.Behavior.Faults {
				problems = append(problems, validateMatch(fmt.Sprintf("%s faults[%d]", label, j), d.Behavior.Faults[j].Match)...)
			}
		}
	}

	if len(problems) > 0 {
		sort.Strings(problems)
		return &ValidationError{Problems: problems}
	}
	return nil
}

type addressSpan struct {
	start uint16
	end   uint16
	addr  uint16
}

func validateUniqueAddresses(deviceLabel, area string, points []Point) []string {
	var problems []string
	seen := map[uint16]struct{}{}
	for _, p := range points {
		if _, ok := seen[p.Address]; ok {
			problems = append(problems, fmt.Sprintf("%s %s: duplicate address %d", deviceLabel, area, p.Address))
			continue
		}
		seen[p.Address] = struct{}{}
	}
	return problems
}

func validateNonOverlappingSpans(deviceLabel, area string, points []Point) []string {
	var problems []string
	spans := make([]addressSpan, 0, len(points))
	for _, p := range points {
		width, err := pointRegisterWidth(p)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s %s address %d: %v", deviceLabel, area, p.Address, err))
			continue
		}
		end := uint32(p.Address) + uint32(width) - 1
		if end > MaxRegisterValue {
			problems = append(problems, fmt.Sprintf("%s %s address %d: span extends past register 65535", deviceLabel, area, p.Address))
			continue
		}
		spans = append(spans, addressSpan{start: p.Address, end: uint16(end), addr: p.Address})
	}
	for i := 0; i < len(spans); i++ {
		for j := i + 1; j < len(spans); j++ {
			if spansOverlap(spans[i], spans[j]) {
				problems = append(problems, fmt.Sprintf("%s %s: overlapping address spans at %d and %d", deviceLabel, area, spans[i].addr, spans[j].addr))
			}
		}
	}
	return problems
}

func spansOverlap(a, b addressSpan) bool {
	return a.start <= b.end && b.start <= a.end
}

func validateIdentity(deviceLabel string, id *Identity) []string {
	var problems []string
	seen := map[uint8]struct{}{}
	for _, o := range id.Objects {
		if o.ID <= 0x02 {
			problems = append(problems, fmt.Sprintf("%s identity.objects: id %d (0x%02X) is reserved for the named fields (vendorName/productCode/majorMinorRevision)", deviceLabel, o.ID, o.ID))
		}
		if _, ok := seen[o.ID]; ok {
			problems = append(problems, fmt.Sprintf("%s identity.objects: duplicate id %d", deviceLabel, o.ID))
			continue
		}
		seen[o.ID] = struct{}{}
	}
	return problems
}

func validateMatch(label string, m Match) []string {
	var problems []string
	if m.AddressRange != nil && m.AddressRange.Start > m.AddressRange.End {
		problems = append(problems, fmt.Sprintf("%s: addressRange.start (%d) must be <= addressRange.end (%d)", label, m.AddressRange.Start, m.AddressRange.End))
	}
	return problems
}
