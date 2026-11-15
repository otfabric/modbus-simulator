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
	"sync"
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

	// test is set by the tests of this package only.
	test *runTest
}

// runTest is how the tests of this package reach into a run.
type runTest struct {
	shutdownTimeout time.Duration                             // 0: shutdownTimeout
	createTrace     func(path string) (io.WriteCloser, error) // nil: os.Create
	ready           func(*running)                            // called once Run serves
}

// shutdownTimeout bounds how long Run waits, once it stops, for the control API and the
// requests in progress to finish.
const shutdownTimeout = 10 * time.Second

func (o RunOptions) shutdownTimeout() time.Duration {
	if o.test != nil && o.test.shutdownTimeout > 0 {
		return o.test.shutdownTimeout
	}
	return shutdownTimeout
}

func (o RunOptions) createTrace(path string) (io.WriteCloser, error) {
	if o.test != nil && o.test.createTrace != nil {
		return o.test.createTrace(path)
	}
	return os.Create(path)
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
// blocks until ctx is cancelled. Startup progress, and a warning when the trace cannot be
// written, go to progress (may be nil). A trace record that could not be written is
// reported as an error once the run ends.
//
// Once ctx is cancelled, Run stops the control API and the Modbus server and waits for the
// requests in progress, at most 10 s in all. When they do not finish in that time, Run
// returns an error and leaves the trace file to them unclosed.
func Run(ctx context.Context, opts RunOptions, progress io.Writer) (err error) {
	if progress == nil {
		progress = io.Discard
	}
	progress = &lockedWriter{w: progress}
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

	// Whatever has been started is stopped by r.stop when Run returns, in reverse order.
	var r running
	defer func() { err = r.stop(err, opts.shutdownTimeout()) }()

	var writer TraceWriter
	if strings.TrimSpace(opts.TracePath) != "" {
		file, ferr := opts.createTrace(opts.TracePath)
		if ferr != nil {
			return fmt.Errorf("create trace file: %w", ferr)
		}
		r.trace = NewJSONLWriter(file)
		writer = r.trace
	}

	r.handler = NewHandler(store, writer, HandlerOptions{
		TraceDecoded: opts.TraceDecoded,
		TraceRaw:     opts.TraceRaw,
		Transport:    scheme,
		OnTraceError: func(err error) {
			_, _ = fmt.Fprintf(progress, "⚠️  Trace file not written: %v. Requests are still served but no longer traced, and run will exit with code 4\n", err)
		},
	})
	server, err := modbus.NewServer(conf, r.handler)
	if err != nil {
		return fmt.Errorf("create modbus server: %w", err)
	}
	if err := server.Start(); err != nil {
		return fmt.Errorf("start modbus server: %w", err)
	}
	r.server = server

	_, _ = fmt.Fprintf(progress, "🚀 Simulating %d device(s) on %s\n", len(store.Units()), conf.URL)
	if writer != nil {
		_, _ = fmt.Fprintf(progress, "🧾 Writing trace to %s\n", opts.TracePath)
	}

	if strings.TrimSpace(opts.Control) != "" {
		ln, lerr := net.Listen("tcp", opts.Control)
		if lerr != nil {
			return fmt.Errorf("start control API: %w", lerr)
		}
		r.ctl = &control{handler: r.handler, fixturePath: opts.FixturePath, progress: progress}
		r.control = newControlServer(r.ctl)
		r.controlDone = make(chan struct{})
		go func() {
			defer close(r.controlDone)
			_ = r.control.Serve(ln)
		}()
		_, _ = fmt.Fprintf(progress, "🎛  Control API on http://%s\n", ln.Addr())
	}

	if opts.test != nil && opts.test.ready != nil {
		opts.test.ready(&r)
	}
	<-ctx.Done()
	return ctx.Err()
}

// running is what Run has started.
type running struct {
	trace       *JSONLWriter
	handler     *Handler
	server      *modbus.Server
	ctl         *control
	control     *http.Server
	controlDone chan struct{} // closed when the control API's Serve returned
}

// stop ends the control API, then the Modbus server, waiting at most timeout in all for
// the requests in progress, and then closes the trace. It returns err, or, when err is nil
// or the cancellation of the run, the error of the shutdown or of the trace.
func (r *running) stop(err error, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	finished := true
	if r.control != nil {
		if errors.Is(r.control.Shutdown(ctx), context.DeadlineExceeded) {
			_ = r.control.Close()
			finished = false
		}
		<-r.controlDone
	}
	if r.server != nil && errors.Is(r.server.Shutdown(ctx), context.DeadlineExceeded) {
		finished = false
	}

	replace := err == nil || errors.Is(err, context.Canceled)
	if !finished {
		// The requests still in progress may hold the trace writer: it is left to them.
		if replace {
			err = fmt.Errorf("shutdown: %d request(s) still in progress after %s; the trace file was not closed", r.handler.inProgress(), timeout)
		}
		return err
	}
	if r.trace != nil {
		if cerr := r.trace.Close(); cerr != nil && replace {
			err = fmt.Errorf("write trace file: %w", cerr)
		}
	}
	return err
}

// lockedWriter serialises the progress lines that Run, the handler and the control API
// write.
type lockedWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (lw *lockedWriter) Write(p []byte) (int, error) {
	lw.mu.Lock()
	defer lw.mu.Unlock()
	return lw.w.Write(p)
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
