// Package schemas holds the canonical JSON Schema definitions for modbus-simulator
// artifacts. Files are embedded for offline validation in the CLI; the same paths are
// published in this repository and referenced by each document's optional $schema field.
package schemas

import "embed"

//go:embed fixture-v1.json trace-v1.json expect-v1.json
var FS embed.FS

const (
	// FixtureV1 is the embedded path for modbus-simulator.fixture.v1.
	FixtureV1 = "fixture-v1.json"
	// TraceV1 is the embedded path for modbus-simulator.trace.v1.
	TraceV1 = "trace-v1.json"
	// ExpectV1 is the embedded path for modbus-simulator.expect.v1.
	ExpectV1 = "expect-v1.json"
)
