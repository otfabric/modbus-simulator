package simulator

import (
	"bytes"
	"fmt"
	"sync"

	"github.com/otfabric/modbus-simulator/schemas"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

// schemaFiles maps a schemaVersion identifier to its embedded schema resource path.
var schemaFiles = map[string]string{
	FixtureSchemaVersion: schemas.FixtureV1,
	TraceSchemaVersion:   schemas.TraceV1,
	ExpectSchemaVersion:  schemas.ExpectV1,
}

var (
	schemaOnce sync.Once
	schemaErr  error
	compiled   map[string]*jsonschema.Schema
)

func loadSchemas() {
	compiled = make(map[string]*jsonschema.Schema, len(schemaFiles))
	c := jsonschema.NewCompiler()
	for _, file := range schemaFiles {
		data, err := schemas.FS.ReadFile(file)
		if err != nil {
			schemaErr = fmt.Errorf("read embedded schema %s: %w", file, err)
			return
		}
		doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
		if err != nil {
			schemaErr = fmt.Errorf("parse embedded schema %s: %w", file, err)
			return
		}
		if err := c.AddResource(file, doc); err != nil {
			schemaErr = fmt.Errorf("register embedded schema %s: %w", file, err)
			return
		}
	}
	for version, file := range schemaFiles {
		sch, err := c.Compile(file)
		if err != nil {
			schemaErr = fmt.Errorf("compile embedded schema %s: %w", file, err)
			return
		}
		compiled[version] = sch
	}
}

// validateBytesAgainstSchema validates raw JSON against the embedded schema for the
// given schemaVersion. The jsonschema error already renders a readable, indented list
// of the specific violations and their instance locations.
func validateBytesAgainstSchema(version string, data []byte) error {
	schemaOnce.Do(loadSchemas)
	if schemaErr != nil {
		return schemaErr
	}
	sch, ok := compiled[version]
	if !ok {
		return fmt.Errorf("no embedded schema registered for %q", version)
	}
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("parse JSON: %w", err)
	}
	if err := sch.Validate(inst); err != nil {
		return fmt.Errorf("schema validation failed: %w", err)
	}
	return nil
}
