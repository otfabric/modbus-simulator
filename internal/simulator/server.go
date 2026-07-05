package simulator

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/otfabric/go-modbus"
)

// RunOptions configures the fixture simulator server.
type RunOptions struct {
	FixturePath string
	Listen      string
	TracePath   string
	MaxClients  uint
	Debug       bool
}

// ErrFixtureInvalid wraps a fixture validation failure so callers can map it to the
// invalid-input exit code.
type ErrFixtureInvalid struct{ Err error }

func (e *ErrFixtureInvalid) Error() string { return e.Err.Error() }
func (e *ErrFixtureInvalid) Unwrap() error { return e.Err }

// Run validates the fixture, builds the in-memory store, starts a Modbus TCP server, and
// blocks until ctx is cancelled. Startup progress is written to progress (may be nil).
func Run(ctx context.Context, opts RunOptions, progress io.Writer) error {
	if progress == nil {
		progress = io.Discard
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	f, err := ValidateFixtureFile(opts.FixturePath)
	if err != nil {
		return &ErrFixtureInvalid{Err: err}
	}
	store, err := BuildStore(f)
	if err != nil {
		return &ErrFixtureInvalid{Err: err}
	}

	var writer TraceWriter
	if strings.TrimSpace(opts.TracePath) != "" {
		file, ferr := os.Create(opts.TracePath)
		if ferr != nil {
			return fmt.Errorf("create trace file: %w", ferr)
		}
		jw := NewJSONLWriter(file)
		defer func() { _ = jw.Close() }()
		writer = jw
	}

	handler := NewHandler(store, writer)

	logger := modbus.NopLogger()
	if opts.Debug {
		logger = modbus.NewStdLogger(nil)
	}
	url := listenURL(opts.Listen)
	server, err := modbus.NewServer(&modbus.ServerConfig{
		URL:        url,
		MaxClients: opts.MaxClients,
		Logger:     logger,
	}, handler)
	if err != nil {
		return fmt.Errorf("create modbus server: %w", err)
	}
	if err := server.Start(); err != nil {
		return fmt.Errorf("start modbus server: %w", err)
	}

	_, _ = fmt.Fprintf(progress, "🚀 Simulating %d device(s) on %s\n", len(store.Units()), url)
	if writer != nil {
		_, _ = fmt.Fprintf(progress, "🧾 Writing trace to %s\n", opts.TracePath)
	}

	<-ctx.Done()
	_ = server.Stop()
	return ctx.Err()
}

// listenURL normalizes a listen flag (":5020", "host:port", or "tcp://host:port") into a
// go-modbus TCP server URL.
func listenURL(listen string) string {
	l := strings.TrimSpace(listen)
	if l == "" {
		l = ":5020"
	}
	if strings.Contains(l, "://") {
		return l
	}
	if strings.HasPrefix(l, ":") {
		l = "0.0.0.0" + l
	}
	return "tcp://" + l
}
