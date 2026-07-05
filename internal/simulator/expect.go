package simulator

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
)

// ExpectSchemaVersion is the expectation-file schema identifier.
const ExpectSchemaVersion = "modbus-simulator.expect.v1"

// Assertion kinds.
const (
	KindContains    = "contains"
	KindNotContains = "notContains"
	KindCount       = "count"
	KindOrdered     = "ordered"
	KindExact       = "exact"
)

// Expectation is a modbus-simulator.expect.v1 document.
type Expectation struct {
	SchemaVersion string      `json:"schemaVersion"`
	Assertions    []Assertion `json:"assertions"`
}

// Assertion is one assertion of a supported kind.
type Assertion struct {
	Kind     string    `json:"kind"`
	Match    *Matcher  `json:"match,omitempty"`
	Sequence []Matcher `json:"sequence,omitempty"`
	Equals   *int      `json:"equals,omitempty"`
	AtLeast  *int      `json:"atLeast,omitempty"`
	AtMost   *int      `json:"atMost,omitempty"`
}

// Matcher is a subset matcher against a trace record.
type Matcher struct {
	Request     map[string]any `json:"request,omitempty"`
	Response    map[string]any `json:"response,omitempty"`
	StateChange map[string]any `json:"stateChange,omitempty"`
}

// AssertReport is the structured result of evaluating all assertions.
type AssertReport struct {
	Passed  bool              `json:"passed"`
	Total   int               `json:"total"`
	Failed  int               `json:"failed"`
	Records int               `json:"records"`
	Results []AssertionResult `json:"results"`
}

// AssertionResult is the outcome of a single assertion.
type AssertionResult struct {
	Index  int    `json:"index"`
	Kind   string `json:"kind"`
	Passed bool   `json:"passed"`
	Detail string `json:"detail,omitempty"`
}

// AssertTraceFiles loads and schema-validates a JSONL trace and an expectation file,
// then evaluates the assertions.
func AssertTraceFiles(tracePath, expectPath string) (AssertReport, error) {
	records, err := LoadTraceRecords(tracePath)
	if err != nil {
		return AssertReport{}, err
	}
	exp, err := LoadExpectation(expectPath)
	if err != nil {
		return AssertReport{}, err
	}
	return EvaluateAssertions(records, exp), nil
}

// LoadTraceRecords reads a JSONL trace file, validates each line against the trace
// schema, and returns records as generic JSON maps for subset matching.
func LoadTraceRecords(path string) ([]map[string]any, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open trace file: %w", err)
	}
	defer func() { _ = f.Close() }()

	var records []map[string]any
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	line := 0
	for sc.Scan() {
		line++
		raw := bytes.TrimSpace(sc.Bytes())
		if len(raw) == 0 {
			continue
		}
		if err := validateBytesAgainstSchema(TraceSchemaVersion, raw); err != nil {
			return nil, fmt.Errorf("trace line %d: %w", line, err)
		}
		var rec map[string]any
		if err := json.Unmarshal(raw, &rec); err != nil {
			return nil, fmt.Errorf("trace line %d: %w", line, err)
		}
		records = append(records, rec)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read trace file: %w", err)
	}
	return records, nil
}

// LoadExpectation reads and validates an expectation file.
func LoadExpectation(path string) (*Expectation, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read expectation file: %w", err)
	}
	if err := validateBytesAgainstSchema(ExpectSchemaVersion, data); err != nil {
		return nil, err
	}
	var exp Expectation
	if err := json.Unmarshal(data, &exp); err != nil {
		return nil, fmt.Errorf("decode expectation: %w", err)
	}
	if err := validateExpectationSemantics(&exp); err != nil {
		return nil, err
	}
	return &exp, nil
}

func validateExpectationSemantics(exp *Expectation) error {
	var problems []string
	for i, a := range exp.Assertions {
		switch a.Kind {
		case KindContains, KindNotContains:
			if a.Match == nil {
				problems = append(problems, fmt.Sprintf("assertions[%d] (%s): requires \"match\"", i, a.Kind))
			}
		case KindCount:
			if a.Match == nil {
				problems = append(problems, fmt.Sprintf("assertions[%d] (count): requires \"match\"", i))
			}
			bounds := 0
			for _, b := range []*int{a.Equals, a.AtLeast, a.AtMost} {
				if b != nil {
					bounds++
				}
			}
			if bounds != 1 {
				problems = append(problems, fmt.Sprintf("assertions[%d] (count): requires exactly one of equals/atLeast/atMost", i))
			}
		case KindOrdered, KindExact:
			if len(a.Sequence) == 0 {
				problems = append(problems, fmt.Sprintf("assertions[%d] (%s): requires a non-empty \"sequence\"", i, a.Kind))
			}
		}
	}
	if len(problems) > 0 {
		return &ValidationError{Problems: problems}
	}
	return nil
}

// EvaluateAssertions runs every assertion against the records and builds a report.
func EvaluateAssertions(records []map[string]any, exp *Expectation) AssertReport {
	report := AssertReport{Passed: true, Total: len(exp.Assertions), Records: len(records)}
	for i, a := range exp.Assertions {
		res := evaluateAssertion(records, a, i)
		if !res.Passed {
			report.Passed = false
			report.Failed++
		}
		report.Results = append(report.Results, res)
	}
	return report
}

func evaluateAssertion(records []map[string]any, a Assertion, index int) AssertionResult {
	res := AssertionResult{Index: index, Kind: a.Kind, Passed: true}
	switch a.Kind {
	case KindContains:
		if countMatches(records, *a.Match) == 0 {
			res.Passed, res.Detail = false, "no trace record matched"
		}
	case KindNotContains:
		if n := countMatches(records, *a.Match); n > 0 {
			res.Passed, res.Detail = false, fmt.Sprintf("%d record(s) matched but none were expected", n)
		}
	case KindCount:
		n := countMatches(records, *a.Match)
		switch {
		case a.Equals != nil && n != *a.Equals:
			res.Passed, res.Detail = false, fmt.Sprintf("expected exactly %d, got %d", *a.Equals, n)
		case a.AtLeast != nil && n < *a.AtLeast:
			res.Passed, res.Detail = false, fmt.Sprintf("expected at least %d, got %d", *a.AtLeast, n)
		case a.AtMost != nil && n > *a.AtMost:
			res.Passed, res.Detail = false, fmt.Sprintf("expected at most %d, got %d", *a.AtMost, n)
		}
	case KindOrdered:
		if idx, ok := matchOrdered(records, a.Sequence); !ok {
			res.Passed, res.Detail = false, fmt.Sprintf("sequence[%d] not found in order", idx)
		}
	case KindExact:
		if ok, detail := matchExact(records, a.Sequence); !ok {
			res.Passed, res.Detail = false, detail
		}
	default:
		res.Passed, res.Detail = false, "unknown assertion kind"
	}
	return res
}

func countMatches(records []map[string]any, m Matcher) int {
	n := 0
	for _, rec := range records {
		if matchRecord(rec, m) {
			n++
		}
	}
	return n
}

// matchOrdered returns (firstUnmatchedIndex, true) if all matchers occur in order.
func matchOrdered(records []map[string]any, seq []Matcher) (int, bool) {
	mi := 0
	for _, rec := range records {
		if mi >= len(seq) {
			break
		}
		if matchRecord(rec, seq[mi]) {
			mi++
		}
	}
	if mi == len(seq) {
		return 0, true
	}
	return mi, false
}

func matchExact(records []map[string]any, seq []Matcher) (bool, string) {
	if len(records) != len(seq) {
		return false, fmt.Sprintf("expected exactly %d record(s), trace has %d", len(seq), len(records))
	}
	for i := range seq {
		if !matchRecord(records[i], seq[i]) {
			return false, fmt.Sprintf("record %d does not match sequence[%d]", i, i)
		}
	}
	return true, ""
}

func matchRecord(rec map[string]any, m Matcher) bool {
	if m.Request != nil && !subsetMatch(rec["request"], m.Request) {
		return false
	}
	if m.Response != nil && !subsetMatch(rec["response"], m.Response) {
		return false
	}
	if m.StateChange != nil {
		scs, _ := rec["stateChanges"].([]any)
		found := false
		for _, sc := range scs {
			if subsetMatch(sc, m.StateChange) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// subsetMatch reports whether every field in expected is present and equal in actual.
// ts/durationMs/raw are never referenced because expectations only carry
// request/response/stateChange fields.
func subsetMatch(actual any, expected map[string]any) bool {
	am, ok := actual.(map[string]any)
	if !ok {
		return false
	}
	for k, ev := range expected {
		av, ok := am[k]
		if !ok {
			return false
		}
		if !valueMatch(av, ev) {
			return false
		}
	}
	return true
}

func valueMatch(actual, expected any) bool {
	if em, ok := expected.(map[string]any); ok {
		return subsetMatch(actual, em)
	}
	return reflect.DeepEqual(normalizeJSON(actual), normalizeJSON(expected))
}

// normalizeJSON coerces json.Number to float64 so values decoded by different code paths
// compare equal.
func normalizeJSON(v any) any {
	switch n := v.(type) {
	case json.Number:
		if f, err := n.Float64(); err == nil {
			return f
		}
		return n.String()
	case []any:
		out := make([]any, len(n))
		for i := range n {
			out[i] = normalizeJSON(n[i])
		}
		return out
	default:
		return v
	}
}

// MarshalTextOutput renders the assertion report for --format text.
func (r AssertReport) MarshalTextOutput() (string, error) {
	var b strings.Builder
	for _, res := range r.Results {
		mark := "PASS"
		if !res.Passed {
			mark = "FAIL"
		}
		fmt.Fprintf(&b, "[%s] #%d %s", mark, res.Index, res.Kind)
		if res.Detail != "" {
			fmt.Fprintf(&b, " — %s", res.Detail)
		}
		b.WriteString("\n")
	}
	if r.Passed {
		fmt.Fprintf(&b, "✅ %d/%d assertions passed over %d record(s)\n", r.Total, r.Total, r.Records)
	} else {
		fmt.Fprintf(&b, "❌ %d/%d assertions failed over %d record(s)\n", r.Failed, r.Total, r.Records)
	}
	return b.String(), nil
}
