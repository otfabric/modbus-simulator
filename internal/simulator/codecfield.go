package simulator

import (
	"encoding/json"
	"fmt"
	"math"
	"net"
	"time"

	"github.com/otfabric/go-modbus/codec"
)

// registerField is runtime metadata for a codec-backed multi-register point.
type registerField struct {
	area         string
	startAddress uint16
	codecID      string
	name         string
	description  string
	codec        codec.RuntimeCodec
}

func (f registerField) registerCount() uint16 {
	return f.codec.RegisterSpec().Count
}

// resolveCodec looks up a go-modbus runtime codec by stable ID.
func resolveCodec(id string) (codec.RuntimeCodec, error) {
	rc, ok, err := codec.RuntimeCodecByID(id)
	if err != nil {
		return nil, fmt.Errorf("codec %q: %w", id, err)
	}
	if !ok {
		return nil, fmt.Errorf("unknown codec %q (run modbus-simulator codecs list)", id)
	}
	return rc, nil
}

// pointRegisterWidth returns how many registers a point occupies.
func pointRegisterWidth(p Point) (uint16, error) {
	if p.Codec == "" {
		return 1, nil
	}
	rc, err := resolveCodec(p.Codec)
	if err != nil {
		return 0, err
	}
	return rc.RegisterSpec().Count, nil
}

// encodePointRegisters encodes a fixture point into raw registers.
func encodePointRegisters(p Point) ([]uint16, error) {
	if p.Codec == "" {
		v, err := pointReg(p)
		if err != nil {
			return nil, err
		}
		return []uint16{v}, nil
	}
	rc, err := resolveCodec(p.Codec)
	if err != nil {
		return nil, err
	}
	goVal, err := coerceJSONValue(p.Value, rc.ValueKind())
	if err != nil {
		return nil, err
	}
	regs, err := codec.EncodeRegistersAny(goVal, rc)
	if err != nil {
		return nil, fmt.Errorf("encode value: %w", err)
	}
	return regs, nil
}

// validateCodecPoint checks codec id, value kind, encode, and round-trip decode.
func validateCodecPoint(deviceLabel, area string, p Point) []string {
	if p.Codec == "" {
		return nil
	}
	label := fmt.Sprintf("%s %s address %d codec %q", deviceLabel, area, p.Address, p.Codec)
	rc, err := resolveCodec(p.Codec)
	if err != nil {
		return []string{label + ": " + err.Error()}
	}
	goVal, err := coerceJSONValue(p.Value, rc.ValueKind())
	if err != nil {
		return []string{label + ": " + err.Error()}
	}
	regs, err := codec.EncodeRegistersAny(goVal, rc)
	if err != nil {
		return []string{label + ": encode: " + err.Error()}
	}
	decoded, err := codec.DecodeRegistersAny(regs, rc)
	if err != nil {
		return []string{label + ": round-trip decode: " + err.Error()}
	}
	if !valuesEquivalent(goVal, decoded, rc.ValueKind()) {
		return []string{label + ": round-trip decode produced a different value"}
	}
	return nil
}

func valuesEquivalent(encoded, decoded any, kind codec.CodecValueKind) bool {
	switch kind {
	case codec.CodecValueFloat32:
		a, okA := encoded.(float32)
		b, okB := decoded.(float32)
		return okA && okB && a == b
	case codec.CodecValueFloat64:
		a, okA := encoded.(float64)
		b, okB := decoded.(float64)
		return okA && okB && a == b
	case codec.CodecValueIP:
		a, okA := encoded.(net.IP)
		b, okB := decoded.(net.IP)
		return okA && okB && a.Equal(b)
	case codec.CodecValueHardwareAddr:
		a, okA := encoded.(net.HardwareAddr)
		b, okB := decoded.(net.HardwareAddr)
		return okA && okB && a.String() == b.String()
	case codec.CodecValueTime:
		a, okA := encoded.(time.Time)
		b, okB := decoded.(time.Time)
		return okA && okB && a.Equal(b)
	case codec.CodecValueByteSlice, codec.CodecValueUint8Slice:
		return fmt.Sprintf("%v", encoded) == fmt.Sprintf("%v", decoded)
	default:
		return fmt.Sprintf("%v", encoded) == fmt.Sprintf("%v", decoded)
	}
}

func coerceJSONValue(v any, kind codec.CodecValueKind) (any, error) {
	switch kind {
	case codec.CodecValueUint16:
		n, err := asInt64(v)
		if err != nil {
			return nil, err
		}
		if n < 0 || n > math.MaxUint16 {
			return nil, fmt.Errorf("value %d out of range for uint16", n)
		}
		return uint16(n), nil
	case codec.CodecValueInt16:
		n, err := asInt64(v)
		if err != nil {
			return nil, err
		}
		if n < math.MinInt16 || n > math.MaxInt16 {
			return nil, fmt.Errorf("value %d out of range for int16", n)
		}
		return int16(n), nil
	case codec.CodecValueUint32:
		n, err := asInt64(v)
		if err != nil {
			return nil, err
		}
		if n < 0 || n > math.MaxUint32 {
			return nil, fmt.Errorf("value %d out of range for uint32", n)
		}
		return uint32(n), nil
	case codec.CodecValueInt32:
		n, err := asInt64(v)
		if err != nil {
			return nil, err
		}
		if n < math.MinInt32 || n > math.MaxInt32 {
			return nil, fmt.Errorf("value %d out of range for int32", n)
		}
		return int32(n), nil
	case codec.CodecValueUint48, codec.CodecValueUint64:
		n, err := asInt64(v)
		if err != nil {
			return nil, err
		}
		if n < 0 {
			return nil, fmt.Errorf("value %d out of range for unsigned integer", n)
		}
		return uint64(n), nil
	case codec.CodecValueInt48, codec.CodecValueInt64:
		n, err := asInt64(v)
		if err != nil {
			return nil, err
		}
		return n, nil
	case codec.CodecValueFloat32:
		f, err := asFloat64(v)
		if err != nil {
			return nil, err
		}
		return float32(f), nil
	case codec.CodecValueFloat64:
		return asFloat64(v)
	case codec.CodecValueString:
		s, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("value must be a string")
		}
		return s, nil
	case codec.CodecValueByteSlice:
		return asByteSlice(v)
	case codec.CodecValueUint8Slice:
		b, err := asByteSlice(v)
		if err != nil {
			return nil, err
		}
		out := make([]uint8, len(b))
		copy(out, b)
		return out, nil
	case codec.CodecValueIP:
		s, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("value must be an IP address string")
		}
		ip := net.ParseIP(s)
		if ip == nil {
			return nil, fmt.Errorf("invalid IP address %q", s)
		}
		return ip, nil
	case codec.CodecValueHardwareAddr:
		s, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("value must be a hardware address string")
		}
		mac, err := net.ParseMAC(s)
		if err != nil {
			return nil, fmt.Errorf("invalid hardware address %q: %w", s, err)
		}
		return mac, nil
	case codec.CodecValueTime:
		s, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("value must be an RFC3339 time string")
		}
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			return nil, fmt.Errorf("invalid RFC3339 time %q: %w", s, err)
		}
		return t, nil
	default:
		return nil, fmt.Errorf("unsupported codec value kind %s", kind)
	}
}

func asInt64(v any) (int64, error) {
	switch n := v.(type) {
	case json.Number:
		return n.Int64()
	case float64:
		if math.Trunc(n) != n {
			return 0, fmt.Errorf("value must be an integer")
		}
		return int64(n), nil
	default:
		return 0, fmt.Errorf("value must be a number")
	}
}

func asFloat64(v any) (float64, error) {
	switch n := v.(type) {
	case json.Number:
		return n.Float64()
	case float64:
		return n, nil
	default:
		return 0, fmt.Errorf("value must be a number")
	}
}

func asByteSlice(v any) ([]byte, error) {
	arr, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("value must be an array of integers 0–255")
	}
	out := make([]byte, len(arr))
	for i, el := range arr {
		n, err := asInt64(el)
		if err != nil {
			return nil, fmt.Errorf("byte[%d]: %w", i, err)
		}
		if n < 0 || n > 255 {
			return nil, fmt.Errorf("byte[%d] value %d out of range 0–255", i, n)
		}
		out[i] = byte(n)
	}
	return out, nil
}

// valueForTrace converts a decoded Go value to a JSON-friendly trace value.
func valueForTrace(v any, kind codec.CodecValueKind) any {
	switch kind {
	case codec.CodecValueIP:
		if ip, ok := v.(net.IP); ok {
			return ip.String()
		}
	case codec.CodecValueHardwareAddr:
		if mac, ok := v.(net.HardwareAddr); ok {
			return mac.String()
		}
	case codec.CodecValueTime:
		if tm, ok := v.(time.Time); ok {
			return tm.UTC().Format(time.RFC3339)
		}
	case codec.CodecValueByteSlice, codec.CodecValueUint8Slice:
		if b, ok := v.([]byte); ok {
			out := make([]int, len(b))
			for i, x := range b {
				out[i] = int(x)
			}
			return out
		}
	case codec.CodecValueUnknown, codec.CodecValueUint16, codec.CodecValueInt16,
		codec.CodecValueUint32, codec.CodecValueInt32, codec.CodecValueUint48, codec.CodecValueInt48,
		codec.CodecValueUint64, codec.CodecValueInt64, codec.CodecValueFloat32, codec.CodecValueFloat64,
		codec.CodecValueString:
	}
	return v
}
