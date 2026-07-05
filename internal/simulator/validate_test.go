package simulator

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidateFixture_ValidExample(t *testing.T) {
	f, err := ValidateFixtureFile("testdata/valid_device.json")
	require.NoError(t, err)
	require.Equal(t, FixtureSchemaVersion, f.SchemaVersion)
	require.Len(t, f.Devices, 1)
	require.Equal(t, uint8(1), f.Devices[0].UnitID)
	require.Equal(t, UnknownAddressException, f.ResolvedUnknownAddress(&f.Devices[0]))
	require.Equal(t, StateModePersistent, f.ResolvedStateMode(&f.Devices[0]))
}

// minimalFixture returns a valid single-device fixture with the given device body spliced in.
func minimalFixture(deviceBody string) string {
	return `{
      "schemaVersion": "modbus-simulator.fixture.v1",
      "devices": [` + deviceBody + `]
    }`
}

func TestValidateFixture_Rejections(t *testing.T) {
	cases := []struct {
		name       string
		json       string
		wantSubstr string
	}{
		{
			name:       "wrong schema version",
			json:       `{"schemaVersion":"modbus-simulator.fixture.v2","devices":[{"unitId":1,"state":{}}]}`,
			wantSubstr: "schema validation failed",
		},
		{
			name:       "unknown top-level field",
			json:       `{"schemaVersion":"modbus-simulator.fixture.v1","devices":[{"unitId":1,"state":{}}],"bogus":1}`,
			wantSubstr: "schema validation failed",
		},
		{
			name:       "empty devices",
			json:       `{"schemaVersion":"modbus-simulator.fixture.v1","devices":[]}`,
			wantSubstr: "schema validation failed",
		},
		{
			name:       "unit id zero",
			json:       minimalFixture(`{"unitId":0,"state":{}}`),
			wantSubstr: "schema validation failed",
		},
		{
			name:       "unit id above 247",
			json:       minimalFixture(`{"unitId":248,"state":{}}`),
			wantSubstr: "schema validation failed",
		},
		{
			name:       "writable on discrete inputs",
			json:       minimalFixture(`{"unitId":1,"state":{"discreteInputs":[{"address":0,"value":true,"writable":true}]}}`),
			wantSubstr: "schema validation failed",
		},
		{
			name:       "writable on input registers",
			json:       minimalFixture(`{"unitId":1,"state":{"inputRegisters":[{"address":0,"value":1,"writable":true}]}}`),
			wantSubstr: "schema validation failed",
		},
		{
			name:       "register value out of range",
			json:       minimalFixture(`{"unitId":1,"state":{"holdingRegisters":[{"address":0,"value":70000}]}}`),
			wantSubstr: "schema validation failed",
		},
		{
			name:       "coil value not boolean",
			json:       minimalFixture(`{"unitId":1,"state":{"coils":[{"address":0,"value":1}]}}`),
			wantSubstr: "schema validation failed",
		},
		{
			name:       "unsupported fc in match",
			json:       minimalFixture(`{"unitId":1,"state":{},"behavior":{"exceptions":[{"match":{"fc":22},"exceptionCode":"illegal_function"}]}}`),
			wantSubstr: "schema validation failed",
		},
		{
			name:       "address and addressRange together",
			json:       minimalFixture(`{"unitId":1,"state":{},"behavior":{"exceptions":[{"match":{"address":1,"addressRange":{"start":1,"end":2}},"exceptionCode":"illegal_function"}]}}`),
			wantSubstr: "schema validation failed",
		},
		{
			name:       "quantity zero",
			json:       minimalFixture(`{"unitId":1,"state":{},"behavior":{"exceptions":[{"match":{"quantity":0},"exceptionCode":"illegal_function"}]}}`),
			wantSubstr: "schema validation failed",
		},
		{
			name:       "duplicate unit id",
			json:       `{"schemaVersion":"modbus-simulator.fixture.v1","devices":[{"unitId":1,"state":{}},{"unitId":1,"state":{}}]}`,
			wantSubstr: "duplicate unitId",
		},
		{
			name:       "duplicate address in area",
			json:       minimalFixture(`{"unitId":1,"state":{"holdingRegisters":[{"address":5,"value":1},{"address":5,"value":2}]}}`),
			wantSubstr: "duplicate address 5",
		},
		{
			name:       "identity object id reserved",
			json:       minimalFixture(`{"unitId":1,"state":{},"identity":{"vendorName":"v","productCode":"p","majorMinorRevision":"1.0.0","objects":[{"id":1,"value":"x"}]}}`),
			wantSubstr: "reserved for the named fields",
		},
		{
			name:       "identity duplicate object id",
			json:       minimalFixture(`{"unitId":1,"state":{},"identity":{"vendorName":"v","productCode":"p","majorMinorRevision":"1.0.0","objects":[{"id":128,"value":"a"},{"id":128,"value":"b"}]}}`),
			wantSubstr: "duplicate id 128",
		},
		{
			name:       "addressRange start after end",
			json:       minimalFixture(`{"unitId":1,"state":{},"behavior":{"exceptions":[{"match":{"addressRange":{"start":10,"end":5}},"exceptionCode":"illegal_function"}]}}`),
			wantSubstr: "must be <=",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ValidateFixtureBytes([]byte(tc.json))
			require.Error(t, err)
			require.Truef(t, strings.Contains(err.Error(), tc.wantSubstr),
				"error %q does not contain %q", err.Error(), tc.wantSubstr)
		})
	}
}
