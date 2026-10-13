package simulator

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"sync"
)

// TraceSchemaVersion is the trace JSONL schema identifier.
const TraceSchemaVersion = "modbus-simulator.trace.v1"

// TraceRecord is one served-request transaction line in the JSONL trace.
type TraceRecord struct {
	SchemaVersion string         `json:"schemaVersion"`
	TS            string         `json:"ts"`
	Seq           uint64         `json:"seq"`
	Conn          *TraceConn     `json:"conn,omitempty"`
	UnitID        uint8          `json:"unitId"`
	Request       TraceRequest   `json:"request"`
	Response      TraceResponse  `json:"response"`
	StateChanges  []StateChange  `json:"stateChanges"`
	Decoded       []DecodedField `json:"decoded,omitempty"`
	MatchedRule   string         `json:"matchedRule,omitempty"`
	Raw           *TraceRaw      `json:"raw,omitempty"`
	DurationMs    float64        `json:"durationMs"`
}

// DecodedField is an optional typed decode overlay for codec-backed fixture fields.
type DecodedField struct {
	Area    string `json:"area"`
	Address uint16 `json:"address"`
	Codec   string `json:"codec"`
	Name    string `json:"name,omitempty"`
	Value   any    `json:"value,omitempty"`
	Error   string `json:"error,omitempty"`
}

// TraceRaw holds the PDUs of a transaction as hex: the request as the simulator decoded
// it and the response it returned, each encoded the way the specification prescribes.
// Response is empty when none was sent and for FC43, whose response go-modbus frames.
type TraceRaw struct {
	Request  string `json:"request"`
	Response string `json:"response,omitempty"`
}

// TraceConn identifies the client connection.
type TraceConn struct {
	Transport string `json:"transport"`
	Remote    string `json:"remote"`
}

// TraceRequest is the normalized request. Value/Values hold bool for coils and int for
// register areas. AndMask/OrMask are set for FC22. For FC23 Address and Quantity describe
// the read, WriteAddress and Values the write. FC43 fields are set only for device
// identification.
type TraceRequest struct {
	FC               uint8   `json:"fc"`
	Name             string  `json:"name"`
	Area             string  `json:"area"`
	Address          *uint16 `json:"address,omitempty"`
	Quantity         uint16  `json:"quantity,omitempty"`
	Value            any     `json:"value,omitempty"`
	Values           any     `json:"values,omitempty"`
	AndMask          *int    `json:"andMask,omitempty"`
	OrMask           *int    `json:"orMask,omitempty"`
	WriteAddress     *uint16 `json:"writeAddress,omitempty"`
	MEIType          *int    `json:"meiType,omitempty"`
	ReadDeviceIDCode *int    `json:"readDeviceIdCode,omitempty"`
	ObjectID         *int    `json:"objectId,omitempty"`
}

// TraceResponse is the normalized response.
type TraceResponse struct {
	OK            bool           `json:"ok"`
	NoResponse    bool           `json:"noResponse,omitempty"`
	Values        any            `json:"values,omitempty"`
	ExceptionCode string         `json:"exceptionCode,omitempty"`
	Fault         string         `json:"fault,omitempty"`
	Identity      *TraceIdentity `json:"identity,omitempty"`
}

// TraceIdentity mirrors the FC43 response identity object.
type TraceIdentity struct {
	VendorName         string           `json:"vendorName"`
	ProductCode        string           `json:"productCode"`
	MajorMinorRevision string           `json:"majorMinorRevision"`
	Objects            []IdentityObject `json:"objects,omitempty"`
}

// TraceWriter consumes trace records emitted by the handler.
type TraceWriter interface {
	Write(rec *TraceRecord) error
	Close() error
}

// JSONLWriter writes one compact JSON object per line, safe for concurrent use. Every
// record is flushed as it is written, so the trace can be read while the simulator runs
// and survives an abrupt stop. The first write error is sticky and reported by Close.
type JSONLWriter struct {
	mu  sync.Mutex
	bw  *bufio.Writer
	c   io.Closer
	err error
}

// NewJSONLWriter wraps w; if w is also an io.Closer it is closed by Close.
func NewJSONLWriter(w io.Writer) *JSONLWriter {
	jw := &JSONLWriter{bw: bufio.NewWriter(w)}
	if c, ok := w.(io.Closer); ok {
		jw.c = c
	}
	return jw
}

// Write appends one record as a single JSON line.
func (jw *JSONLWriter) Write(rec *TraceRecord) error {
	jw.mu.Lock()
	defer jw.mu.Unlock()
	if jw.err != nil {
		return jw.err
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(rec); err != nil { // Encode appends a trailing newline
		jw.err = err
		return err
	}
	if _, err := jw.bw.Write(buf.Bytes()); err != nil {
		jw.err = err
		return err
	}
	if err := jw.bw.Flush(); err != nil {
		jw.err = err
		return err
	}
	return nil
}

// Close flushes buffered data and closes the underlying writer if closable.
func (jw *JSONLWriter) Close() error {
	jw.mu.Lock()
	defer jw.mu.Unlock()
	if ferr := jw.bw.Flush(); ferr != nil && jw.err == nil {
		jw.err = ferr
	}
	if jw.c != nil {
		if cerr := jw.c.Close(); cerr != nil && jw.err == nil {
			jw.err = cerr
		}
	}
	return jw.err
}

var _ TraceWriter = (*JSONLWriter)(nil)

// intPtr is a small helper for optional int JSON fields.
func intPtr(v int) *int { return &v }
