package simulator

import (
	"encoding/json"
	"math"
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

func TestValidateFixture_Uint64FullRange(t *testing.T) {
	fx := `{"schemaVersion":"modbus-simulator.fixture.v1","devices":[{"unitId":1,"state":{"holdingRegisters":[
	  {"address":0,"codec":"uint64/layout:12345678","value":18446744073709551615}]}}]}`
	f, err := ValidateFixtureBytes([]byte(fx))
	require.NoError(t, err)
	store, err := BuildStore(f)
	require.NoError(t, err)
	regs, err := store.ReadHolding(1, 0, 4)
	require.NoError(t, err)
	require.Equal(t, []uint16{0xFFFF, 0xFFFF, 0xFFFF, 0xFFFF}, regs)
}

func TestCoerceJSONValue_Uint64Rejects(t *testing.T) {
	for _, in := range []string{"-1", "18446744073709551616", "1.5"} {
		_, err := coerceJSONValue(json.Number(in), codec.CodecValueUint64)
		require.Error(t, err, in)
	}
}

// A float32 value must lie in the float32 range. A value that rounds to the largest
// float32 is kept; one that would become an infinity is refused.
func TestCoerceJSONValue_Float32Range(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want float32
		ok   bool
	}{
		{"0", 0, true},
		{"1", 1, true},
		{"-1", -1, true},
		{"1.401298464324817e-45", math.SmallestNonzeroFloat32, true},
		{"3.4028234e38", math.MaxFloat32, true},
		{"3.4028235e38", math.MaxFloat32, true},
		{"-3.4028235e38", -math.MaxFloat32, true},
		{"3.5e38", 0, false},
		{"-3.5e38", 0, false},
		{"1e39", 0, false},
		{"1.7976931348623157e308", 0, false},
	} {
		v, err := coerceJSONValue(json.Number(tc.in), codec.CodecValueFloat32)
		if !tc.ok {
			require.ErrorContains(t, err, "out of range for float32", tc.in)
			continue
		}
		require.NoError(t, err, tc.in)
		require.Equal(t, tc.want, v, tc.in)
	}

	fx := `{"schemaVersion":"modbus-simulator.fixture.v1","devices":[{"unitId":1,"state":{"holdingRegisters":[
	  {"address":0,"codec":"float32/layout:4321","value":1e39}]}}]}`
	_, err := ValidateFixtureBytes([]byte(fx))
	require.ErrorContains(t, err, `codec "float32/layout:4321": value 1e+39 out of range for float32`)
}
