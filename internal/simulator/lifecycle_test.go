package simulator

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/otfabric/go-modbus"
	"github.com/otfabric/modbus-simulator/internal/leakcheck"
	"github.com/stretchr/testify/require"
)

// A reload takes effect between two requests (REQUIREMENTS.md, Control API). A request
// whose rules were applied before the reload, and which takes effect after it, is answered
// from the new fixture; a fault that already acted on it is kept.
func TestHandler_ReloadTakesEffectBetweenRequests(t *testing.T) {
	const old = `{"schemaVersion":"modbus-simulator.fixture.v1","broadcast":true,"devices":[{"unitId":2,
	  "state":{"holdingRegisters":[{"address":100,"value":7},{"address":101,"value":8}]},
	  "behavior":{"faults":[{"id":"slow","match":{"address":100},"fault":"delay","delayMs":1},
	    {"id":"silent","match":{"address":101},"fault":"timeout"}]}}]}`
	read := func(addr uint16) func(h *Handler) ([]uint16, error) {
		return func(h *Handler) ([]uint16, error) {
			return h.HandleHoldingRegisters(context.Background(), &modbus.HoldingRegistersRequest{UnitID: 2, FunctionCode: 3, Addr: addr, Quantity: 1})
		}
	}
	broadcastWrite := func(h *Handler) ([]uint16, error) {
		return h.HandleHoldingRegisters(context.Background(), &modbus.HoldingRegistersRequest{UnitID: 0, FunctionCode: 6, Addr: 100, IsWrite: true, Args: []uint16{5}})
	}
	for _, tc := range []struct {
		name     string
		request  func(h *Handler) ([]uint16, error)
		reloaded string // the devices of the new fixture
		wantErr  error
		want     []uint16
		wantRule string
		wantExc  string
		fault    string
	}{
		{
			name: "unit removed", request: read(100),
			reloaded: `"devices":[{"unitId":7,"state":{}}]`,
			wantErr:  modbus.ErrIllegalFunction, wantExc: ExcIllegalFunction,
		},
		{
			name: "exception rule added", request: read(100),
			reloaded: `"devices":[{"unitId":2,"state":{"holdingRegisters":[{"address":100,"value":9}]},
			  "behavior":{"exceptions":[{"id":"guard","match":{"address":100},"exceptionCode":"illegal_data_value"}]}}]`,
			wantErr: modbus.ErrIllegalDataValue, wantRule: "guard", wantExc: ExcIllegalDataValue,
		},
		{
			name: "new state, rule gone", request: read(100),
			reloaded: `"devices":[{"unitId":2,"state":{"holdingRegisters":[{"address":100,"value":9}]}}]`,
			want:     []uint16{9},
		},
		{
			name: "broadcast turned off", request: broadcastWrite,
			reloaded: `"devices":[{"unitId":2,"state":{"holdingRegisters":[{"address":100,"value":9}]}}]`,
			wantErr:  modbus.ErrIllegalFunction, wantExc: ExcIllegalFunction,
		},
		{
			name: "a fault that acted is kept", request: read(101),
			reloaded: `"devices":[{"unitId":2,"state":{"holdingRegisters":[{"address":101,"value":9}]}}]`,
			wantErr:  modbus.ErrNoResponse, wantRule: "silent", fault: FaultTimeout,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := &captureWriter{}
			h := buildHandler(t, old, w)
			entered, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			h.beforeCommit = func() { once.Do(func() { close(entered); <-release }) }

			type answer struct {
				vals []uint16
				err  error
			}
			done := make(chan answer, 1)
			go func() {
				vals, err := tc.request(h)
				done <- answer{vals, err}
			}()
			<-entered
			nf, err := ValidateFixtureBytes([]byte(`{"schemaVersion":"modbus-simulator.fixture.v1",` + tc.reloaded + `}`))
			require.NoError(t, err)
			ns, err := BuildStore(nf)
			require.NoError(t, err)
			h.ReplaceStore(ns)
			close(release)
			got := <-done

			if tc.wantErr != nil {
				require.ErrorIs(t, got.err, tc.wantErr)
			} else {
				require.NoError(t, got.err)
				require.Equal(t, tc.want, got.vals)
			}
			require.Len(t, w.recs, 1)
			rec := w.recs[0]
			require.Equal(t, tc.wantRule, rec.MatchedRule)
			require.Equal(t, tc.wantExc, rec.Response.ExceptionCode)
			require.Equal(t, tc.fault, rec.Response.Fault)
			require.Equal(t, tc.wantErr == nil, rec.Response.OK)
		})
	}
}

// blockingTrace is a trace file whose writes wait until released.
type blockingTrace struct {
	writing chan struct{} // closed when the first write starts
	release chan struct{}
	once    sync.Once
}

func newBlockingTrace() *blockingTrace {
	return &blockingTrace{writing: make(chan struct{}), release: make(chan struct{})}
}

func (b *blockingTrace) Write(p []byte) (int, error) {
	b.once.Do(func() { close(b.writing) })
	<-b.release
	return len(p), nil
}

func (b *blockingTrace) Close() error { return nil }

// syncBuffer is a bytes.Buffer that can be written and read concurrently.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// runSim starts Run with test hooks and returns what it started, the cancel function and
// the channel Run's result arrives on.
func runSim(t *testing.T, opts RunOptions, progress io.Writer) (*running, context.CancelFunc, <-chan error) {
	t.Helper()
	ready := make(chan *running, 1)
	if opts.test == nil {
		opts.test = &runTest{}
	}
	opts.test.ready = func(r *running) { ready <- r }
	opts.FixturePath = writeFixture(t, stressFixture())
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, opts, progress) }()
	t.Cleanup(cancel)
	select {
	case r := <-ready:
		return r, cancel, done
	case err := <-done:
		t.Fatalf("Run returned before it served: %v", err)
		return nil, nil, nil
	}
}

// Once its context is cancelled, Run returns within the shutdown bound with an error, also
// when a request cannot finish because its trace record cannot be written and a control
// request waits behind it. The trace is then left to that request.
func TestRun_ShutdownIsBounded(t *testing.T) {
	baseline := leakcheck.Take()
	trace := newBlockingTrace()
	control := fmt.Sprintf("127.0.0.1:%d", freePort(t))
	listen := fmt.Sprintf("127.0.0.1:%d", freePort(t))
	const bound = 300 * time.Millisecond
	r, cancel, done := runSim(t, RunOptions{
		Listen: listen, TracePath: "trace.jsonl", Control: control,
		test: &runTest{
			shutdownTimeout: bound,
			createTrace:     func(string) (io.WriteCloser, error) { return trace, nil },
		},
	}, nil)

	conn, err := rawDial(listen)
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()
	_, err = conn.Write(mbapFrame(1, 1, readPDU(3, 0, 1)))
	require.NoError(t, err)
	<-trace.writing // the request holds the commit lock in its trace write

	resetDone := make(chan struct{})
	go func() {
		defer close(resetDone)
		resp, err := http.Post("http://"+control+"/reset", "", nil)
		if err == nil {
			_ = resp.Body.Close()
		}
	}()
	// The reset holds control.mu while it waits for the commit lock.
	require.Eventually(t, func() bool {
		if r.ctl.mu.TryLock() {
			r.ctl.mu.Unlock()
			return false
		}
		return true
	}, 5*time.Second, time.Millisecond)
	require.EqualValues(t, 1, r.handler.inProgress())

	cancel()
	select {
	case err := <-done:
		require.Error(t, err)
		require.NotErrorIs(t, err, context.Canceled)
		require.ErrorContains(t, err, "shutdown: 1 request(s) still in progress after 300ms; the trace file was not closed")
	case <-time.After(5 * time.Second):
		close(trace.release)
		t.Fatal("Run did not return within 5 s of the cancellation; the shutdown bound is 300 ms")
	}

	// Released, the request and the reset end, and nothing is left running.
	close(trace.release)
	<-resetDone
	require.Eventually(t, func() bool { return r.handler.inProgress() == 0 }, 5*time.Second, time.Millisecond)
	_ = conn.Close()
	requireNoGoroutineLeak(t, baseline)
}

type failingTrace struct{}

func (failingTrace) Write([]byte) (int, error) { return 0, errors.New("disk full") }
func (failingTrace) Close() error              { return nil }

// A trace that cannot be written is reported while the simulator runs: one warning line,
// and /healthz answers 503 with the error. Requests are still served, and Run reports the
// error when it ends.
func TestRun_TraceWriteFailureIsReported(t *testing.T) {
	control := fmt.Sprintf("127.0.0.1:%d", freePort(t))
	var progress syncBuffer
	listen := fmt.Sprintf("127.0.0.1:%d", freePort(t))
	_, cancel, done := runSim(t, RunOptions{
		Listen: listen, TracePath: "trace.jsonl", Control: control,
		test: &runTest{createTrace: func(string) (io.WriteCloser, error) { return failingTrace{}, nil }},
	}, &progress)

	status, reply := httpDo(t, http.MethodGet, "http://"+control+"/healthz")
	require.Equal(t, http.StatusOK, status)
	require.True(t, reply.OK)

	c, err := modbus.New(modbus.Config{URL: "tcp://" + listen, Timeout: 2 * time.Second})
	require.NoError(t, err)
	require.NoError(t, c.Open())
	for range 2 {
		got, err := c.ReadHoldingRegisters(context.Background(), 1, 0, 1)
		require.NoError(t, err)
		require.Equal(t, []uint16{0}, got)
	}
	require.NoError(t, c.Close())

	require.Equal(t, 1, strings.Count(progress.String(), "Trace file not written"), progress.String())
	require.Contains(t, progress.String(), "disk full")
	status, reply = httpDo(t, http.MethodGet, "http://"+control+"/healthz")
	require.Equal(t, http.StatusServiceUnavailable, status)
	require.False(t, reply.OK)
	require.Contains(t, reply.Error, "trace file not written: disk full")

	cancel()
	require.ErrorContains(t, <-done, "write trace file: disk full")
}
