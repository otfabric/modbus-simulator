package simulator

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// monkey is a misbehaving Modbus/TCP client. Each action opens a connection of its own
// and returns an error only when the simulator breaks a rule: a complete request must be
// answered with a response that belongs to it, or the connection must be closed.
type monkey struct {
	addr string
	rng  *rand.Rand
	txn  uint16
}

type monkeyAction struct {
	name string
	run  func(m *monkey) error
}

var monkeyActions = []monkeyAction{
	{"garbage", (*monkey).garbage},
	{"truncated frame", (*monkey).truncatedFrame},
	{"bad length field", (*monkey).badLengthField},
	{"random pdu", (*monkey).randomPDU},
	{"illegal quantity", (*monkey).illegalQuantity},
	{"address overflow", (*monkey).addressOverflow},
	{"wrong byte count", (*monkey).wrongByteCount},
	{"slow loris", (*monkey).slowLoris},
	{"pipelining", (*monkey).pipelining},
	{"connect and leave", (*monkey).connectAndLeave},
	{"leave during delay", (*monkey).leaveDuringDelay},
	{"write", (*monkey).write},
	{"mask write", (*monkey).maskWrite},
	{"timeout fault", (*monkey).timeoutFault},
	{"connection close fault", (*monkey).connectionCloseFault},
	{"read/write", (*monkey).readWrite},
	{"unknown unit", (*monkey).unknownUnit},
	{"exception rule", (*monkey).exceptionRule},
	{"device identification", (*monkey).deviceIdentification},
}

func (m *monkey) act() error {
	a := monkeyActions[m.rng.Intn(len(monkeyActions))]
	if err := a.run(m); err != nil {
		return fmt.Errorf("%s: %w", a.name, err)
	}
	return nil
}

func (m *monkey) nextTxn() uint16 {
	m.txn++
	return m.txn
}

// exchange sends one complete request on a new connection and returns the response PDU,
// or nil when the server closed the connection instead. Leaving the request unanswered
// is an error.
func (m *monkey) exchange(unit byte, pdu []byte) ([]byte, error) {
	conn, err := rawDial(m.addr)
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close() }()
	txn := m.nextTxn()
	if _, err := conn.Write(mbapFrame(txn, unit, pdu)); err != nil {
		return nil, nil // closed by the server
	}
	resp, err := expectResponse(conn, txn, unit, pdu[0])
	var v violation
	switch {
	case err == nil:
		return resp, nil
	case errors.As(err, &v):
		return nil, fmt.Errorf("%w (request % x)", err, pdu)
	case isTimeout(err):
		return nil, fmt.Errorf("no response and connection left open (request % x)", pdu)
	default:
		return nil, nil // closed by the server
	}
}

// exchangeException is exchange for a request that must be refused with an exception.
func (m *monkey) exchangeException(unit byte, pdu []byte, code byte) error {
	resp, err := m.exchange(unit, pdu)
	if err != nil {
		return err
	}
	if resp == nil {
		return fmt.Errorf("connection closed, want exception %d (request % x)", code, pdu)
	}
	if !isException(resp, code) {
		return fmt.Errorf("response % x, want exception %d (request % x)", resp, code, pdu)
	}
	return nil
}

func (m *monkey) randomBytes(n int) []byte {
	b := make([]byte, n)
	m.rng.Read(b)
	return b
}

func (m *monkey) garbage() error {
	conn, err := rawDial(m.addr)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	b := m.randomBytes(1 + m.rng.Intn(300))
	if len(b) >= 4 {
		b[3] |= 1 // never a Modbus protocol id, so never a request a worker's model would miss
	}
	_, _ = conn.Write(b)
	_ = conn.SetReadDeadline(time.Now().Add(20 * time.Millisecond))
	_, _ = io.Copy(io.Discard, conn)
	return nil
}

func (m *monkey) truncatedFrame() error {
	conn, err := rawDial(m.addr)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	frame := mbapFrame(m.nextTxn(), chaosUnit, readPDU(3, 0, 10))
	_, _ = conn.Write(frame[:1+m.rng.Intn(len(frame)-1)])
	if m.rng.Intn(2) == 0 {
		time.Sleep(time.Duration(m.rng.Intn(20)) * time.Millisecond)
	}
	return nil
}

func (m *monkey) badLengthField() error {
	conn, err := rawDial(m.addr)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	frame := mbapFrame(m.nextTxn(), chaosUnit, readPDU(3, 0, 10))
	lengths := []uint16{0, 1, 255, 300, 65535}
	binary.BigEndian.PutUint16(frame[4:], lengths[m.rng.Intn(len(lengths))])
	_, _ = conn.Write(frame)
	_ = conn.SetReadDeadline(time.Now().Add(20 * time.Millisecond))
	_, _ = io.Copy(io.Discard, conn)
	return nil
}

func (m *monkey) randomPDU() error {
	pdu := m.randomBytes(1 + m.rng.Intn(24))
	unit := byte(chaosUnit)
	if m.rng.Intn(4) == 0 {
		unit = 9
	}
	_, err := m.exchange(unit, pdu)
	return err
}

func (m *monkey) illegalQuantity() error {
	fc := byte(1 + m.rng.Intn(4))
	quantities := []uint16{0, 126, 65535}
	if fc <= 2 {
		quantities = []uint16{0, 2001, 65535}
	}
	return m.exchangeException(chaosUnit, readPDU(fc, uint16(m.rng.Intn(10)), quantities[m.rng.Intn(3)]), 3)
}

// A request that runs past address 65535 is refused, also by a unit that answers reads of
// unknown addresses with zeroes.
func (m *monkey) addressOverflow() error {
	fc := byte(1 + m.rng.Intn(4))
	back := uint16(m.rng.Intn(8))
	return m.exchangeException(chaosUnit, readPDU(fc, 65535-back, back+2+uint16(m.rng.Intn(8))), 2)
}

func (m *monkey) wrongByteCount() error {
	pdu := []byte{16, 0, 0, 0, 2, 3, 0, 1, 0, 2}
	return m.exchangeException(chaosUnit, pdu, 3)
}

func (m *monkey) slowLoris() error {
	conn, err := rawDial(m.addr)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	txn := m.nextTxn()
	for _, b := range mbapFrame(txn, chaosUnit, readPDU(4, 500, 10)) {
		if _, err := conn.Write([]byte{b}); err != nil {
			return err
		}
		time.Sleep(time.Millisecond)
	}
	resp, err := expectResponse(conn, txn, chaosUnit, 4)
	if err != nil {
		return err
	}
	if len(resp) != 22 || resp[1] != 20 {
		return fmt.Errorf("response % x, want 10 registers", resp)
	}
	return nil
}

func (m *monkey) pipelining() error {
	conn, err := rawDial(m.addr)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	n := 2 + m.rng.Intn(5)
	first := m.txn + 1
	var batch []byte
	for i := 0; i < n; i++ {
		batch = append(batch, mbapFrame(m.nextTxn(), chaosUnit, readPDU(2, uint16(i), 3))...)
	}
	if _, err := conn.Write(batch); err != nil {
		return err
	}
	for i := 0; i < n; i++ {
		resp, err := expectResponse(conn, first+uint16(i), chaosUnit, 2)
		if err != nil {
			return fmt.Errorf("response %d of %d: %w", i+1, n, err)
		}
		if len(resp) != 3 {
			return fmt.Errorf("response %d of %d: % x", i+1, n, resp)
		}
	}
	return nil
}

func (m *monkey) connectAndLeave() error {
	conn, err := rawDial(m.addr)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	if m.rng.Intn(2) == 0 {
		return nil
	}
	// Half-close: the server must hang up too, not keep the connection.
	if err := conn.CloseWrite(); err != nil {
		return nil
	}
	if _, err := io.Copy(io.Discard, conn); isTimeout(err) {
		return errors.New("connection kept open after the client shut down its side")
	}
	return nil
}

func (m *monkey) leaveDuringDelay() error {
	conn, err := rawDial(m.addr)
	if err != nil {
		return err
	}
	_, _ = conn.Write(mbapFrame(m.nextTxn(), chaosUnit, readPDU(3, chaosDelayAddr, 1)))
	time.Sleep(time.Duration(m.rng.Intn(chaosDelayMs)) * time.Millisecond)
	return conn.Close()
}

func (m *monkey) write() error {
	pdu := []byte{6, 0, byte(m.rng.Intn(10)), byte(m.rng.Intn(256)), byte(m.rng.Intn(256))}
	if m.rng.Intn(2) == 0 {
		pdu = []byte{5, 0, byte(m.rng.Intn(10)), byte(0xFF * m.rng.Intn(2)), 0}
	}
	resp, err := m.exchange(chaosUnit, pdu)
	if err != nil {
		return err
	}
	if string(resp) != string(pdu) {
		return fmt.Errorf("response % x, want the echo of % x", resp, pdu)
	}
	return nil
}

// A timeout fault leaves its request unanswered; the next one on the connection is served.
func (m *monkey) timeoutFault() error {
	conn, err := rawDial(m.addr)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	silent, next := m.nextTxn(), m.nextTxn()
	batch := append(mbapFrame(silent, chaosUnit, readPDU(chaosFaultFC, chaosTimeoutAddr, chaosFaultQty)),
		mbapFrame(next, chaosUnit, readPDU(2, 0, 3))...)
	if _, err := conn.Write(batch); err != nil {
		return err
	}
	if _, err := expectResponse(conn, next, chaosUnit, 2); err != nil {
		return fmt.Errorf("the only response must be the one to the request after the fault: %w", err)
	}
	return nil
}

// A connectionClose fault hangs up without a byte.
func (m *monkey) connectionCloseFault() error {
	conn, err := rawDial(m.addr)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	if _, err := conn.Write(mbapFrame(m.nextTxn(), chaosUnit, readPDU(chaosFaultFC, chaosConnCloseAddr, chaosFaultQty))); err != nil {
		return err
	}
	n, err := io.Copy(io.Discard, conn)
	if isTimeout(err) {
		return errors.New("connection left open")
	}
	if n != 0 {
		return fmt.Errorf("%d bytes sent before the connection was closed", n)
	}
	return nil
}

func (m *monkey) maskWrite() error {
	pdu := append([]byte{22, 0, byte(m.rng.Intn(10))}, m.randomBytes(4)...)
	resp, err := m.exchange(chaosUnit, pdu)
	if err != nil {
		return err
	}
	if string(resp) != string(pdu) {
		return fmt.Errorf("response % x, want the echo of % x", resp, pdu)
	}
	return nil
}

func (m *monkey) readWrite() error {
	// Read 0-9, write two registers somewhere in 0-8.
	pdu := append([]byte{23, 0, 0, 0, 10, 0, byte(m.rng.Intn(9)), 0, 2, 4}, m.randomBytes(4)...)
	resp, err := m.exchange(chaosUnit, pdu)
	if err != nil {
		return err
	}
	if len(resp) != 22 || resp[0] != 23 || resp[1] != 20 {
		return fmt.Errorf("response % x, want 10 registers", resp)
	}
	return nil
}

// Units 0 and 255 are outside the range a fixture can configure, and still traced.
func (m *monkey) unknownUnit() error {
	units := []byte{0, 9, 247, 255}
	return m.exchangeException(units[m.rng.Intn(len(units))], readPDU(3, 0, 1), 1)
}

func (m *monkey) exceptionRule() error {
	return m.exchangeException(chaosUnit, readPDU(3, chaosExcAddr, 1), 4)
}

func (m *monkey) deviceIdentification() error {
	resp, err := m.exchange(chaosUnit, []byte{0x2B, 0x0E, 0x01, 0x00})
	if err != nil {
		return err
	}
	if len(resp) < 8 || resp[0] != 0x2B {
		return fmt.Errorf("response % x", resp)
	}
	return nil
}

// Every monkey action on its own, so that a broken rule is reported by name.
func TestChaos_EachAction(t *testing.T) {
	seed := testSeed(t)
	f := stressFixture()
	s := startSim(t, f, RunOptions{})
	for i, a := range monkeyActions {
		t.Run(a.name, func(t *testing.T) {
			m := &monkey{addr: s.addr(), rng: rand.New(rand.NewSource(seed + int64(i)))}
			for n := 0; n < 20; n++ {
				require.NoError(t, a.run(m))
			}
		})
	}
	s.stop(t)
	requireReplayable(t, f, s.records(t))
}

// Monkeys misbehave on unit 2 while well-behaved clients work on unit 1. The clients must
// not notice: every answer matches their model. Afterwards the simulator still serves,
// the trace is valid and replays as a sequential history, and nothing is left running.
func TestChaos_MonkeysDoNotDisturbClients(t *testing.T) {
	seed := testSeed(t)
	soakRounds(t, func(t *testing.T, n int) {
		const monkeys, minActions, ops = 6, 40, 200
		seed := seed + int64(n)*1000
		baseline := runtime.NumGoroutine()
		f := stressFixture()
		s := startSim(t, f, RunOptions{})

		stop := make(chan struct{})
		errs := make([]error, monkeys)
		var wg sync.WaitGroup
		for i := 0; i < monkeys; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				m := &monkey{addr: s.addr(), rng: rand.New(rand.NewSource(seed + 100 + int64(i)))}
				for done := 0; ; done++ {
					select {
					case <-stop:
						if done >= minActions {
							return
						}
					default:
					}
					if err := m.act(); err != nil {
						errs[i] = fmt.Errorf("monkey %d, action %d: %w", i, done, err)
						return
					}
				}
			}(i)
		}
		var once sync.Once
		stopMonkeys := func() { once.Do(func() { close(stop) }); wg.Wait() }
		defer stopMonkeys()

		models := runWorkers(t, s, seed, ops)
		stopMonkeys()
		for _, err := range errs {
			require.NoError(t, err)
		}

		// Still serving.
		c := dialClient(t, s.port)
		got, err := c.ReadInputRegisters(context.Background(), 1, 0, 2)
		require.NoError(t, err)
		require.Equal(t, []uint16{1000, 1001}, got)
		require.NoError(t, c.Close())

		s.stop(t)
		rp := requireReplayable(t, f, s.records(t))
		for _, m := range models {
			m.requireMatchesReplay(t, rp)
		}
		requireNoGoroutineLeak(t, baseline)
	})
}
