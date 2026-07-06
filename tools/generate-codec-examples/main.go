// Command generate-codec-examples builds bundled codec example fixtures under
// examples/. Each bundled file covers every codec in a family from codecs list.
// Run from repo root: go run ./tools/generate-codec-examples
package main

import (
	"encoding/json"
	"fmt"
	"math"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/otfabric/go-modbus/codec"
)

type bundle struct {
	filename string
	family   codec.CodecFamily
}

var bundles = []bundle{
	{filename: "codec-integers.json", family: codec.CodecFamilyInteger},
	{filename: "codec-floats.json", family: codec.CodecFamilyFloat},
	{filename: "codec-text.json", family: codec.CodecFamilyText},
	{filename: "codec-bcd.json", family: codec.CodecFamilyBCD},
	{filename: "codec-bytes.json", family: codec.CodecFamilyBytes},
	{filename: "codec-decimal-limb.json", family: codec.CodecFamilyDecimalLimb},
	{filename: "codec-datetime.json", family: codec.CodecFamilyTime},
	{filename: "codec-network.json", family: codec.CodecFamilyNetwork},
	{filename: "codec-hardware.json", family: codec.CodecFamilyHardwareAddress},
}

type fixtureDoc struct {
	SchemaVersion string `json:"schemaVersion"`
	Name          string `json:"name"`
	Description   string `json:"description"`
	Devices       []struct {
		UnitID uint8 `json:"unitId"`
		State  struct {
			HoldingRegisters []pointJSON `json:"holdingRegisters"`
		} `json:"state"`
	} `json:"devices"`
}

type pointJSON struct {
	Address uint16 `json:"address"`
	Codec   string `json:"codec"`
	Value   any    `json:"value"`
	Name    string `json:"name"`
}

func main() {
	root, err := repoRoot()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	outDir := filepath.Join(root, "examples")

	all := codec.AvailableCodecDescriptors()
	byFamily := map[codec.CodecFamily][]codec.CodecDescriptor{}
	for _, d := range all {
		byFamily[d.Family] = append(byFamily[d.Family], d)
	}

	for _, b := range bundles {
		descs := byFamily[b.family]
		sort.Slice(descs, func(i, j int) bool { return descs[i].ID < descs[j].ID })
		if len(descs) == 0 {
			fmt.Fprintf(os.Stderr, "no codecs for family %s\n", b.family)
			os.Exit(1)
		}
		doc, err := buildBundle(b, descs)
		if err != nil {
			fmt.Fprintf(os.Stderr, "build %s: %v\n", b.filename, err)
			os.Exit(1)
		}
		data, err := json.MarshalIndent(doc, "", "  ")
		if err != nil {
			fmt.Fprintf(os.Stderr, "marshal %s: %v\n", b.filename, err)
			os.Exit(1)
		}
		data = append(data, '\n')
		path := filepath.Join(outDir, b.filename)
		if err := os.WriteFile(path, data, 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "write %s: %v\n", path, err)
			os.Exit(1)
		}
		fmt.Printf("wrote %s (%d codecs, %d registers)\n", path, len(descs), totalRegs(descs))
	}
}

func buildBundle(b bundle, descs []codec.CodecDescriptor) (*fixtureDoc, error) {
	points := make([]pointJSON, 0, len(descs))
	var addr uint32
	for _, d := range descs {
		val, err := sampleValue(d)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", d.ID, err)
		}
		rc, ok, err := codec.RuntimeCodecByID(d.ID)
		if err != nil || !ok {
			return nil, fmt.Errorf("resolve %s: ok=%v err=%v", d.ID, ok, err)
		}
		if _, err := codec.EncodeRegistersAny(val, rc); err != nil {
			return nil, fmt.Errorf("encode %s: %w", d.ID, err)
		}
		width := uint32(rc.RegisterSpec().Count)
		if addr+width-1 > math.MaxUint16 {
			return nil, fmt.Errorf("%s: address space exhausted at %d", d.ID, addr)
		}
		points = append(points, pointJSON{
			Address: uint16(addr),
			Codec:   d.ID,
			Value:   jsonValue(val),
			Name:    safeName(d.ID),
		})
		addr += width
	}

	doc := &fixtureDoc{
		SchemaVersion: "modbus-simulator.fixture.v1",
		Name:          strings.TrimSuffix(b.filename, ".json"),
		Description:   fmt.Sprintf("One example per %s codec from modbus-simulator codecs list (%d entries).", b.family, len(descs)),
	}
	doc.Devices = []struct {
		UnitID uint8 `json:"unitId"`
		State  struct {
			HoldingRegisters []pointJSON `json:"holdingRegisters"`
		} `json:"state"`
	}{
		{UnitID: 1, State: struct {
			HoldingRegisters []pointJSON `json:"holdingRegisters"`
		}{HoldingRegisters: points}},
	}
	return doc, nil
}

func jsonValue(v any) any {
	switch x := v.(type) {
	case []byte:
		out := make([]int, len(x))
		for i, b := range x {
			out[i] = int(b)
		}
		return out
	case time.Time:
		return x.UTC().Format(time.RFC3339)
	case net.IP:
		return x.String()
	case net.HardwareAddr:
		return x.String()
	default:
		return v
	}
}

func safeName(id string) string {
	return strings.NewReplacer("/", "_", ":", "_", ".", "_").Replace(id)
}

func totalRegs(descs []codec.CodecDescriptor) int {
	var n int
	for _, d := range descs {
		n += int(d.RegisterSpec.Count)
	}
	return n
}

func sampleValue(d codec.CodecDescriptor) (any, error) {
	rc, ok, err := codec.RuntimeCodecByID(d.ID)
	if err != nil || !ok {
		return nil, fmt.Errorf("not found")
	}
	for _, cand := range candidates(d, rc) {
		regs, err := codec.EncodeRegistersAny(cand, rc)
		if err != nil {
			continue
		}
		dec, err := codec.DecodeRegistersAny(regs, rc)
		if err != nil {
			continue
		}
		if equivalent(cand, dec, rc.ValueKind()) {
			return cand, nil
		}
	}
	return nil, fmt.Errorf("no round-trip sample value")
}

func candidates(d codec.CodecDescriptor, rc codec.RuntimeCodec) []any {
	bytes := int(rc.ByteSpec().Count)
	switch rc.ValueKind() {
	case codec.CodecValueUint16:
		return []any{uint16(1234), uint16(0), uint16(65535)}
	case codec.CodecValueInt16:
		return []any{int16(-1234), int16(0), int16(32767)}
	case codec.CodecValueUint32:
		return []any{uint32(1234567890), uint32(0), uint32(1000)}
	case codec.CodecValueInt32:
		return []any{int32(-123456), int32(0), int32(1000)}
	case codec.CodecValueUint48, codec.CodecValueUint64:
		return []any{uint64(123456789012), uint64(1000)}
	case codec.CodecValueInt48, codec.CodecValueInt64:
		return []any{int64(-9876543210), int64(1000)}
	case codec.CodecValueFloat32:
		return []any{float32(230.5), float32(0), float32(-1.5)}
	case codec.CodecValueFloat64:
		return []any{float64(123.456789), float64(0), float64(-2.5)}
	case codec.CodecValueString:
		return stringCandidates(d.ID, rc)
	case codec.CodecValueByteSlice, codec.CodecValueUint8Slice:
		b := make([]byte, bytes)
		for i := range b {
			b[i] = byte(i + 1)
		}
		if rc.ValueKind() == codec.CodecValueUint8Slice {
			u := make([]uint8, len(b))
			copy(u, b)
			return []any{u}
		}
		return []any{b}
	case codec.CodecValueIP:
		if strings.Contains(d.ID, "ipv6") {
			return []any{net.ParseIP("2001:db8::1")}
		}
		return []any{net.ParseIP("192.168.1.10").To4()}
	case codec.CodecValueHardwareAddr:
		if strings.Contains(d.ID, "eui64") {
			m, _ := net.ParseMAC("00:1a:2b:3c:4d:5e:6f:70")
			return []any{m}
		}
		m, _ := net.ParseMAC("00:1a:2b:3c:4d:5e")
		return []any{m}
	case codec.CodecValueTime:
		t := time.Date(2024, 6, 15, 12, 30, 0, 0, time.UTC)
		return []any{t}
	default:
		return nil
	}
}

func paddedDigits(width int, core string) string {
	if len(core) >= width {
		return core[:width]
	}
	return strings.Repeat("0", width-len(core)) + core
}

func stringCandidates(id string, rc codec.RuntimeCodec) []any {
	bytes := int(rc.ByteSpec().Count)
	regs := int(rc.RegisterSpec().Count)
	full := strings.Repeat("A", bytes)

	switch {
	case strings.HasPrefix(id, "utf16"):
		chars := bytes / 2
		if chars < 1 {
			chars = 1
		}
		short := strings.Repeat("A", min(chars, 4))
		return []any{short, strings.Repeat("B", chars), full[:min(len(full), bytes)]}
	case strings.Contains(id, "signed_packed_bcd"):
		digits := max(regs*4, 1)
		return []any{
			strings.Repeat("1", digits),
			paddedDigits(digits, "1234"),
			"-1234",
		}
	case strings.Contains(id, "packed_bcd"):
		digits := max(regs*4, 1)
		return []any{
			strings.Repeat("1", digits),
			paddedDigits(digits, "1234"),
		}
	case strings.Contains(id, "bcd"):
		digits := max(regs*2, 1)
		return []any{
			strings.Repeat("1", digits),
			paddedDigits(digits, "1234"),
		}
	case strings.Contains(id, "fixed") || strings.Contains(id, "reverse"):
		return []any{full, strings.Repeat("X", bytes)}
	default:
		shortLen := min(bytes, 8)
		return []any{strings.Repeat("A", shortLen), "Hi", full}
	}
}

func equivalent(a, b any, kind codec.CodecValueKind) bool {
	switch kind {
	case codec.CodecValueFloat32:
		return a.(float32) == b.(float32)
	case codec.CodecValueFloat64:
		return a.(float64) == b.(float64)
	case codec.CodecValueIP:
		return a.(net.IP).Equal(b.(net.IP))
	case codec.CodecValueHardwareAddr:
		return a.(net.HardwareAddr).String() == b.(net.HardwareAddr).String()
	case codec.CodecValueTime:
		return a.(time.Time).Equal(b.(time.Time))
	case codec.CodecValueByteSlice, codec.CodecValueUint8Slice:
		return fmt.Sprintf("%v", a) == fmt.Sprintf("%v", b)
	default:
		return fmt.Sprintf("%v", a) == fmt.Sprintf("%v", b)
	}
}

func repoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("go.mod not found")
		}
		dir = parent
	}
}
