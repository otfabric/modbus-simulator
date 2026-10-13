package simulator

import (
	"encoding/binary"
	"encoding/hex"
)

// buildTraceRaw encodes the PDUs of a transaction for the trace.
func buildTraceRaw(r reqInfo, res txResult) *TraceRaw {
	raw := &TraceRaw{Request: hex.EncodeToString(requestPDU(r))}
	if pdu := responsePDU(r, res); pdu != nil {
		raw.Response = hex.EncodeToString(pdu)
	}
	return raw
}

func appendUint16s(pdu []byte, vals ...uint16) []byte {
	for _, v := range vals {
		pdu = binary.BigEndian.AppendUint16(pdu, v)
	}
	return pdu
}

// packBits packs coils as the specification prescribes: the first coil in the least
// significant bit of the first byte, unused bits zero.
func packBits(bits []bool) []byte {
	out := make([]byte, (len(bits)+7)/8)
	for i, b := range bits {
		if b {
			out[i/8] |= 1 << (i % 8)
		}
	}
	return out
}

func coilValue(v []bool) uint16 {
	if len(v) > 0 && v[0] {
		return 0xFF00
	}
	return 0
}

func firstReg(v []uint16) uint16 {
	if len(v) == 0 {
		return 0
	}
	return v[0]
}

func requestPDU(r reqInfo) []byte {
	pdu := []byte{r.fc}
	switch r.fc {
	case 5:
		return appendUint16s(pdu, r.address, coilValue(r.coilValues))
	case 6:
		return appendUint16s(pdu, r.address, firstReg(r.regValues))
	case 15:
		bits := packBits(r.coilValues)
		pdu = appendUint16s(pdu, r.address, uint16(len(r.coilValues)))
		return append(append(pdu, byte(len(bits))), bits...)
	case 16:
		pdu = appendUint16s(pdu, r.address, uint16(len(r.regValues)))
		return appendUint16s(append(pdu, byte(2*len(r.regValues))), r.regValues...)
	case fcMaskWrite:
		return appendUint16s(pdu, r.address, r.andMask, r.orMask)
	case fcReadWrite:
		pdu = appendUint16s(pdu, r.address, r.quantity, r.writeAddr, uint16(len(r.regValues)))
		return appendUint16s(append(pdu, byte(2*len(r.regValues))), r.regValues...)
	case fcIdentity:
		return append(pdu, byte(r.mei), byte(r.category), byte(r.objectID))
	default: // FC01-FC04
		return appendUint16s(pdu, r.address, r.quantity)
	}
}

// exceptionByte is the exception a failed request is answered with. A delay fault that
// was cut short is sent as a server device failure.
func exceptionByte(res txResult) byte {
	switch res.exceptionCode {
	case ExcIllegalFunction:
		return 0x01
	case ExcIllegalDataAddress:
		return 0x02
	case ExcIllegalDataValue:
		return 0x03
	default:
		return 0x04
	}
}

// responsePDU returns nil when no response is sent and for FC43.
func responsePDU(r reqInfo, res txResult) []byte {
	if res.noResponse || res.fault == FaultTimeout || res.fault == FaultConnectionClose {
		return nil
	}
	if !res.ok {
		return []byte{r.fc | 0x80, exceptionByte(res)}
	}
	pdu := []byte{r.fc}
	switch r.fc {
	case 1, 2:
		bits := packBits(res.bools)
		return append(append(pdu, byte(len(bits))), bits...)
	case 3, 4, fcReadWrite:
		return appendUint16s(append(pdu, byte(2*len(res.regs))), res.regs...)
	case 15:
		return appendUint16s(pdu, r.address, uint16(len(r.coilValues)))
	case 16:
		return appendUint16s(pdu, r.address, uint16(len(r.regValues)))
	case fcIdentity:
		return nil
	default: // FC05, FC06 and FC22 echo the request
		return requestPDU(r)
	}
}
