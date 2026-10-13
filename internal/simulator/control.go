package simulator

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
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

func (c *control) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeReply(w, http.StatusOK, controlReply{OK: true})
	})
	mux.HandleFunc("POST /reset", c.reset)
	mux.HandleFunc("POST /reload", c.reload)
	return mux
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
