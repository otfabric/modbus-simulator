package simulator

import (
	"os"
	"strconv"
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

// $schema is advisory: the published address, the old otfabric.dev one and a relative
// path all validate, against the embedded schema selected by schemaVersion.
func TestValidateFixture_SchemaURLAdvisory(t *testing.T) {
	raw, err := os.ReadFile("testdata/valid_device.json")
	require.NoError(t, err)
	const published = "https://otfabric.io/schemas/modbus-simulator/fixture-v1.json"
	require.Contains(t, string(raw), published)
	for _, u := range []string{
		published,
		"https://otfabric.dev/schemas/modbus-simulator/fixture-v1.json",
		"../schemas/fixture-v1.json",
	} {
		f, err := ValidateFixtureBytes([]byte(strings.Replace(string(raw), published, u, 1)))
		require.NoError(t, err, u)
		require.Equal(t, u, f.Schema)
	}
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
			name:       "unsupported schema version",
			json:       `{"schemaVersion":"modbus-simulator.fixture.v2","devices":[{"unitId":1,"state":{}}]}`,
			wantSubstr: "unsupported schemaVersion",
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
			json:       minimalFixture(`{"unitId":1,"state":{},"behavior":{"exceptions":[{"match":{"fc":7},"exceptionCode":"illegal_function"}]}}`),
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
			wantSubstr: "overlapping address spans",
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

// An identity value is at most 244 bytes long, what one FC43 response object can carry.
// The length counts bytes in UTF-8, not characters.
func TestValidateFixture_IdentityValueLength(t *testing.T) {
	identity := func(vendor, object string) string {
		return minimalFixture(`{"unitId":1,"state":{},"identity":{"vendorName":` + strconv.Quote(vendor) +
			`,"productCode":"p","majorMinorRevision":"1","objects":[{"id":128,"value":` + strconv.Quote(object) + `}]}}`)
	}
	for _, tc := range []struct {
		name           string
		vendor, object string
		wantErr        string
	}{
		{name: "empty", vendor: "", object: ""},
		{name: "1 byte", vendor: "v", object: "o"},
		{name: "243 bytes", vendor: strings.Repeat("v", 243), object: strings.Repeat("o", 243)},
		{name: "244 bytes", vendor: strings.Repeat("v", 244), object: strings.Repeat("o", 244)},
		{name: "244 bytes in 2-byte characters", vendor: strings.Repeat("é", 122), object: "o"},
		{name: "vendorName 245 bytes", vendor: strings.Repeat("v", 245), object: "o", wantErr: "schema validation failed"},
		{name: "object 245 bytes", vendor: "v", object: strings.Repeat("o", 245), wantErr: "schema validation failed"},
		{name: "vendorName 246 bytes in 123 characters", vendor: strings.Repeat("é", 123), object: "o", wantErr: "identity.vendorName: value is 246 bytes, at most 244"},
		{name: "object 246 bytes in 123 characters", vendor: "v", object: strings.Repeat("é", 123), wantErr: "identity.objects[0]: value is 246 bytes, at most 244"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ValidateFixtureBytes([]byte(identity(tc.vendor, tc.object)))
			if tc.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, tc.wantErr)
		})
	}

	// productCode and majorMinorRevision are held to the same length.
	for _, field := range []string{"productCode", "majorMinorRevision"} {
		id := &Identity{VendorName: "v", ProductCode: "p", MajorMinorRevision: "1"}
		long := strings.Repeat("é", 123)
		if field == "productCode" {
			id.ProductCode = long
		} else {
			id.MajorMinorRevision = long
		}
		problems := validateIdentity("dev", id)
		require.Len(t, problems, 1, field)
		require.Contains(t, problems[0], "identity."+field+": value is 246 bytes")
	}
}
