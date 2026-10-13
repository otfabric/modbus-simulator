package simulator

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/otfabric/go-modbus"
	"github.com/stretchr/testify/require"
)

// tracedHandler returns a handler over the fixture and a function that returns the
// records traced so far.
func tracedHandler(t *testing.T, fixtureJSON string, opts HandlerOptions) (*Handler, func() []TraceRecord) {
	t.Helper()
	f, err := ValidateFixtureBytes([]byte(fixtureJSON))
	require.NoError(t, err)
	store, err := BuildStore(f)
	require.NoError(t, err)
	var buf bytes.Buffer
	h := NewHandler(store, NewJSONLWriter(&buf), opts)
	return h, func() []TraceRecord {
		lines := splitJSONL(buf.String())
		for i, line := range lines {
			require.NoError(t, validateBytesAgainstSchema(TraceSchemaVersion, []byte(line)), "trace line %d", i+1)
		}
		return decodeRecords(t, lines)
	}
}

const twoUnitFixture = `{"schemaVersion":"modbus-simulator.fixture.v1", %s
  "devices":[
    {"unitId":1,"state":{
      "coils":[{"address":0,"value":false}],
      "holdingRegisters":[{"address":4,"value":18},{"address":5,"value":1},{"address":6,"value":2},{"address":7,"value":3,"writable":false}]}},
    {"unitId":2,"state":{
      "holdingRegisters":[{"address":4,"value":99,"writable":false},{"address":5,"value":50}]},
     "behavior":{"exceptions":[{"id":"guard","match":{"fc":23,"address":6},"exceptionCode":"illegal_data_value"}]}}
  ]}`

func TestHandler_MaskWrite(t *testing.T) {
	h, records := tracedHandler(t, fmt.Sprintf(twoUnitFixture, ""), HandlerOptions{TraceRaw: true})
	ctx := context.Background()

	// The example of the specification: 0x12 with AND 0xF2 and OR 0x25 gives 0x17.
	require.NoError(t, h.HandleMaskWrite(ctx, &modbus.MaskWriteRequest{UnitID: 1, FunctionCode: 22, Addr: 4, AndMask: 0xF2, OrMask: 0x25}))
	got, err := readHolding(t, h, 4, 1)
	require.NoError(t, err)
	require.Equal(t, []uint16{0x17}, got)

	// Read-only and unknown registers are refused.
	require.ErrorIs(t, h.HandleMaskWrite(ctx, &modbus.MaskWriteRequest{UnitID: 1, FunctionCode: 22, Addr: 7, AndMask: 0, OrMask: 1}), modbus.ErrIllegalDataAddress)
	require.ErrorIs(t, h.HandleMaskWrite(ctx, &modbus.MaskWriteRequest{UnitID: 1, FunctionCode: 22, Addr: 400, AndMask: 0, OrMask: 1}), modbus.ErrIllegalDataAddress)

	recs := records()
	require.Len(t, recs, 4)
	req := recs[0].Request
	require.Equal(t, "mask_write_register", req.Name)
	require.Equal(t, AreaHoldingRegisters, req.Area)
	require.Equal(t, 0xF2, *req.AndMask)
	require.Equal(t, 0x25, *req.OrMask)
	require.Len(t, recs[0].StateChanges, 1)
	require.EqualValues(t, 0x12, toInt(recs[0].StateChanges[0].Before))
	require.EqualValues(t, 0x17, toInt(recs[0].StateChanges[0].After))
	require.Equal(t, "16000400f20025", recs[0].Raw.Request)
	require.Equal(t, "16000400f20025", recs[0].Raw.Response)
	require.Equal(t, "9602", recs[2].Raw.Response)
}

func TestHandler_ReadWriteRegisters(t *testing.T) {
	h, records := tracedHandler(t, fmt.Sprintf(twoUnitFixture, ""), HandlerOptions{TraceRaw: true})
	ctx := context.Background()

	// The write is made first: the read of 4-7 sees the new 5 and 6.
	got, err := h.HandleReadWriteRegisters(ctx, &modbus.ReadWriteRegistersRequest{
		UnitID: 1, FunctionCode: 23, ReadAddr: 4, ReadQty: 4, WriteAddr: 5, WriteValues: []uint16{11, 12},
	})
	require.NoError(t, err)
	require.Equal(t, []uint16{18, 11, 12, 3}, got)

	// A write that cannot be made changes nothing, and neither does a read that cannot.
	for _, req := range []*modbus.ReadWriteRegistersRequest{
		{UnitID: 1, FunctionCode: 23, ReadAddr: 4, ReadQty: 1, WriteAddr: 6, WriteValues: []uint16{7, 7}}, // 7 is read-only
		{UnitID: 1, FunctionCode: 23, ReadAddr: 4, ReadQty: 9, WriteAddr: 5, WriteValues: []uint16{7}},    // read runs past 7
	} {
		_, err = h.HandleReadWriteRegisters(ctx, req)
		require.ErrorIs(t, err, modbus.ErrIllegalDataAddress)
	}
	got, err = readHolding(t, h, 4, 4)
	require.NoError(t, err)
	require.Equal(t, []uint16{18, 11, 12, 3}, got)

	// A rule on an address matches when the read or the write covers it.
	_, err = h.HandleReadWriteRegisters(ctx, &modbus.ReadWriteRegistersRequest{
		UnitID: 2, FunctionCode: 23, ReadAddr: 5, ReadQty: 1, WriteAddr: 6, WriteValues: []uint16{1},
	})
	require.ErrorIs(t, err, modbus.ErrIllegalDataValue)

	recs := records()
	require.Len(t, recs, 5)
	req := recs[0].Request
	require.Equal(t, "read_write_multiple_registers", req.Name)
	require.EqualValues(t, 4, *req.Address)
	require.EqualValues(t, 4, req.Quantity)
	require.EqualValues(t, 5, *req.WriteAddress)
	require.Equal(t, []any{11.0, 12.0}, req.Values)
	require.Equal(t, []any{18.0, 11.0, 12.0, 3.0}, recs[0].Response.Values)
	require.Len(t, recs[0].StateChanges, 2)
	require.Equal(t, "170004000400050002040"+"00b000c", recs[0].Raw.Request)
	require.Equal(t, "17080012000b000c0003", recs[0].Raw.Response)
	require.Empty(t, recs[1].StateChanges)
	require.Equal(t, "guard", recs[4].MatchedRule)
}

func TestTraceRaw(t *testing.T) {
	h, records := tracedHandler(t, fmt.Sprintf(twoUnitFixture, ""), HandlerOptions{TraceRaw: true})
	ctx := context.Background()
	_, _ = h.HandleHoldingRegisters(ctx, &modbus.HoldingRegistersRequest{UnitID: 1, FunctionCode: 3, Addr: 4, Quantity: 2})
	_, _ = h.HandleCoils(ctx, &modbus.CoilsRequest{UnitID: 1, FunctionCode: 5, Addr: 0, IsWrite: true, Args: []bool{true}})
	_, _ = h.HandleCoils(ctx, &modbus.CoilsRequest{UnitID: 1, FunctionCode: 1, Addr: 0, Quantity: 1})
	_, _ = h.HandleHoldingRegisters(ctx, &modbus.HoldingRegistersRequest{UnitID: 1, FunctionCode: 16, Addr: 5, IsWrite: true, Args: []uint16{0x0102, 0x0304}})
	_, _ = h.HandleCoils(ctx, &modbus.CoilsRequest{UnitID: 1, FunctionCode: 15, Addr: 0, IsWrite: true, Args: []bool{true}})
	_, _ = h.HandleHoldingRegisters(ctx, &modbus.HoldingRegistersRequest{UnitID: 9, FunctionCode: 6, Addr: 5, IsWrite: true, Args: []uint16{7}})
	_, _ = h.HandleDeviceIdentification(ctx, &modbus.DeviceIdentificationRequest{UnitID: 1, MEIType: modbus.MEIReadDeviceIdentification, Category: modbus.DeviceIDBasic})

	want := [][2]string{
		{"0300040002", "030400120001"},
		{"050000ff00", "050000ff00"},
		{"0100000001", "010101"},
		{"100005000204" + "01020304", "1000050002"},
		{"0f0000000101" + "01", "0f00000001"},
		{"0600050007", "8601"},
		{"2b0e0100", "ab01"},
	}
	recs := records()
	require.Len(t, recs, len(want))
	for i, w := range want {
		require.Equal(t, w[0], recs[i].Raw.Request, "request %d", i)
		require.Equal(t, w[1], recs[i].Raw.Response, "response %d", i)
	}

	// Not traced unless asked for.
	h, records = tracedHandler(t, fmt.Sprintf(twoUnitFixture, ""), HandlerOptions{})
	_, _ = h.HandleHoldingRegisters(ctx, &modbus.HoldingRegistersRequest{UnitID: 1, FunctionCode: 3, Addr: 4, Quantity: 2})
	require.Nil(t, records()[0].Raw)
}

func TestHandler_Broadcast(t *testing.T) {
	h, records := tracedHandler(t, fmt.Sprintf(twoUnitFixture, `"broadcast":true,`), HandlerOptions{TraceRaw: true})
	ctx := context.Background()

	// Register 4 is writable on unit 1 and read-only on unit 2; 5 is writable on both.
	_, err := h.HandleHoldingRegisters(ctx, &modbus.HoldingRegistersRequest{UnitID: 0, FunctionCode: 16, Addr: 4, IsWrite: true, Args: []uint16{70, 71}})
	require.ErrorIs(t, err, modbus.ErrNoResponse)
	_, err = h.HandleHoldingRegisters(ctx, &modbus.HoldingRegistersRequest{UnitID: 0, FunctionCode: 6, Addr: 5, IsWrite: true, Args: []uint16{80}})
	require.ErrorIs(t, err, modbus.ErrNoResponse)
	require.ErrorIs(t, h.HandleMaskWrite(ctx, &modbus.MaskWriteRequest{UnitID: 0, FunctionCode: 22, Addr: 5, AndMask: 0xFFFF, OrMask: 0}), modbus.ErrNoResponse)
	// A read cannot be broadcast: nobody answers and nothing happens.
	_, err = h.HandleHoldingRegisters(ctx, &modbus.HoldingRegistersRequest{UnitID: 0, FunctionCode: 3, Addr: 4, Quantity: 1})
	require.ErrorIs(t, err, modbus.ErrNoResponse)

	for unit, want := range map[uint8][]uint16{1: {70, 80}, 2: {99, 80}} {
		got, err := h.HandleHoldingRegisters(ctx, &modbus.HoldingRegistersRequest{UnitID: unit, FunctionCode: 3, Addr: 4, Quantity: 2})
		require.NoError(t, err)
		require.Equal(t, want, got, "unit %d", unit)
	}

	recs := records()
	require.EqualValues(t, 0, recs[0].UnitID)
	require.True(t, recs[0].Response.OK)
	require.True(t, recs[0].Response.NoResponse)
	require.Empty(t, recs[0].Raw.Response)
	// Unit 2 ignored the first write as a whole; both took the second.
	require.Len(t, recs[0].StateChanges, 2)
	require.EqualValues(t, 1, *recs[0].StateChanges[0].UnitID)
	require.Len(t, recs[1].StateChanges, 2)
	require.EqualValues(t, 1, *recs[1].StateChanges[0].UnitID)
	require.EqualValues(t, 2, *recs[1].StateChanges[1].UnitID)
	require.Empty(t, recs[2].StateChanges) // the mask left the value as it was
	require.False(t, recs[3].Response.OK)
	require.True(t, recs[3].Response.NoResponse)
}

func TestHandler_UnitZeroWithoutBroadcast(t *testing.T) {
	h, _ := tracedHandler(t, fmt.Sprintf(twoUnitFixture, ""), HandlerOptions{})
	_, err := h.HandleHoldingRegisters(context.Background(), &modbus.HoldingRegistersRequest{UnitID: 0, FunctionCode: 6, Addr: 5, IsWrite: true, Args: []uint16{80}})
	require.ErrorIs(t, err, modbus.ErrIllegalFunction)
}

// Over TCP a broadcast is executed and left unanswered; the connection stays usable.
func TestE2E_Broadcast(t *testing.T) {
	f := stressFixture()
	f.Broadcast = true
	s := startSim(t, f, RunOptions{})

	conn, err := rawDial(s.addr())
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()
	// A broadcast write of 4660 to register 5, then a read of it on unit 1, in one segment.
	_, err = conn.Write(append(mbapFrame(1, 0, []byte{6, 0, 5, 0x12, 0x34}), mbapFrame(2, 1, readPDU(3, 5, 1))...))
	require.NoError(t, err)
	pdu, err := expectResponse(conn, 2, 1, 3)
	require.NoError(t, err, "the only response must be the one to the read")
	require.Equal(t, []byte{3, 2, 0x12, 0x34}, pdu)

	s.stop(t)
	recs := s.records(t)
	require.Len(t, recs, 2)
	require.True(t, recs[0].Response.NoResponse)
	requireReplayable(t, f, recs)
}

// Clients of a resetOnConnection device each work on a state of their own.
func TestHandler_ResetOnConnection_ClientsAreIsolated(t *testing.T) {
	fx := `{"schemaVersion":"modbus-simulator.fixture.v1","defaults":{"stateMode":"resetOnConnection"},
	  "devices":[{"unitId":1,"state":{"holdingRegisters":[{"address":1,"value":501}],"coils":[{"address":0,"value":false}]}}]}`
	h := buildHandler(t, fx, nil)
	ctx := context.Background()
	read := func(conn string) uint16 {
		got, err := h.HandleHoldingRegisters(ctx, &modbus.HoldingRegistersRequest{UnitID: 1, FunctionCode: 3, Addr: 1, Quantity: 1, ClientAddr: conn})
		require.NoError(t, err)
		return got[0]
	}
	write := func(conn string, v uint16) {
		_, err := h.HandleHoldingRegisters(ctx, &modbus.HoldingRegistersRequest{UnitID: 1, FunctionCode: 6, Addr: 1, IsWrite: true, Args: []uint16{v}, ClientAddr: conn})
		require.NoError(t, err)
	}
	const a, b = "10.0.0.1:1000", "10.0.0.2:2000"

	write(a, 777)
	require.EqualValues(t, 501, read(b), "a new client starts from the fixture values")
	write(b, 888)
	require.EqualValues(t, 777, read(a), "a client that connects later does not reset the others")
	require.EqualValues(t, 888, read(b))
	require.Equal(t, 2, h.store.Sessions())
}

func httpDo(t *testing.T, method, url string) (int, controlReply) {
	t.Helper()
	req, err := http.NewRequest(method, url, nil)
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	var reply controlReply
	if resp.StatusCode != http.StatusMethodNotAllowed {
		require.NoError(t, json.Unmarshal(body, &reply), string(body))
	}
	return resp.StatusCode, reply
}

func TestControlAPI(t *testing.T) {
	control := fmt.Sprintf("127.0.0.1:%d", freePort(t))
	s := startSim(t, stressFixture(), RunOptions{Control: control})
	base := "http://" + control
	ctx := context.Background()
	c := dialClient(t, s.port)
	defer func() { _ = c.Close() }()
	read := func(unit uint8, addr uint16) uint16 {
		got, err := c.ReadHoldingRegister(ctx, unit, addr)
		require.NoError(t, err)
		return got
	}

	status, reply := httpDo(t, http.MethodGet, base+"/healthz")
	require.Equal(t, http.StatusOK, status)
	require.True(t, reply.OK)

	// Reset one unit, then all.
	require.NoError(t, c.WriteRegister(ctx, 1, 3, 4242))
	require.NoError(t, c.WriteRegister(ctx, chaosUnit, 3, 4343))
	status, reply = httpDo(t, http.MethodPost, base+"/reset?unit=1")
	require.Equal(t, http.StatusOK, status)
	require.EqualValues(t, 1, *reply.Unit)
	require.EqualValues(t, 3, read(1, 3))
	require.EqualValues(t, 4343, read(chaosUnit, 3))
	status, _ = httpDo(t, http.MethodPost, base+"/reset")
	require.Equal(t, http.StatusOK, status)
	require.EqualValues(t, 3, read(chaosUnit, 3))

	status, reply = httpDo(t, http.MethodPost, base+"/reset?unit=9")
	require.Equal(t, http.StatusNotFound, status)
	require.Contains(t, reply.Error, "unit 9")
	status, _ = httpDo(t, http.MethodPost, base+"/reset?unit=many")
	require.Equal(t, http.StatusBadRequest, status)
	status, _ = httpDo(t, http.MethodGet, base+"/reset")
	require.Equal(t, http.StatusMethodNotAllowed, status)

	// A fixture that is not valid is refused and the old one keeps being served.
	require.NoError(t, c.WriteRegister(ctx, 1, 3, 4242))
	require.NoError(t, os.WriteFile(s.fixturePath, []byte(`{"schemaVersion":"modbus-simulator.fixture.v1","devices":[]}`), 0o644))
	status, reply = httpDo(t, http.MethodPost, base+"/reload")
	require.Equal(t, http.StatusUnprocessableEntity, status)
	require.Contains(t, reply.Error, "not reloaded")
	require.EqualValues(t, 4242, read(1, 3))

	// A valid one replaces devices and state.
	require.NoError(t, os.WriteFile(s.fixturePath, []byte(`{"schemaVersion":"modbus-simulator.fixture.v1","name":"second",
	  "devices":[{"unitId":7,"state":{"holdingRegisters":[{"address":3,"value":1234}]}}]}`), 0o644))
	status, reply = httpDo(t, http.MethodPost, base+"/reload")
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, "second", reply.Fixture.Name)
	require.EqualValues(t, 1234, read(7, 3))
	_, err := c.ReadHoldingRegister(ctx, 1, 3)
	require.ErrorIs(t, err, modbus.ErrIllegalFunction, "unit 1 is no longer in the fixture")

	s.stop(t)
	requireContiguousSeq(t, s.records(t))
	_, err = http.Get(base + "/healthz")
	require.Error(t, err, "the control API stops with the simulator")
}

func TestRun_ControlAddressInUse(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = ln.Close() }()
	err = Run(context.Background(), RunOptions{
		FixturePath: writeFixture(t, stressFixture()),
		Listen:      fmt.Sprintf("127.0.0.1:%d", freePort(t)),
		Control:     ln.Addr().String(),
	}, nil)
	require.ErrorContains(t, err, "start control API")
}

// tlsMaterial writes a CA, a server certificate for 127.0.0.1 and a client certificate
// to a directory and returns the file paths, the client key pair and the CA pool.
type tlsMaterial struct {
	caFile, certFile, keyFile string
	client                    tls.Certificate
	roots                     *x509.CertPool
}

func newTLSMaterial(t *testing.T) tlsMaterial {
	t.Helper()
	dir := t.TempDir()
	newKey := func() *ecdsa.PrivateKey {
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		require.NoError(t, err)
		return key
	}
	writePEM := func(name, kind string, der []byte) string {
		path := filepath.Join(dir, name)
		require.NoError(t, os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: kind, Bytes: der}), 0o600))
		return path
	}
	template := func(serial int64, cn string) *x509.Certificate {
		return &x509.Certificate{
			SerialNumber: big.NewInt(serial),
			Subject:      pkix.Name{CommonName: cn},
			NotBefore:    time.Now().Add(-time.Hour),
			NotAfter:     time.Now().Add(time.Hour),
			KeyUsage:     x509.KeyUsageDigitalSignature,
		}
	}

	caKey := newKey()
	caTmpl := template(1, "test CA")
	caTmpl.IsCA, caTmpl.BasicConstraintsValid, caTmpl.KeyUsage = true, true, x509.KeyUsageCertSign
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	require.NoError(t, err)
	caCert, err := x509.ParseCertificate(caDER)
	require.NoError(t, err)

	issue := func(serial int64, cn string, usage x509.ExtKeyUsage) (certDER, keyDER []byte) {
		key := newKey()
		tmpl := template(serial, cn)
		tmpl.ExtKeyUsage = []x509.ExtKeyUsage{usage}
		tmpl.IPAddresses = []net.IP{net.ParseIP("127.0.0.1")}
		certDER, err := x509.CreateCertificate(rand.Reader, tmpl, caCert, &key.PublicKey, caKey)
		require.NoError(t, err)
		keyDER, err = x509.MarshalECPrivateKey(key)
		require.NoError(t, err)
		return certDER, keyDER
	}
	serverCert, serverKey := issue(2, "simulator", x509.ExtKeyUsageServerAuth)
	clientCert, clientKey := issue(3, "gateway", x509.ExtKeyUsageClientAuth)

	m := tlsMaterial{
		caFile:   writePEM("ca.pem", "CERTIFICATE", caDER),
		certFile: writePEM("server.pem", "CERTIFICATE", serverCert),
		keyFile:  writePEM("server.key", "EC PRIVATE KEY", serverKey),
		roots:    x509.NewCertPool(),
	}
	m.roots.AddCert(caCert)
	m.client, err = tls.X509KeyPair(
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: clientCert}),
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: clientKey}))
	require.NoError(t, err)
	return m
}

func TestE2E_TLS(t *testing.T) {
	m := newTLSMaterial(t)
	s := startSim(t, stressFixture(), RunOptions{TLSCert: m.certFile, TLSKey: m.keyFile, TLSClientCA: m.caFile})
	ctx := context.Background()

	var c *modbus.Client
	require.Eventually(t, func() bool {
		var err error
		c, err = modbus.New(modbus.Config{
			URL: "tcp+tls://" + s.addr(), Timeout: 2 * time.Second, TLSClientCert: &m.client, TLSRootCAs: m.roots,
		})
		require.NoError(t, err)
		return c.Open() == nil
	}, 3*time.Second, 20*time.Millisecond)
	require.NoError(t, c.WriteRegister(ctx, 1, 3, 4242))
	got, err := c.ReadHoldingRegister(ctx, 1, 3)
	require.NoError(t, err)
	require.EqualValues(t, 4242, got)
	require.NoError(t, c.Close())

	// A client that does not speak TLS gets nothing.
	plain, err := modbus.New(modbus.Config{URL: "tcp://" + s.addr(), Timeout: 300 * time.Millisecond})
	require.NoError(t, err)
	if plain.Open() == nil {
		_, err = plain.ReadHoldingRegister(ctx, 1, 3)
		require.Error(t, err)
		_ = plain.Close()
	}

	s.stop(t)
	recs := s.records(t)
	require.Len(t, recs, 2)
	require.Equal(t, "tcp+tls", recs[0].Conn.Transport)
}

func TestRun_TLSOptions(t *testing.T) {
	m := newTLSMaterial(t)
	base := RunOptions{FixturePath: writeFixture(t, stressFixture()), Listen: fmt.Sprintf("127.0.0.1:%d", freePort(t))}
	for name, tc := range map[string]struct {
		cert, key, ca string
		want          string
	}{
		"incomplete":     {m.certFile, m.keyFile, "", "a certificate, a key and a client CA"},
		"missing cert":   {filepath.Join(t.TempDir(), "none.pem"), m.keyFile, m.caFile, "load TLS certificate"},
		"key as CA":      {m.certFile, m.keyFile, m.keyFile, "no certificate in"},
		"cert/key mixed": {m.caFile, m.keyFile, m.caFile, "load TLS certificate"},
	} {
		opts := base
		opts.TLSCert, opts.TLSKey, opts.TLSClientCA = tc.cert, tc.key, tc.ca
		require.ErrorContains(t, Run(context.Background(), opts, nil), tc.want, name)
	}
}

// Timeout and connectionClose faults do on the wire what their names say, for every
// function code: no response with the connection kept, and a connection closed without
// a response.
func TestE2E_FaultsOnTheWire(t *testing.T) {
	const fixture = `{"schemaVersion":"modbus-simulator.fixture.v1","devices":[{"unitId":1,
	  "identity":{"vendorName":"v","productCode":"p","majorMinorRevision":"1"},
	  "state":{"coils":[{"address":0,"value":false}],"discreteInputs":[{"address":0,"value":true}],
	    "holdingRegisters":[{"address":0,"value":1},{"address":1,"value":2}],"inputRegisters":[{"address":0,"value":3}]},
	  "behavior":{"faults":[%s]}}]}`
	requests := map[string][]byte{
		"fc01": readPDU(1, 0, 1), "fc02": readPDU(2, 0, 1), "fc03": readPDU(3, 0, 1), "fc04": readPDU(4, 0, 1),
		"fc05": {5, 0, 0, 0xFF, 0}, "fc06": {6, 0, 0, 0, 9},
		"fc15": {15, 0, 0, 0, 1, 1, 1}, "fc16": {16, 0, 0, 0, 1, 2, 0, 9},
		"fc22": {22, 0, 0, 0xFF, 0xFF, 0, 0}, "fc23": {23, 0, 0, 0, 1, 0, 1, 0, 1, 2, 0, 9},
		"fc43": {0x2B, 0x0E, 1, 0},
	}
	start := func(t *testing.T, rule string) *sim {
		f, err := ValidateFixtureBytes([]byte(fmt.Sprintf(fixture, rule)))
		require.NoError(t, err)
		return startSim(t, f, RunOptions{TraceRaw: true})
	}

	t.Run("timeout", func(t *testing.T) {
		s := start(t, `{"id":"silent","match":{},"fault":"timeout","delayMs":20}`)
		for name, pdu := range requests {
			conn, err := rawDial(s.addr())
			require.NoError(t, err)
			began := time.Now()
			_, err = conn.Write(mbapFrame(1, 1, pdu))
			require.NoError(t, err)
			_ = conn.SetReadDeadline(time.Now().Add(250 * time.Millisecond))
			n, err := conn.Read(make([]byte, 16))
			require.Zero(t, n, "%s: a response was sent", name)
			require.True(t, isTimeout(err), "%s: connection not kept open: %v", name, err)
			require.GreaterOrEqual(t, time.Since(began), 20*time.Millisecond)
			require.NoError(t, conn.Close())
		}
		s.stop(t)
		recs := s.records(t)
		require.Len(t, recs, len(requests))
		for _, rec := range recs {
			require.Equal(t, FaultTimeout, rec.Response.Fault)
			require.Equal(t, "silent", rec.MatchedRule)
			require.Empty(t, rec.Raw.Response)
		}
	})

	t.Run("connectionClose", func(t *testing.T) {
		s := start(t, `{"id":"hangup","match":{},"fault":"connectionClose"}`)
		for name, pdu := range requests {
			conn, err := rawDial(s.addr())
			require.NoError(t, err)
			_, err = conn.Write(mbapFrame(1, 1, pdu))
			require.NoError(t, err)
			n, err := io.Copy(io.Discard, conn)
			require.Zero(t, n, "%s: a response was sent", name)
			require.False(t, isTimeout(err), "%s: connection left open", name)
			require.NoError(t, conn.Close())
		}
		s.stop(t)
		recs := s.records(t)
		require.Len(t, recs, len(requests))
		for _, rec := range recs {
			require.Equal(t, FaultConnectionClose, rec.Response.Fault)
			require.Empty(t, rec.Raw.Response)
		}
	})

	// A go-modbus client sees a timeout, and a closed connection, as such.
	t.Run("client view", func(t *testing.T) {
		s := start(t, `{"match":{"address":0,"fc":3},"fault":"timeout"},{"match":{"address":1,"fc":3},"fault":"connectionClose"}`)
		c, err := modbus.New(modbus.Config{URL: "tcp://" + s.addr(), Timeout: 150 * time.Millisecond})
		require.NoError(t, err)
		require.NoError(t, c.Open())
		defer func() { _ = c.Close() }()
		ctx := context.Background()

		_, err = c.ReadHoldingRegisters(ctx, 1, 0, 1)
		require.ErrorIs(t, err, modbus.ErrRequestTimedOut)
		_, err = c.ReadHoldingRegisters(ctx, 1, 1, 1)
		require.Error(t, err)
		require.NotErrorIs(t, err, modbus.ErrRequestTimedOut)
		require.NotErrorIs(t, err, modbus.ErrServerDeviceFailure)
		// The simulator keeps serving.
		got, err := c.ReadInputRegisters(ctx, 1, 0, 1)
		require.NoError(t, err)
		require.Equal(t, []uint16{3}, got)
	})
}
