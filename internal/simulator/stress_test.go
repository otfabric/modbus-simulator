package simulator

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/otfabric/go-modbus"
	"github.com/stretchr/testify/require"
)

// Parallel clients each check their own block against a model while they all contend for
// the shared block. Afterwards the trace must hold exactly one record per request and
// replay as a sequential history that ends in the state the models expect.
func TestStress_ParallelClients(t *testing.T) {
	seed := testSeed(t)
	soakRounds(t, func(t *testing.T, n int) {
		const ops = 300
		baseline := runtime.NumGoroutine()
		f := stressFixture()
		s := startSim(t, f, RunOptions{})

		models := runWorkers(t, s, seed+int64(n)*1000, ops)
		s.stop(t)

		recs := s.records(t)
		require.Len(t, recs, stressWorkers*ops)
		rp := requireReplayable(t, f, recs)
		for _, m := range models {
			m.requireMatchesReplay(t, rp)
		}
		requireNoGoroutineLeak(t, baseline)
	})
}

// The handler is called from many goroutines at once, as the server does with one
// goroutine per connection, with connections that come and go.
func TestStress_HandlerParallel(t *testing.T) {
	seed := testSeed(t)
	f := stressFixture()
	data, err := os.ReadFile(writeFixture(t, f))
	require.NoError(t, err)
	parsed, err := ValidateFixtureBytes(data)
	require.NoError(t, err)
	store, err := BuildStore(parsed)
	require.NoError(t, err)
	var buf bytes.Buffer
	w := NewJSONLWriter(&buf)
	h := NewHandler(store, w, HandlerOptions{TraceDecoded: true, TraceRaw: true})

	const goroutines, ops = 16, 400
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed + int64(g)))
			ctx, disconnect := context.WithCancel(context.Background())
			for i := 0; i < ops; i++ {
				if i%50 == 0 { // a new connection
					disconnect()
					ctx, disconnect = context.WithCancel(context.Background())
				}
				randomHandlerCall(ctx, h, rng, fmt.Sprintf("10.0.%d.%d:502", g, i/50))
			}
			disconnect()
		}(g)
	}
	wg.Wait()
	require.NoError(t, w.Close())

	recs := decodeRecords(t, splitJSONL(buf.String()))
	require.Len(t, recs, goroutines*ops)
	requireContiguousSeq(t, recs)
	requireReplayable(t, f, recs)

	// Every connection is gone, so no client address and none of its state may be kept.
	require.Eventually(t, func() bool {
		n := 0
		h.conns.Range(func(any, any) bool { n++; return true })
		return n == 0 && store.Sessions() == 0
	}, 2*time.Second, 10*time.Millisecond)
}

// randomHandlerCall makes one handler call with arbitrary parameters and panics when the
// answer has the wrong shape.
func randomHandlerCall(ctx context.Context, h *Handler, rng *rand.Rand, clientAddr string) {
	unit := uint8(1 + rng.Intn(4)) // unit 4 does not exist
	var addr uint16
	switch rng.Intn(4) {
	case 0:
		addr = hotBase + uint16(rng.Intn(hotSize+2))
	case 1:
		addr = uint16(65530 + rng.Intn(6))
	default:
		addr = uint16(rng.Intn(stressWorkers*100 + 20))
	}
	qty := uint16(1 + rng.Intn(12))
	check := func(n int, err error) {
		if err == nil && n != int(qty) {
			panic(fmt.Sprintf("%d values for quantity %d", n, qty))
		}
	}
	regs := make([]uint16, qty)
	bits := make([]bool, qty)
	for i := range regs {
		regs[i], bits[i] = uint16(rng.Intn(4)), rng.Intn(2) == 0
	}
	switch rng.Intn(11) {
	case 9:
		_ = h.HandleMaskWrite(ctx, &modbus.MaskWriteRequest{ClientAddr: clientAddr, UnitID: unit, FunctionCode: 22, Addr: addr, AndMask: regs[0], OrMask: uint16(rng.Intn(1 << 16))})
	case 10:
		res, err := h.HandleReadWriteRegisters(ctx, &modbus.ReadWriteRegistersRequest{
			ClientAddr: clientAddr, UnitID: unit, FunctionCode: 23, ReadAddr: addr, ReadQty: qty,
			WriteAddr: addr + uint16(rng.Intn(4)), WriteValues: regs[:1+rng.Intn(len(regs))],
		})
		check(len(res), err)
	case 0:
		res, err := h.HandleCoils(ctx, &modbus.CoilsRequest{ClientAddr: clientAddr, UnitID: unit, FunctionCode: 1, Addr: addr, Quantity: qty})
		check(len(res), err)
	case 1:
		res, err := h.HandleDiscreteInputs(ctx, &modbus.DiscreteInputsRequest{ClientAddr: clientAddr, UnitID: unit, FunctionCode: 2, Addr: addr, Quantity: qty})
		check(len(res), err)
	case 2:
		res, err := h.HandleHoldingRegisters(ctx, &modbus.HoldingRegistersRequest{ClientAddr: clientAddr, UnitID: unit, FunctionCode: 3, Addr: addr, Quantity: qty})
		check(len(res), err)
	case 3:
		res, err := h.HandleInputRegisters(ctx, &modbus.InputRegistersRequest{ClientAddr: clientAddr, UnitID: unit, FunctionCode: 4, Addr: addr, Quantity: qty})
		check(len(res), err)
	case 4:
		_, _ = h.HandleCoils(ctx, &modbus.CoilsRequest{ClientAddr: clientAddr, UnitID: unit, FunctionCode: 5, Addr: addr, IsWrite: true, Args: bits[:1]})
	case 5:
		_, _ = h.HandleHoldingRegisters(ctx, &modbus.HoldingRegistersRequest{ClientAddr: clientAddr, UnitID: unit, FunctionCode: 6, Addr: addr, IsWrite: true, Args: regs[:1]})
	case 6:
		_, _ = h.HandleCoils(ctx, &modbus.CoilsRequest{ClientAddr: clientAddr, UnitID: unit, FunctionCode: 15, Addr: addr, IsWrite: true, Args: bits})
	case 7:
		_, _ = h.HandleHoldingRegisters(ctx, &modbus.HoldingRegistersRequest{ClientAddr: clientAddr, UnitID: unit, FunctionCode: 16, Addr: addr, IsWrite: true, Args: regs})
	default:
		_, _ = h.HandleDeviceIdentification(ctx, &modbus.DeviceIdentificationRequest{
			ClientAddr: clientAddr, UnitID: unit, MEIType: modbus.MEIReadDeviceIdentification, Category: modbus.DeviceIDBasic,
		})
	}
}

// --max-clients 0 means no limit.
func TestStress_NoClientLimitByDefault(t *testing.T) {
	s := startSim(t, stressFixture(), RunOptions{})
	const n = 32
	clients := make([]*modbus.Client, n)
	for i := range clients {
		clients[i] = dialClient(t, s.port)
		defer func(c *modbus.Client) { _ = c.Close() }(clients[i])
	}
	for i, c := range clients {
		got, err := c.ReadHoldingRegisters(context.Background(), 1, 0, 1)
		require.NoError(t, err, "client %d of %d", i+1, n)
		require.Equal(t, []uint16{0}, got)
	}
}

// With a client limit, a client over the limit is turned away without disturbing the
// others, and is served once a slot is free.
func TestStress_ClientLimit(t *testing.T) {
	s := startSim(t, stressFixture(), RunOptions{MaxClients: 2})
	ctx := context.Background()
	first, second := dialClient(t, s.port), dialClient(t, s.port)
	defer func() { _ = second.Close() }()
	for _, c := range []*modbus.Client{first, second} {
		_, err := c.ReadHoldingRegisters(ctx, 1, 0, 1)
		require.NoError(t, err)
	}

	extra, err := rawDial(s.addr())
	if err == nil {
		_ = extra.SetDeadline(time.Now().Add(500 * time.Millisecond))
		_, _ = extra.Write(mbapFrame(1, 1, readPDU(3, 0, 1)))
		_, _, _, err = readFrame(extra)
		_ = extra.Close()
	}
	require.Error(t, err, "a third client was served although the limit is 2")

	for _, c := range []*modbus.Client{first, second} {
		_, err := c.ReadHoldingRegisters(ctx, 1, 0, 1)
		require.NoError(t, err)
	}

	require.NoError(t, first.Close())
	require.Eventually(t, func() bool {
		c, err := modbus.New(modbus.Config{URL: "tcp://" + s.addr(), Timeout: 300 * time.Millisecond})
		if err != nil || c.Open() != nil {
			return false
		}
		defer func() { _ = c.Close() }()
		_, err = c.ReadHoldingRegisters(ctx, 1, 0, 1)
		return err == nil
	}, 3*time.Second, 50*time.Millisecond)
}

// Stopping the simulator does not wait for requests that are held up by a delay fault.
func TestStress_ShutdownWithRequestsInFlight(t *testing.T) {
	baseline := runtime.NumGoroutine()
	f := stressFixture()
	f.Devices[1].Behavior.Faults[0].DelayMs = 60_000
	s := startSim(t, f, RunOptions{})

	conns := make([]net.Conn, 6)
	for i := range conns {
		conn, err := rawDial(s.addr())
		require.NoError(t, err)
		defer func() { _ = conn.Close() }()
		_, err = conn.Write(mbapFrame(uint16(i), chaosUnit, readPDU(3, chaosDelayAddr, 1)))
		require.NoError(t, err)
		conns[i] = conn
	}
	time.Sleep(100 * time.Millisecond) // let the requests reach the handler

	start := time.Now()
	s.stop(t)
	require.Less(t, time.Since(start), 2*time.Second)

	for _, rec := range s.records(t) {
		require.False(t, rec.Response.OK)
		require.Equal(t, "slow", rec.MatchedRule)
	}
	for _, conn := range conns {
		_ = conn.Close()
	}
	requireNoGoroutineLeak(t, baseline)
}

// Many short-lived connections, also to the unit that resets on every connection.
func TestStress_ConnectionChurn(t *testing.T) {
	baseline := runtime.NumGoroutine()
	s := startSim(t, stressFixture(), RunOptions{})
	const goroutines, cycles = 8, 40
	errs := make(chan error, goroutines)
	for g := 0; g < goroutines; g++ {
		go func() {
			errs <- func() error {
				ctx := context.Background()
				for i := 0; i < cycles; i++ {
					c, err := modbus.New(modbus.Config{URL: "tcp://" + s.addr(), Timeout: 2 * time.Second})
					if err != nil {
						return err
					}
					if err := c.Open(); err != nil {
						return fmt.Errorf("cycle %d: open: %w", i, err)
					}
					if err := c.WriteRegister(ctx, 3, 0, uint16(i)); err != nil {
						return fmt.Errorf("cycle %d: write: %w", i, err)
					}
					if _, err := c.ReadHoldingRegisters(ctx, 3, 0, 1); err != nil {
						return fmt.Errorf("cycle %d: read: %w", i, err)
					}
					if err := c.Close(); err != nil {
						return fmt.Errorf("cycle %d: close: %w", i, err)
					}
				}
				return nil
			}()
		}()
	}
	for g := 0; g < goroutines; g++ {
		require.NoError(t, <-errs)
	}
	s.stop(t)
	require.Len(t, s.records(t), goroutines*cycles*2)
	requireNoGoroutineLeak(t, baseline)
}

// The same requests against the same fixture give the same trace, apart from the time
// stamps, the durations and the client's port.
func TestDeterministicTrace(t *testing.T) {
	seed := testSeed(t)
	run := func() []string {
		s := startSim(t, stressFixture(), RunOptions{TraceDecoded: true})
		c := dialClient(t, s.port)
		rng := rand.New(rand.NewSource(seed))
		m := newBlockModel(0)
		for i := 0; i < 300; i++ {
			require.NoError(t, m.step(context.Background(), c, rng))
		}
		require.NoError(t, c.Close())
		s.stop(t)
		var lines []string
		for _, rec := range s.records(t) {
			rec.TS, rec.DurationMs, rec.Conn = "", 0, nil
			line, err := json.Marshal(rec)
			require.NoError(t, err)
			lines = append(lines, string(line))
		}
		return lines
	}
	first, second := run(), run()
	require.Len(t, first, 300)
	require.Equal(t, first, second)
}

func TestRun_ListenAddressInUse(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = ln.Close() }()

	done := make(chan error, 1)
	go func() {
		done <- Run(context.Background(), RunOptions{FixturePath: writeFixture(t, stressFixture()), Listen: ln.Addr().String()}, nil)
	}()
	select {
	case err := <-done:
		require.ErrorContains(t, err, "start modbus server")
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not return although the listen address is in use")
	}
}

func TestRun_TraceFileNotWritable(t *testing.T) {
	err := Run(context.Background(), RunOptions{
		FixturePath: writeFixture(t, stressFixture()),
		Listen:      fmt.Sprintf("127.0.0.1:%d", freePort(t)),
		TracePath:   filepath.Join(t.TempDir(), "missing-dir", "trace.jsonl"),
	}, nil)
	require.ErrorContains(t, err, "create trace file")
	require.ErrorIs(t, err, os.ErrNotExist)
}
