package simulator

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/otfabric/go-modbus"
)

// RunOptions configures the fixture simulator server.
type RunOptions struct {
	FixturePath  string
	Listen       string
	TracePath    string
	TraceDecoded bool
	TraceRaw     bool
	MaxClients   uint // 0 = no limit
	Debug        bool

	// TLSCert, TLSKey and TLSClientCA are PEM files. Setting them makes the listener
	// Modbus/TCP Security (TLS with client certificates); all three are needed.
	TLSCert     string
	TLSKey      string
	TLSClientCA string

	// Control is the host:port of the HTTP control API; empty disables it.
	Control string
}

// TLSEnabled reports whether any TLS option is set.
func (o RunOptions) TLSEnabled() bool {
	return o.TLSCert != "" || o.TLSKey != "" || o.TLSClientCA != ""
}

// ErrFixtureInvalid wraps a fixture validation failure so callers can map it to the
// invalid-input exit code.
type ErrFixtureInvalid struct{ Err error }

func (e *ErrFixtureInvalid) Error() string { return e.Err.Error() }
func (e *ErrFixtureInvalid) Unwrap() error { return e.Err }

// Run validates the fixture, builds the in-memory store, starts a Modbus TCP server, and
// blocks until ctx is cancelled. Startup progress is written to progress (may be nil).
// A trace record that could not be written is reported as an error once the run ends.
func Run(ctx context.Context, opts RunOptions, progress io.Writer) (err error) {
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

	conf := &modbus.ServerConfig{MaxClients: opts.MaxClients}
	if conf.MaxClients == 0 {
		// go-modbus reads 0 as its default of 10 clients; here it means no limit.
		conf.MaxClients = math.MaxUint32
	}
	conf.Logger = modbus.NopLogger()
	if opts.Debug {
		conf.Logger = modbus.NewStdLogger(nil)
	}
	scheme := "tcp"
	if opts.TLSEnabled() {
		scheme = "tcp+tls"
		if conf.TLSServerCert, conf.TLSClientCAs, err = loadTLS(opts); err != nil {
			return err
		}
	}
	conf.URL = listenURL(opts.Listen, scheme)

	var writer TraceWriter
	if strings.TrimSpace(opts.TracePath) != "" {
		file, ferr := os.Create(opts.TracePath)
		if ferr != nil {
			return fmt.Errorf("create trace file: %w", ferr)
		}
		jw := NewJSONLWriter(file)
		defer func() {
			if cerr := jw.Close(); cerr != nil && (err == nil || errors.Is(err, context.Canceled)) {
				err = fmt.Errorf("write trace file: %w", cerr)
			}
		}()
		writer = jw
	}

	handler := NewHandler(store, writer, HandlerOptions{
		TraceDecoded: opts.TraceDecoded,
		TraceRaw:     opts.TraceRaw,
		Transport:    scheme,
	})
	server, err := modbus.NewServer(conf, handler)
	if err != nil {
		return fmt.Errorf("create modbus server: %w", err)
	}
	if err := server.Start(); err != nil {
		return fmt.Errorf("start modbus server: %w", err)
	}
	defer func() { _ = server.Stop() }()

	_, _ = fmt.Fprintf(progress, "🚀 Simulating %d device(s) on %s\n", len(store.Units()), conf.URL)
	if writer != nil {
		_, _ = fmt.Fprintf(progress, "🧾 Writing trace to %s\n", opts.TracePath)
	}

	if strings.TrimSpace(opts.Control) != "" {
		ln, lerr := net.Listen("tcp", opts.Control)
		if lerr != nil {
			return fmt.Errorf("start control API: %w", lerr)
		}
		ctl := &control{handler: handler, fixturePath: opts.FixturePath, progress: progress}
		srv := &http.Server{Handler: ctl.routes(), ReadHeaderTimeout: 5 * time.Second}
		go func() { _ = srv.Serve(ln) }()
		defer func() { _ = srv.Close() }()
		_, _ = fmt.Fprintf(progress, "🎛  Control API on http://%s\n", ln.Addr())
	}

	<-ctx.Done()
	return ctx.Err()
}

// loadTLS reads the server certificate and the CA that client certificates must chain to.
func loadTLS(opts RunOptions) (*tls.Certificate, *x509.CertPool, error) {
	if opts.TLSCert == "" || opts.TLSKey == "" || opts.TLSClientCA == "" {
		return nil, nil, errors.New("TLS needs a certificate, a key and a client CA")
	}
	cert, err := tls.LoadX509KeyPair(opts.TLSCert, opts.TLSKey)
	if err != nil {
		return nil, nil, fmt.Errorf("load TLS certificate: %w", err)
	}
	pem, err := os.ReadFile(opts.TLSClientCA)
	if err != nil {
		return nil, nil, fmt.Errorf("read TLS client CA: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return nil, nil, fmt.Errorf("read TLS client CA: no certificate in %s", opts.TLSClientCA)
	}
	return &cert, pool, nil
}

// listenURL normalizes a listen flag (":5020", "host:port", or "scheme://host:port") into
// a go-modbus server URL with the given scheme.
func listenURL(listen, scheme string) string {
	l := strings.TrimSpace(listen)
	if i := strings.Index(l, "://"); i >= 0 {
		l = l[i+3:]
	}
	if l == "" {
		l = ":5020"
	}
	if strings.HasPrefix(l, ":") {
		l = "0.0.0.0" + l
	}
	return scheme + "://" + l
}
