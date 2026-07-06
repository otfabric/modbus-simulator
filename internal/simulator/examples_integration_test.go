package simulator

import (
	"context"
	"path/filepath"
	"runtime"
	"sort"
	"testing"

	"github.com/otfabric/go-modbus"
	"github.com/otfabric/go-modbus/codec"
	"github.com/stretchr/testify/require"
)

const exampleUnitID uint8 = 1

type exampleCase struct {
	fixture  string
	exercise func(t *testing.T, ctx context.Context, c *modbus.Client)
}

var exampleCases = []exampleCase{
	{
		fixture: "fc01-read-coils.json",
		exercise: func(t *testing.T, ctx context.Context, c *modbus.Client) {
			got, err := c.ReadCoils(ctx, exampleUnitID, 0, 3)
			require.NoError(t, err)
			require.Equal(t, []bool{true, false, true}, got)
		},
	},
	{
		fixture: "fc02-read-discrete-inputs.json",
		exercise: func(t *testing.T, ctx context.Context, c *modbus.Client) {
			got, err := c.ReadDiscreteInputs(ctx, exampleUnitID, 0, 3)
			require.NoError(t, err)
			require.Equal(t, []bool{false, true, false}, got)
		},
	},
	{
		fixture: "fc03-read-holding-registers.json",
		exercise: func(t *testing.T, ctx context.Context, c *modbus.Client) {
			got, err := c.ReadHoldingRegisters(ctx, exampleUnitID, 100, 3)
			require.NoError(t, err)
			require.Equal(t, []uint16{2301, 501, 42}, got)
		},
	},
	{
		fixture: "fc04-read-input-registers.json",
		exercise: func(t *testing.T, ctx context.Context, c *modbus.Client) {
			got, err := c.ReadInputRegisters(ctx, exampleUnitID, 0, 2)
			require.NoError(t, err)
			require.Equal(t, []uint16{1234, 5678}, got)
		},
	},
	{
		fixture: "fc05-write-single-coil.json",
		exercise: func(t *testing.T, ctx context.Context, c *modbus.Client) {
			require.NoError(t, c.WriteCoil(ctx, exampleUnitID, 10, true))
			got, err := c.ReadCoil(ctx, exampleUnitID, 10)
			require.NoError(t, err)
			require.True(t, got)
		},
	},
	{
		fixture: "fc06-write-single-register.json",
		exercise: func(t *testing.T, ctx context.Context, c *modbus.Client) {
			require.NoError(t, c.WriteRegister(ctx, exampleUnitID, 1, 777))
			got, err := c.ReadHoldingRegister(ctx, exampleUnitID, 1)
			require.NoError(t, err)
			require.Equal(t, uint16(777), got)
		},
	},
	{
		fixture: "fc15-write-multiple-coils.json",
		exercise: func(t *testing.T, ctx context.Context, c *modbus.Client) {
			want := []bool{true, false, true}
			require.NoError(t, c.WriteCoils(ctx, exampleUnitID, 0, want))
			got, err := c.ReadCoils(ctx, exampleUnitID, 0, 3)
			require.NoError(t, err)
			require.Equal(t, want, got)
		},
	},
	{
		fixture: "fc16-write-multiple-registers.json",
		exercise: func(t *testing.T, ctx context.Context, c *modbus.Client) {
			want := []uint16{111, 222, 333}
			require.NoError(t, c.WriteRegisters(ctx, exampleUnitID, 10, want))
			got, err := c.ReadHoldingRegisters(ctx, exampleUnitID, 10, 3)
			require.NoError(t, err)
			require.Equal(t, want, got)
		},
	},
	{
		fixture: "fc43-device-identification.json",
		exercise: func(t *testing.T, ctx context.Context, c *modbus.Client) {
			id, err := c.ReadDeviceIdentification(ctx, exampleUnitID, modbus.DeviceIDBasic, 0)
			require.NoError(t, err)
			require.NotNil(t, id)
			require.Equal(t, "OTFabric", deviceIDObjectValue(id, 0x00))
			require.Equal(t, "SIM-FC43", deviceIDObjectValue(id, 0x01))
		},
	},
	{
		fixture: "all-function-codes.json",
		exercise: func(t *testing.T, ctx context.Context, c *modbus.Client) {
			coils, err := c.ReadCoils(ctx, exampleUnitID, 0, 2)
			require.NoError(t, err)
			require.Equal(t, []bool{true, false}, coils)

			discrete, err := c.ReadDiscreteInputs(ctx, exampleUnitID, 0, 2)
			require.NoError(t, err)
			require.Equal(t, []bool{false, true}, discrete)

			holding, err := c.ReadHoldingRegisters(ctx, exampleUnitID, 0, 2)
			require.NoError(t, err)
			require.Equal(t, []uint16{2301, 501}, holding)

			inputs, err := c.ReadInputRegisters(ctx, exampleUnitID, 0, 2)
			require.NoError(t, err)
			require.Equal(t, []uint16{1234, 5678}, inputs)

			require.NoError(t, c.WriteCoil(ctx, exampleUnitID, 10, true))
			gotCoil, err := c.ReadCoil(ctx, exampleUnitID, 10)
			require.NoError(t, err)
			require.True(t, gotCoil)

			require.NoError(t, c.WriteRegister(ctx, exampleUnitID, 1, 777))
			gotReg, err := c.ReadHoldingRegister(ctx, exampleUnitID, 1)
			require.NoError(t, err)
			require.Equal(t, uint16(777), gotReg)

			require.NoError(t, c.WriteCoils(ctx, exampleUnitID, 0, []bool{false, true}))
			gotCoils, err := c.ReadCoils(ctx, exampleUnitID, 0, 2)
			require.NoError(t, err)
			require.Equal(t, []bool{false, true}, gotCoils)

			wantRegs := []uint16{111, 222, 333}
			require.NoError(t, c.WriteRegisters(ctx, exampleUnitID, 10, wantRegs))
			gotRegs, err := c.ReadHoldingRegisters(ctx, exampleUnitID, 10, 3)
			require.NoError(t, err)
			require.Equal(t, wantRegs, gotRegs)

			id, err := c.ReadDeviceIdentification(ctx, exampleUnitID, modbus.DeviceIDBasic, 0)
			require.NoError(t, err)
			require.Equal(t, "OTFabric", deviceIDObjectValue(id, 0x00))
			require.Equal(t, "SIM-ALL", deviceIDObjectValue(id, 0x01))

			_, err = c.ReadHoldingRegisters(ctx, exampleUnitID, 999, 1)
			require.Error(t, err)
		},
	},
}

var codecBundleFixtures = []string{
	"codec-integers.json",
	"codec-floats.json",
	"codec-text.json",
	"codec-bcd.json",
	"codec-bytes.json",
	"codec-decimal-limb.json",
	"codec-datetime.json",
	"codec-network.json",
	"codec-hardware.json",
}

func init() {
	for _, fixture := range codecBundleFixtures {
		fixture := fixture
		exampleCases = append(exampleCases, exampleCase{
			fixture: fixture,
			exercise: func(t *testing.T, ctx context.Context, c *modbus.Client) {
				exerciseCodecBundle(t, ctx, c, exampleFixturePath(fixture))
			},
		})
	}
}

func exerciseCodecBundle(t *testing.T, ctx context.Context, c *modbus.Client, path string) {
	t.Helper()
	fixt, err := ValidateFixtureFile(path)
	require.NoError(t, err)
	require.Len(t, fixt.Devices, 1)
	d := fixt.Devices[0]
	for _, p := range d.State.HoldingRegisters {
		if p.Codec == "" {
			continue
		}
		exerciseCodecPoint(t, ctx, c, AreaHoldingRegisters, p)
	}
	for _, p := range d.State.InputRegisters {
		if p.Codec == "" {
			continue
		}
		exerciseCodecPoint(t, ctx, c, AreaInputRegisters, p)
	}
}

func exerciseCodecPoint(t *testing.T, ctx context.Context, c *modbus.Client, area string, p Point) {
	t.Helper()
	want, err := encodePointRegisters(p)
	require.NoError(t, err)
	var got []uint16
	var errRead error
	switch area {
	case AreaHoldingRegisters:
		got, errRead = c.ReadHoldingRegisters(ctx, exampleUnitID, p.Address, uint16(len(want)))
	case AreaInputRegisters:
		got, errRead = c.ReadInputRegisters(ctx, exampleUnitID, p.Address, uint16(len(want)))
	default:
		t.Fatalf("unknown area %q", area)
	}
	require.NoError(t, errRead)
	require.Equal(t, want, got, "codec %s at address %d", p.Codec, p.Address)
}

func exampleFixturePath(name string) string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..", "examples", name)
}

func examplesDir() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..", "examples")
}

func deviceIDObjectValue(id *modbus.DeviceIdentification, objectID modbus.DeviceIDObjectID) string {
	if id == nil {
		return ""
	}
	for _, o := range id.Objects {
		if o.ID == objectID {
			return o.Value
		}
	}
	return ""
}

func TestExamplesIntegration(t *testing.T) {
	for _, tc := range exampleCases {
		t.Run(tc.fixture, func(t *testing.T) {
			exercise := tc.exercise
			withFixtureServer(t, exampleFixturePath(tc.fixture), "", func(ctx context.Context, c *modbus.Client) {
				exercise(t, ctx, c)
			})
		})
	}
}

func TestExamples_AllFixturesHaveIntegrationCase(t *testing.T) {
	matches, err := filepath.Glob(filepath.Join(examplesDir(), "*.json"))
	require.NoError(t, err)
	require.NotEmpty(t, matches)

	covered := make(map[string]struct{}, len(exampleCases))
	for _, tc := range exampleCases {
		covered[tc.fixture] = struct{}{}
	}

	for _, path := range matches {
		name := filepath.Base(path)
		_, ok := covered[name]
		require.Truef(t, ok, "examples/%s has no integration test case; add it to exampleCases", name)
	}
}

func TestExamples_AllCodecsHaveExample(t *testing.T) {
	want := make(map[string]struct{})
	for _, d := range codec.AvailableCodecDescriptors() {
		want[d.ID] = struct{}{}
	}

	got := make(map[string]struct{})
	matches, err := filepath.Glob(filepath.Join(examplesDir(), "codec-*.json"))
	require.NoError(t, err)
	require.NotEmpty(t, matches)

	for _, path := range matches {
		fixt, err := ValidateFixtureFile(path)
		require.NoError(t, err)
		for _, d := range fixt.Devices {
			collectCodecIDs(d.State.HoldingRegisters, got)
			collectCodecIDs(d.State.InputRegisters, got)
		}
	}

	var missing []string
	for id := range want {
		if _, ok := got[id]; !ok {
			missing = append(missing, id)
		}
	}
	sort.Strings(missing)
	require.Empty(t, missing, "missing codec examples: %v", missing)
}

func collectCodecIDs(points []Point, out map[string]struct{}) {
	for _, p := range points {
		if p.Codec != "" {
			out[p.Codec] = struct{}{}
		}
	}
}
