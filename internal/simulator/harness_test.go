package simulator

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/otfabric/go-modbus"
	"github.com/stretchr/testify/require"
)

// The chaos and stress tests share one fixture.
//
// Unit 1 is used by well-behaved clients. Each worker owns a block of blockSize
// addresses in every area (index 0-11 writable, 12-13 read-only, 14-15 absent), which it
// checks against a model of its own. All workers share the "hot" block, whose registers
// and coils are always written together with one value.
//
// Unit 2 is the target of the chaos monkeys (zeroForReads, a delay fault, an exception
// rule). Unit 3 resets on every new connection.
const (
	stressWorkers = 8
	blockSize     = 16
	blockPresent  = 14
	blockWritable = 12
	hotBase       = 5000
	hotSize       = 8

	chaosUnit      = 2
	chaosDelayAddr = 100
	chaosDelayMs   = 50
	chaosExcAddr   = 200

	// The timeout and connectionClose rules match one exact request each, so that a
	// monkey's random PDU does not run into them.
	chaosFaultFC       = 4
	chaosFaultQty      = 77
	chaosTimeoutAddr   = 30000
	chaosConnCloseAddr = 31000
)

func blockBase(worker int) uint16 { return uint16(worker * 100) }

func stressFixture() *Fixture {
	no := false
	var st State
	for w := 0; w < stressWorkers; w++ {
		for i := 0; i < blockPresent; i++ {
			addr := blockBase(w) + uint16(i)
			hp := Point{Address: addr, Value: int(addr)}
			cp := Point{Address: addr, Value: i%2 == 0}
			if i >= blockWritable {
				hp.Writable, cp.Writable = &no, &no
			}
			st.HoldingRegisters = append(st.HoldingRegisters, hp)
			st.Coils = append(st.Coils, cp)
			st.InputRegisters = append(st.InputRegisters, Point{Address: addr, Value: int(addr) + 1000})
			st.DiscreteInputs = append(st.DiscreteInputs, Point{Address: addr, Value: i%3 == 0})
		}
	}
	for i := 0; i < hotSize; i++ {
		st.HoldingRegisters = append(st.HoldingRegisters, Point{Address: hotBase + uint16(i), Value: 0})
		st.Coils = append(st.Coils, Point{Address: hotBase + uint16(i), Value: false})
	}

	var chaos State
	for i := 0; i < 10; i++ {
		chaos.HoldingRegisters = append(chaos.HoldingRegisters, Point{Address: uint16(i), Value: i})
		chaos.Coils = append(chaos.Coils, Point{Address: uint16(i), Value: false})
	}
	chaos.HoldingRegisters = append(chaos.HoldingRegisters,
		Point{Address: chaosDelayAddr, Value: 7}, Point{Address: chaosExcAddr, Value: 9})
	delayAddr, excAddr := uint16(chaosDelayAddr), uint16(chaosExcAddr)
	exactly := func(addr uint16) Match {
		fc, qty := uint8(chaosFaultFC), uint16(chaosFaultQty)
		return Match{FC: &fc, Address: &addr, Quantity: &qty}
	}

	return &Fixture{
		SchemaVersion: FixtureSchemaVersion,
		Name:          "chaos-and-stress",
		Devices: []Device{
			{UnitID: 1, State: st},
			{
				UnitID:   chaosUnit,
				Identity: &Identity{VendorName: "OTFabric", ProductCode: "CHAOS", MajorMinorRevision: "1.0"},
				State:    chaos,
				Behavior: &Behavior{
					UnknownAddress: UnknownAddressZeroForReads,
					Exceptions: []ExceptionRule{{
						ID: "forced", Match: Match{Address: &excAddr}, ExceptionCode: ExcSlaveDeviceFailure,
					}},
					Faults: []FaultRule{
						{ID: "slow", Match: Match{Address: &delayAddr}, Fault: FaultDelay, DelayMs: chaosDelayMs},
						{ID: "silent", Match: exactly(chaosTimeoutAddr), Fault: FaultTimeout},
						{ID: "hangup", Match: exactly(chaosConnCloseAddr), Fault: FaultConnectionClose},
					},
				},
			},
			{
				UnitID:   3,
				State:    State{HoldingRegisters: []Point{{Address: 0, Value: 1}}},
				Behavior: &Behavior{StateMode: StateModeResetOnConnection},
			},
		},
	}
}

func writeFixture(t testing.TB, f *Fixture) string {
	t.Helper()
	data, err := json.Marshal(f)
	require.NoError(t, err)
	_, err = ValidateFixtureBytes(data)
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "fixture.json")
	require.NoError(t, os.WriteFile(path, data, 0o644))
	return path
}

// sim is a simulator running on a local port.
type sim struct {
	port        int
	tracePath   string
	fixturePath string
	stopOnce    sync.Once
	cancel      context.CancelFunc
	done        chan error
}

// startSim runs the simulator for fixture f. It is stopped by stop or, at the latest,
// when the test ends.
func startSim(t *testing.T, f *Fixture, opts RunOptions) *sim {
	t.Helper()
	s := &sim{port: freePort(t), tracePath: filepath.Join(t.TempDir(), "trace.jsonl"), done: make(chan error, 1)}
	s.fixturePath = writeFixture(t, f)
	opts.FixturePath = s.fixturePath
	opts.Listen = fmt.Sprintf("127.0.0.1:%d", s.port)
	opts.TracePath = s.tracePath
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	go func() { s.done <- Run(ctx, opts, nil) }()
	t.Cleanup(func() { s.stop(t) })
	if opts.TLSEnabled() {
		return s // the caller waits for the listener with a TLS client
	}
	c := dialClient(t, s.port)
	require.NoError(t, c.Close())
	return s
}

func (s *sim) stop(t *testing.T) {
	t.Helper()
	s.stopOnce.Do(func() {
		s.cancel()
		waitServerDone(t, s.done)
	})
}

func (s *sim) addr() string { return fmt.Sprintf("127.0.0.1:%d", s.port) }

// records returns the trace, checking that every line is valid against the trace schema
// and that seq counts 1, 2, 3, ... in file order.
func (s *sim) records(t *testing.T) []TraceRecord {
	t.Helper()
	_, err := LoadTraceRecords(s.tracePath)
	require.NoError(t, err)
	data, err := os.ReadFile(s.tracePath)
	require.NoError(t, err)
	recs := decodeRecords(t, splitJSONL(string(data)))
	requireContiguousSeq(t, recs)
	return recs
}

func requireContiguousSeq(t testing.TB, recs []TraceRecord) {
	t.Helper()
	for i, rec := range recs {
		if rec.Seq != uint64(i+1) {
			t.Fatalf("trace line %d has seq %d: seq must follow the order of the file", i+1, rec.Seq)
		}
	}
}

// testSeed returns the seed of a randomized test and logs it. Set
// MODBUS_SIMULATOR_TEST_SEED to replay a failing run.
func testSeed(t testing.TB) int64 {
	t.Helper()
	seed := time.Now().UnixNano()
	if v := os.Getenv("MODBUS_SIMULATOR_TEST_SEED"); v != "" {
		parsed, err := strconv.ParseInt(v, 10, 64)
		require.NoError(t, err, "MODBUS_SIMULATOR_TEST_SEED")
		seed = parsed
	}
	t.Logf("seed %d (replay with MODBUS_SIMULATOR_TEST_SEED=%d)", seed, seed)
	return seed
}

// soakRounds calls round until MODBUS_SIMULATOR_SOAK (a Go duration) has passed, and
// once when it is not set.
func soakRounds(t *testing.T, round func(t *testing.T, n int)) {
	t.Helper()
	var d time.Duration
	if v := os.Getenv("MODBUS_SIMULATOR_SOAK"); v != "" {
		parsed, err := time.ParseDuration(v)
		require.NoError(t, err, "MODBUS_SIMULATOR_SOAK")
		d = parsed
	}
	deadline := time.Now().Add(d)
	for n := 0; ; n++ {
		round(t, n)
		if t.Failed() || !time.Now().Before(deadline) {
			return
		}
	}
}

// requireNoGoroutineLeak waits for the goroutine count to return to the baseline.
func requireNoGoroutineLeak(t *testing.T, baseline int) {
	t.Helper()
	const slack = 2
	deadline := time.Now().Add(5 * time.Second)
	for runtime.NumGoroutine() > baseline+slack {
		if time.Now().After(deadline) {
			buf := make([]byte, 1<<20)
			t.Fatalf("goroutine leak: %d running, baseline %d\n%s",
				runtime.NumGoroutine(), baseline, buf[:runtime.Stack(buf, true)])
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// --- trace replay ---

type pointKey struct {
	unit uint8
	area string
	addr uint16
}

// replayer checks that a trace is a valid sequential history of the fixture: applied in
// file order, every read returns the values the earlier writes left behind and every
// write reports exactly the points it changed.
type replayer struct {
	state        map[pointKey]any
	readOnly     map[pointKey]bool
	units        []uint8
	zeroForReads map[uint8]bool
	skip         map[uint8]bool
	broadcast    bool
}

func newReplayer(f *Fixture) *replayer {
	rp := &replayer{
		state: map[pointKey]any{}, readOnly: map[pointKey]bool{},
		zeroForReads: map[uint8]bool{}, skip: map[uint8]bool{}, broadcast: f.Broadcast,
	}
	for i := range f.Devices {
		d := &f.Devices[i]
		rp.units = append(rp.units, d.UnitID)
		rp.zeroForReads[d.UnitID] = f.ResolvedUnknownAddress(d) == UnknownAddressZeroForReads
		// A resetOnConnection device has a state per connection, which is not replayed.
		rp.skip[d.UnitID] = f.ResolvedStateMode(d) == StateModeResetOnConnection
		for area, points := range map[string][]Point{
			AreaCoils: d.State.Coils, AreaDiscreteInputs: d.State.DiscreteInputs,
			AreaHoldingRegisters: d.State.HoldingRegisters, AreaInputRegisters: d.State.InputRegisters,
		} {
			for _, p := range points {
				key := pointKey{d.UnitID, area, p.Address}
				rp.state[key] = p.Value
				rp.readOnly[key] = p.Writable != nil && !*p.Writable
			}
		}
	}
	sort.Slice(rp.units, func(i, j int) bool { return rp.units[i] < rp.units[j] })
	return rp
}

func normValue(v any) any {
	if f, ok := v.(float64); ok {
		return int(f)
	}
	return v
}

func zeroOf(area string) any {
	if area == AreaCoils || area == AreaDiscreteInputs {
		return false
	}
	return 0
}

// write applies values from start to one unit and returns the state changes, or false
// when the unit cannot take the write. mask, when set, computes the value from the
// current one (FC22).
func (rp *replayer) write(unit uint8, area string, start uint16, values []any, mask func(cur int) int) ([]StateChange, bool) {
	if uint32(start)+uint32(len(values)) > 65536 {
		return nil, false
	}
	for i := range values {
		key := pointKey{unit, area, start + uint16(i)}
		if _, present := rp.state[key]; !present || rp.readOnly[key] {
			return nil, false
		}
	}
	var changes []StateChange
	for i, v := range values {
		key := pointKey{unit, area, start + uint16(i)}
		cur, nv := rp.state[key], normValue(v)
		if mask != nil {
			nv = mask(cur.(int))
		}
		if nv != cur {
			changes = append(changes, StateChange{Area: area, Address: key.addr, Before: cur, After: nv})
			rp.state[key] = nv
		}
	}
	return changes, true
}

func (rp *replayer) apply(rec TraceRecord) error {
	fail := func(format string, args ...any) error {
		return fmt.Errorf("seq %d (unit %d, fc %d): %s", rec.Seq, rec.UnitID, rec.Request.FC, fmt.Sprintf(format, args...))
	}
	req := rec.Request
	if !rec.Response.OK || req.Address == nil {
		if len(rec.StateChanges) != 0 {
			return fail("state changes on a request that does not write")
		}
		return nil
	}
	if rec.UnitID == 0 && !rp.broadcast {
		return fail("request to unit 0 succeeded although broadcast is off")
	}
	if rp.skip[rec.UnitID] {
		return nil
	}

	// The write, if any.
	var values []any
	var mask func(int) int
	start := *req.Address
	switch req.FC {
	case 5, 6:
		values = []any{req.Value}
	case 15, 16:
		values, _ = req.Values.([]any)
	case 22:
		and, or := *req.AndMask, *req.OrMask
		values, mask = []any{nil}, func(cur int) int { return (cur & and) | (or &^ and) }
	case 23:
		values, _ = req.Values.([]any)
		start = *req.WriteAddress
	}

	var want []StateChange
	switch {
	case values == nil:
	case rec.UnitID == 0:
		if !rec.Response.NoResponse {
			return fail("broadcast was answered")
		}
		for _, unit := range rp.units {
			if rp.skip[unit] {
				continue
			}
			changes, _ := rp.write(unit, req.Area, start, values, mask)
			for _, sc := range changes {
				u := unit
				sc.UnitID = &u
				want = append(want, sc)
			}
		}
	default:
		changes, ok := rp.write(rec.UnitID, req.Area, start, values, mask)
		if !ok {
			return fail("write to an unknown or read-only address succeeded")
		}
		want = changes
	}

	var got []StateChange
	for _, sc := range rec.StateChanges {
		if sc.UnitID != nil && rp.skip[*sc.UnitID] {
			continue
		}
		got = append(got, sc)
	}
	if len(got) != len(want) {
		return fail("%d state changes, the trace so far implies %d", len(got), len(want))
	}
	for i, sc := range got {
		w := want[i]
		sameUnit := (sc.UnitID == nil) == (w.UnitID == nil) && (sc.UnitID == nil || *sc.UnitID == *w.UnitID)
		if !sameUnit || sc.Area != w.Area || sc.Address != w.Address || normValue(sc.Before) != w.Before || normValue(sc.After) != w.After {
			return fail("state change %d is %+v, the trace so far implies %+v", i, sc, w)
		}
	}

	// The read, if any, sees the state after the write.
	if req.FC > 4 && req.FC != 23 {
		return nil
	}
	read, _ := rec.Response.Values.([]any)
	if len(read) != int(req.Quantity) {
		return fail("%d values for quantity %d", len(read), req.Quantity)
	}
	for i, v := range read {
		key := pointKey{rec.UnitID, req.Area, *req.Address + uint16(i)}
		wantValue, present := rp.state[key]
		if !present {
			if !rp.zeroForReads[rec.UnitID] {
				return fail("read of unknown address %d succeeded", key.addr)
			}
			wantValue = zeroOf(req.Area)
		}
		if normValue(v) != wantValue {
			return fail("address %d reads %v, the trace so far leaves %v", key.addr, v, wantValue)
		}
	}
	return nil
}

func requireReplayable(t testing.TB, f *Fixture, recs []TraceRecord) *replayer {
	t.Helper()
	rp := newReplayer(f)
	for _, rec := range recs {
		if err := rp.apply(rec); err != nil {
			t.Fatalf("trace is not a sequential history: %v", err)
		}
	}
	return rp
}

// --- well-behaved worker ---

// blockModel is what one worker expects its block on unit 1 to contain.
type blockModel struct {
	base uint16
	hold [blockPresent]uint16
	coil [blockPresent]bool
}

func newBlockModel(worker int) *blockModel {
	m := &blockModel{base: blockBase(worker)}
	for i := range m.hold {
		m.hold[i] = m.base + uint16(i)
		m.coil[i] = i%2 == 0
	}
	return m
}

// requireMatchesReplay checks the model against the state a trace replay ended in.
func (m *blockModel) requireMatchesReplay(t *testing.T, rp *replayer) {
	t.Helper()
	for i := range m.hold {
		addr := m.base + uint16(i)
		require.Equal(t, int(m.hold[i]), rp.state[pointKey{1, AreaHoldingRegisters, addr}], "holding register %d", addr)
		require.Equal(t, m.coil[i], rp.state[pointKey{1, AreaCoils, addr}], "coil %d", addr)
	}
}

func checkOutcome(what string, err error, wantOK bool) error {
	switch {
	case wantOK && err != nil:
		return fmt.Errorf("%s: %w", what, err)
	case !wantOK && !errors.Is(err, modbus.ErrIllegalDataAddress):
		return fmt.Errorf("%s: want illegal data address, got %v", what, err)
	}
	return nil
}

func allEqual[T comparable](vals []T) bool {
	for _, v := range vals {
		if v != vals[0] {
			return false
		}
	}
	return true
}

// step sends one random request and checks the answer against the model.
func (m *blockModel) step(ctx context.Context, c *modbus.Client, rng *rand.Rand) error {
	const unit = 1
	if rng.Intn(5) == 0 {
		return hotStep(ctx, c, rng)
	}
	idx := rng.Intn(blockSize + 2)
	qty := 1 + rng.Intn(6)
	addr := m.base + uint16(idx)
	readable, writable := idx+qty <= blockPresent, idx+qty <= blockWritable
	what := func(op string) string { return fmt.Sprintf("%s addr %d qty %d", op, addr, qty) }

	switch rng.Intn(10) {
	case 8:
		and, or := uint16(rng.Intn(1<<16)), uint16(rng.Intn(1<<16))
		ok := idx < blockWritable
		if err := checkOutcome(what("mask write"), c.MaskWriteRegister(ctx, unit, addr, and, or), ok); err != nil || !ok {
			return err
		}
		m.hold[idx] = (m.hold[idx] & and) | (or &^ and)
	case 9:
		// Write first, then read: the read sees what was just written.
		widx := rng.Intn(blockSize + 2)
		vals := make([]uint16, 1+rng.Intn(4))
		for i := range vals {
			vals[i] = uint16(rng.Intn(1 << 16))
		}
		ok := readable && widx+len(vals) <= blockWritable
		got, err := c.ReadWriteMultipleRegisters(ctx, unit, addr, uint16(qty), m.base+uint16(widx), vals)
		if err := checkOutcome(what("read/write"), err, ok); err != nil || !ok {
			return err
		}
		copy(m.hold[widx:], vals)
		for i, v := range got {
			if v != m.hold[idx+i] {
				return fmt.Errorf("%s: [%d] = %d, model has %d", what("read/write"), i, v, m.hold[idx+i])
			}
		}
	case 0:
		got, err := c.ReadHoldingRegisters(ctx, unit, addr, uint16(qty))
		if err := checkOutcome(what("read holding"), err, readable); err != nil || !readable {
			return err
		}
		for i, v := range got {
			if v != m.hold[idx+i] {
				return fmt.Errorf("%s: [%d] = %d, model has %d", what("read holding"), i, v, m.hold[idx+i])
			}
		}
	case 1:
		got, err := c.ReadCoils(ctx, unit, addr, uint16(qty))
		if err := checkOutcome(what("read coils"), err, readable); err != nil || !readable {
			return err
		}
		for i, v := range got {
			if v != m.coil[idx+i] {
				return fmt.Errorf("%s: [%d] = %t, model has %t", what("read coils"), i, v, m.coil[idx+i])
			}
		}
	case 2:
		got, err := c.ReadInputRegisters(ctx, unit, addr, uint16(qty))
		if err := checkOutcome(what("read input"), err, readable); err != nil || !readable {
			return err
		}
		for i, v := range got {
			if want := addr + uint16(i) + 1000; v != want {
				return fmt.Errorf("%s: [%d] = %d, want %d", what("read input"), i, v, want)
			}
		}
	case 3:
		got, err := c.ReadDiscreteInputs(ctx, unit, addr, uint16(qty))
		if err := checkOutcome(what("read discrete"), err, readable); err != nil || !readable {
			return err
		}
		for i, v := range got {
			if want := (idx+i)%3 == 0; v != want {
				return fmt.Errorf("%s: [%d] = %t, want %t", what("read discrete"), i, v, want)
			}
		}
	case 4:
		v := uint16(rng.Intn(1 << 16))
		ok := idx < blockWritable
		if err := checkOutcome(what("write register"), c.WriteRegister(ctx, unit, addr, v), ok); err != nil || !ok {
			return err
		}
		m.hold[idx] = v
	case 5:
		vals := make([]uint16, qty)
		for i := range vals {
			vals[i] = uint16(rng.Intn(1 << 16))
		}
		if err := checkOutcome(what("write registers"), c.WriteRegisters(ctx, unit, addr, vals), writable); err != nil || !writable {
			return err
		}
		copy(m.hold[idx:], vals)
	case 6:
		v := rng.Intn(2) == 0
		ok := idx < blockWritable
		if err := checkOutcome(what("write coil"), c.WriteCoil(ctx, unit, addr, v), ok); err != nil || !ok {
			return err
		}
		m.coil[idx] = v
	default:
		vals := make([]bool, qty)
		for i := range vals {
			vals[i] = rng.Intn(2) == 0
		}
		if err := checkOutcome(what("write coils"), c.WriteCoils(ctx, unit, addr, vals), writable); err != nil || !writable {
			return err
		}
		copy(m.coil[idx:], vals)
	}
	return nil
}

// hotStep reads or writes the block all workers share. A write sets every point to the
// same value, so a read that sees two different values has seen half a write.
func hotStep(ctx context.Context, c *modbus.Client, rng *rand.Rand) error {
	const unit = 1
	switch rng.Intn(4) {
	case 0:
		vals := make([]uint16, hotSize)
		v := uint16(rng.Intn(1 << 16))
		for i := range vals {
			vals[i] = v
		}
		return checkOutcome("write hot registers", c.WriteRegisters(ctx, unit, hotBase, vals), true)
	case 1:
		vals := make([]bool, hotSize)
		v := rng.Intn(2) == 0
		for i := range vals {
			vals[i] = v
		}
		return checkOutcome("write hot coils", c.WriteCoils(ctx, unit, hotBase, vals), true)
	case 2:
		got, err := c.ReadHoldingRegisters(ctx, unit, hotBase, hotSize)
		if err != nil {
			return fmt.Errorf("read hot registers: %w", err)
		}
		if !allEqual(got) {
			return fmt.Errorf("torn write: hot registers read %v", got)
		}
	default:
		got, err := c.ReadCoils(ctx, unit, hotBase, hotSize)
		if err != nil {
			return fmt.Errorf("read hot coils: %w", err)
		}
		if !allEqual(got) {
			return fmt.Errorf("torn write: hot coils read %v", got)
		}
	}
	return nil
}

// runWorkers runs one client per worker, each sending ops random requests, and returns
// their models.
func runWorkers(t *testing.T, s *sim, seed int64, ops int) []*blockModel {
	t.Helper()
	models := make([]*blockModel, stressWorkers)
	errs := make([]error, stressWorkers)
	var wg sync.WaitGroup
	for w := 0; w < stressWorkers; w++ {
		models[w] = newBlockModel(w)
		c := dialClient(t, s.port)
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			defer func() { _ = c.Close() }()
			rng := rand.New(rand.NewSource(seed + int64(w)))
			for i := 0; i < ops; i++ {
				if err := models[w].step(context.Background(), c, rng); err != nil {
					errs[w] = fmt.Errorf("worker %d, request %d: %w", w, i, err)
					return
				}
			}
		}(w)
	}
	wg.Wait()
	for _, err := range errs {
		require.NoError(t, err)
	}
	return models
}

// --- raw Modbus/TCP ---

func mbapFrame(txn uint16, unit byte, pdu []byte) []byte {
	frame := make([]byte, 7, 7+len(pdu))
	binary.BigEndian.PutUint16(frame[0:], txn)
	binary.BigEndian.PutUint16(frame[4:], uint16(len(pdu)+1))
	frame[6] = unit
	return append(frame, pdu...)
}

func readPDU(fc byte, addr, qty uint16) []byte {
	pdu := []byte{fc, 0, 0, 0, 0}
	binary.BigEndian.PutUint16(pdu[1:], addr)
	binary.BigEndian.PutUint16(pdu[3:], qty)
	return pdu
}

func rawDial(addr string) (*net.TCPConn, error) {
	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		return nil, err
	}
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	return conn.(*net.TCPConn), nil
}

// violation is a response that breaks the protocol, as opposed to a connection error.
type violation string

func (v violation) Error() string { return string(v) }

func violationf(format string, args ...any) error { return violation(fmt.Sprintf(format, args...)) }

// readFrame reads one response frame.
func readFrame(conn net.Conn) (txn uint16, unit byte, pdu []byte, err error) {
	var hdr [7]byte
	if n, err := io.ReadFull(conn, hdr[:]); err != nil {
		if n > 0 && !isTimeout(err) {
			return 0, 0, nil, violationf("connection closed %d bytes into a response header", n)
		}
		return 0, 0, nil, err
	}
	if proto := binary.BigEndian.Uint16(hdr[2:]); proto != 0 {
		return 0, 0, nil, violationf("response with protocol id %d", proto)
	}
	n := int(binary.BigEndian.Uint16(hdr[4:]))
	if n < 2 || n > 254 {
		return 0, 0, nil, violationf("response with length field %d", n)
	}
	pdu = make([]byte, n-1)
	if _, err = io.ReadFull(conn, pdu); err != nil {
		if isTimeout(err) {
			return 0, 0, nil, err
		}
		return 0, 0, nil, violationf("connection closed inside a response of %d bytes", n)
	}
	return binary.BigEndian.Uint16(hdr[0:]), hdr[6], pdu, nil
}

// expectResponse reads the response to a request and checks that it belongs to it: the
// same transaction and unit, and either the function code or its exception.
func expectResponse(conn net.Conn, txn uint16, unit, fc byte) ([]byte, error) {
	gotTxn, gotUnit, pdu, err := readFrame(conn)
	if err != nil {
		return nil, err
	}
	if gotTxn != txn || gotUnit != unit {
		return nil, violationf("response for txn %d unit %d, want txn %d unit %d", gotTxn, gotUnit, txn, unit)
	}
	switch pdu[0] {
	case fc:
	case fc | 0x80:
		if len(pdu) != 2 || pdu[1] == 0 || pdu[1] > 0x0B {
			return nil, violationf("malformed exception response % x", pdu)
		}
	default:
		return nil, violationf("response with function code 0x%02x to a request with 0x%02x", pdu[0], fc)
	}
	return pdu, nil
}

func isException(pdu []byte, code byte) bool {
	return len(pdu) == 2 && pdu[0]&0x80 != 0 && pdu[1] == code
}

func isTimeout(err error) bool {
	var nerr net.Error
	return errors.As(err, &nerr) && nerr.Timeout()
}
