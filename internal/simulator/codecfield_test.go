package simulator

import (
	"encoding/json"
	"testing"

	"github.com/otfabric/go-modbus/codec"
	"github.com/stretchr/testify/require"
)

func TestCoerceJSONValue_Float32(t *testing.T) {
	v, err := coerceJSONValue(json.Number("230.5"), codec.CodecValueFloat32)
	require.NoError(t, err)
	require.Equal(t, float32(230.5), v)
}

func TestCoerceJSONValue_IP(t *testing.T) {
	v, err := coerceJSONValue("192.168.1.10", codec.CodecValueIP)
	require.NoError(t, err)
	require.NotNil(t, v)
}

func TestEncodePointRegisters_Raw(t *testing.T) {
	regs, err := encodePointRegisters(Point{Address: 0, Value: json.Number("2301")})
	require.NoError(t, err)
	require.Equal(t, []uint16{2301}, regs)
}

func TestEncodePointRegisters_Float32(t *testing.T) {
	regs, err := encodePointRegisters(Point{
		Address: 10,
		Codec:   "float32/layout:4321",
		Value:   json.Number("230.5"),
	})
	require.NoError(t, err)
	require.Equal(t, []uint16{17254, 32768}, regs)
}

func TestValidateCodecPoint_RoundTrip(t *testing.T) {
	problems := validateCodecPoint("dev", AreaHoldingRegisters, Point{
		Address: 0,
		Codec:   "ascii/registers:4",
		Value:   "PUMP1",
	})
	require.Empty(t, problems)
}

func TestValidateCodecPoint_UnknownCodec(t *testing.T) {
	problems := validateCodecPoint("dev", AreaHoldingRegisters, Point{
		Address: 0,
		Codec:   "missing/codec",
		Value:   1,
	})
	require.NotEmpty(t, problems)
}

func TestValidateNonOverlappingSpans_Overlap(t *testing.T) {
	problems := validateNonOverlappingSpans("dev", AreaHoldingRegisters, []Point{
		{Address: 10, Codec: "float32/layout:4321", Value: json.Number("1.0")},
		{Address: 11, Value: json.Number("2")},
	})
	require.NotEmpty(t, problems)
}
