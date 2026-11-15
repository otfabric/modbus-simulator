package simulator

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// control is the HTTP control API of a running simulator: it resets the state to the
// fixture values and reloads the fixture file, without a restart. It has no
// authentication and is meant to listen on a loopback address.
type control struct {
	handler     *Handler
	fixturePath string
	progress    io.Writer
	mu          sync.Mutex // one control request at a time
}

type controlReply struct {
	OK      bool            `json:"ok"`
	Error   string          `json:"error,omitempty"`
	Unit    *uint8          `json:"unit,omitempty"`
	Fixture *FixtureSummary `json:"fixture,omitempty"`
}

// The control API's HTTP timeouts: how long a client may take to send the request header,
// and how long an idle keep-alive connection is kept.
const (
	controlReadHeaderTimeout = 5 * time.Second
	controlIdleTimeout       = 30 * time.Second
)

func newControlServer(c *control) *http.Server {
	return &http.Server{
		Handler:           c.routes(),
		ReadHeaderTimeout: controlReadHeaderTimeout,
		IdleTimeout:       controlIdleTimeout,
	}
}

func (c *control) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", c.healthz)
	mux.HandleFunc("POST /reset", c.reset)
	mux.HandleFunc("POST /reload", c.reload)
	return notFromWebPages(mux)
}

// notFromWebPages refuses every request that carries an Origin header. Web browsers send
// one with the requests a page makes to another site, and with every POST; HTTP clients
// outside a browser, such as curl, do not. The control API is meant for test scripts, and
// a page open in a browser on the same machine must not reset or reload the simulator.
func notFromWebPages(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := r.Header["Origin"]; ok {
			failReply(w, http.StatusForbidden, "requests from web pages (with an Origin header) are not accepted")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// healthz reports whether the simulator runs and its trace file, when it writes one, is
// still written.
func (c *control) healthz(w http.ResponseWriter, _ *http.Request) {
	if err := c.handler.TraceError(); err != nil {
		failReply(w, http.StatusServiceUnavailable, "trace file not written: %v", err)
		return
	}
	writeReply(w, http.StatusOK, controlReply{OK: true})
}

func writeReply(w http.ResponseWriter, status int, reply controlReply) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(reply)
}

func failReply(w http.ResponseWriter, status int, format string, args ...any) {
	writeReply(w, status, controlReply{Error: fmt.Sprintf(format, args...)})
}

// reset restores the fixture values of every unit, or of the one named by ?unit=N.
func (c *control) reset(w http.ResponseWriter, r *http.Request) {
	c.mu.Lock()
	defer c.mu.Unlock()

	q := r.URL.Query().Get("unit")
	if q == "" {
		c.handler.ResetState(nil)
		_, _ = fmt.Fprintln(c.progress, "♻️  State reset to the fixture values")
		writeReply(w, http.StatusOK, controlReply{OK: true})
		return
	}
	n, err := strconv.ParseUint(q, 10, 8)
	if err != nil {
		failReply(w, http.StatusBadRequest, "unit %q is not a unit id", q)
		return
	}
	unit := uint8(n)
	if !c.handler.ResetState(&unit) {
		failReply(w, http.StatusNotFound, "unit %d is not in the fixture", unit)
		return
	}
	_, _ = fmt.Fprintf(c.progress, "♻️  Unit %d reset to the fixture values\n", unit)
	writeReply(w, http.StatusOK, controlReply{OK: true, Unit: &unit})
}

// reload reads the fixture file again and serves it from then on. A fixture that is not
// valid is refused and the simulator carries on with what it has.
func (c *control) reload(w http.ResponseWriter, _ *http.Request) {
	c.mu.Lock()
	defer c.mu.Unlock()

	f, err := ValidateFixtureFile(c.fixturePath)
	if err != nil {
		failReply(w, http.StatusUnprocessableEntity, "fixture not reloaded: %v", err)
		return
	}
	store, err := BuildStore(f)
	if err != nil {
		failReply(w, http.StatusUnprocessableEntity, "fixture not reloaded: %v", err)
		return
	}
	c.handler.ReplaceStore(store)
	summary := Summarize(f)
	_, _ = fmt.Fprintf(c.progress, "🔄 Fixture reloaded: %d device(s)\n", len(summary.Devices))
	writeReply(w, http.StatusOK, controlReply{OK: true, Fixture: &summary})
}
