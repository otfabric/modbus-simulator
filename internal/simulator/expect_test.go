package simulator

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/otfabric/go-modbus"
	"github.com/otfabric/modbus-simulator/schemas"
	"github.com/stretchr/testify/require"
)

func genTrace(t *testing.T, fixtureJSON string, ops func(ctx context.Context, h *Handler)) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "trace.jsonl")
	file, err := os.Create(p)
	require.NoError(t, err)
	jw := NewJSONLWriter(file)
	f, err := ValidateFixtureBytes([]byte(fixtureJSON))
	require.NoError(t, err)
	store, err := BuildStore(f)
	require.NoError(t, err)
	h := NewHandler(store, jw, HandlerOptions{})
	ops(context.Background(), h)
	require.NoError(t, jw.Close())
	return p
}

func writeTemp(t *testing.T, name, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.WriteFile(p, []byte(content), 0o644))
	return p
}

const assertFixture = `{
  "schemaVersion":"modbus-simulator.fixture.v1",
  "devices":[{"unitId":1,"state":{"holdingRegisters":[{"address":1,"value":501,"writable":true}]}}]
}`

// writeThenRead produces exactly two records: a write (reg1=777) and a read of reg1.
func writeThenRead(ctx context.Context, h *Handler) {
	_, _ = h.HandleHoldingRegisters(ctx, &modbus.HoldingRegistersRequest{UnitID: 1, FunctionCode: 6, Addr: 1, IsWrite: true, Args: []uint16{777}})
	_, _ = h.HandleHoldingRegisters(ctx, &modbus.HoldingRegistersRequest{UnitID: 1, FunctionCode: 3, Addr: 1, Quantity: 1})
}

func TestAssert_AllKindsPass(t *testing.T) {
	tracePath := genTrace(t, assertFixture, writeThenRead)
	expect := `{
      "schemaVersion":"modbus-simulator.expect.v1",
      "assertions":[
        {"kind":"contains","match":{"request":{"fc":6,"area":"holdingRegisters","address":1}}},
        {"kind":"contains","match":{"stateChange":{"area":"holdingRegisters","address":1,"after":777}}},
        {"kind":"notContains","match":{"response":{"ok":false}}},
        {"kind":"count","match":{"request":{"fc":3}},"equals":1},
        {"kind":"count","match":{"request":{"area":"holdingRegisters"}},"atLeast":2},
        {"kind":"ordered","sequence":[{"request":{"fc":6}},{"request":{"fc":3}}]},
        {"kind":"exact","sequence":[
          {"request":{"name":"write_single_register"},"response":{"ok":true}},
          {"request":{"name":"read_holding_registers"},"response":{"ok":true,"values":[777]}}
        ]}
      ]
    }`
	expectPath := writeTemp(t, "expect.json", expect)

	report, err := AssertTraceFiles(tracePath, expectPath)
	require.NoError(t, err)
	require.Truef(t, report.Passed, "report: %+v", report.Results)
	require.Equal(t, 0, report.Failed)
	require.Equal(t, 2, report.Records)
}

func TestAssert_FailuresReported(t *testing.T) {
	tracePath := genTrace(t, assertFixture, writeThenRead)
	expect := `{
      "schemaVersion":"modbus-simulator.expect.v1",
      "assertions":[
        {"kind":"contains","match":{"request":{"fc":3,"address":9999}}},
        {"kind":"count","match":{"request":{"fc":3}},"equals":5},
        {"kind":"exact","sequence":[{"request":{"fc":6}}]},
        {"kind":"ordered","sequence":[{"request":{"fc":3}},{"request":{"fc":6}}]}
      ]
    }`
	expectPath := writeTemp(t, "expect.json", expect)

	report, err := AssertTraceFiles(tracePath, expectPath)
	require.NoError(t, err)
	require.False(t, report.Passed)
	require.Equal(t, 4, report.Failed)
	for _, r := range report.Results {
		require.False(t, r.Passed)
		require.NotEmpty(t, r.Detail)
	}
}

func TestAssert_ValueSubsetMismatchFails(t *testing.T) {
	tracePath := genTrace(t, assertFixture, writeThenRead)
	// read returns 777, so expecting 501 must fail.
	expect := `{"schemaVersion":"modbus-simulator.expect.v1","assertions":[
      {"kind":"contains","match":{"request":{"fc":3},"response":{"values":[501]}}}]}`
	report, err := AssertTraceFiles(tracePath, writeTemp(t, "e.json", expect))
	require.NoError(t, err)
	require.False(t, report.Passed)
}

func TestLoadExpectation_SemanticErrors(t *testing.T) {
	cases := map[string]string{
		"count without bounds": `{"schemaVersion":"modbus-simulator.expect.v1","assertions":[{"kind":"count","match":{"request":{"fc":3}}}]}`,
		"count two bounds":     `{"schemaVersion":"modbus-simulator.expect.v1","assertions":[{"kind":"count","match":{"request":{"fc":3}},"equals":1,"atLeast":1}]}`,
		"contains no match":    `{"schemaVersion":"modbus-simulator.expect.v1","assertions":[{"kind":"contains"}]}`,
		"ordered no sequence":  `{"schemaVersion":"modbus-simulator.expect.v1","assertions":[{"kind":"ordered"}]}`,
	}
	for name, doc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := LoadExpectation(writeTemp(t, "e.json", doc))
			require.Error(t, err)
		})
	}
}

func TestLoadExpectation_SchemaRejectsUnknownKind(t *testing.T) {
	_, err := LoadExpectation(writeTemp(t, "e.json",
		`{"schemaVersion":"modbus-simulator.expect.v1","assertions":[{"kind":"bogus","match":{}}]}`))
	require.Error(t, err)
}

func TestEvaluateAssertions_MissingMatchFails(t *testing.T) {
	report := EvaluateAssertions(nil, &Expectation{Assertions: []Assertion{{Kind: KindContains}, {Kind: KindCount}}})
	require.False(t, report.Passed)
	require.Equal(t, 2, report.Failed)
}

// A matcher may only name fields that trace records have: a key that no record has would
// never match, so that notContains and count with equals 0 or atMost would pass whatever
// the trace holds.
func TestLoadExpectation_UnknownMatcherKey(t *testing.T) {
	doc := func(assertion string) string {
		return `{"schemaVersion":"modbus-simulator.expect.v1","assertions":[` + assertion + `]}`
	}
	for name, assertion := range map[string]string{
		"request":        `{"kind":"notContains","match":{"request":{"fc":6,"register":1}}}`,
		"response":       `{"kind":"contains","match":{"response":{"okay":true}}}`,
		"stateChange":    `{"kind":"count","match":{"stateChange":{"addr":1}},"equals":0}`,
		"in a sequence":  `{"kind":"ordered","sequence":[{"request":{"fc":6}},{"request":{"function":3}}]}`,
		"a record field": `{"kind":"notContains","match":{"request":{"seq":1}}}`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := parseExpectation([]byte(doc(assertion)))
			require.ErrorContains(t, err, "schema validation failed")
		})
	}

	// Every field of a trace request, response and state change may be named.
	_, err := parseExpectation([]byte(doc(`{"kind":"contains","match":{
	  "request":{"fc":3,"name":"n","area":"coils","address":1,"quantity":1,"value":1,"values":[1],"andMask":1,"orMask":1,
	    "writeAddress":1,"meiType":14,"readDeviceIdCode":1,"objectId":0},
	  "response":{"ok":true,"noResponse":false,"values":[1],"exceptionCode":"illegal_function","fault":"delay","identity":{"vendorName":"v"}},
	  "stateChange":{"unitId":1,"area":"coils","address":1,"before":false,"after":true}}}`)))
	require.NoError(t, err)
}

// The fields a matcher may name are the fields of the trace schema.
func TestExpectSchema_MatcherKeysAreTraceFields(t *testing.T) {
	load := func(name string) map[string]any {
		data, err := schemas.FS.ReadFile(name)
		require.NoError(t, err)
		var doc map[string]any
		require.NoError(t, json.Unmarshal(data, &doc))
		return doc
	}
	defs := func(doc map[string]any) map[string]any { return doc["$defs"].(map[string]any) }
	traceDefs := defs(load(schemas.TraceV1))
	matcher := defs(load(schemas.ExpectV1))["matcher"].(map[string]any)["properties"].(map[string]any)
	for matcherKey, traceDef := range map[string]string{"request": "request", "response": "response", "stateChange": "stateChange"} {
		var want []string
		for k := range traceDefs[traceDef].(map[string]any)["properties"].(map[string]any) {
			want = append(want, k)
		}
		var got []string
		for _, k := range matcher[matcherKey].(map[string]any)["propertyNames"].(map[string]any)["enum"].([]any) {
			got = append(got, k.(string))
		}
		require.ElementsMatch(t, want, got, matcherKey)
	}
}
