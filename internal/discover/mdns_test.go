package discover

import (
	"encoding/binary"
	"net"
	"testing"
)

func appendRR(b []byte, name []byte, typ uint16, rdata []byte) []byte {
	b = append(b, name...)
	b = binary.BigEndian.AppendUint16(b, typ)
	b = binary.BigEndian.AppendUint16(b, 0x8001) // IN, cache-flush
	b = binary.BigEndian.AppendUint32(b, 120)
	b = binary.BigEndian.AppendUint16(b, uint16(len(rdata)))
	return append(b, rdata...)
}

// An avahi-style answer: PTR in answers, SRV and A in additionals, with
// name compression pointers.
func avahiAnswer() []byte {
	b := make([]byte, 12)
	binary.BigEndian.PutUint16(b[2:], 0x8400)
	binary.BigEndian.PutUint16(b[6:], 1)  // answers
	binary.BigEndian.PutUint16(b[10:], 2) // additionals

	svcOff := len(b)
	svcName := appendName(nil, Service)
	// PTR rdata: "Pixregie on pi" + pointer to the service name.
	instRdataOff := len(b) + len(svcName) + 10
	ptrData := append([]byte{14}, "Pixregie on pi"...)
	ptrData = append(ptrData, 0xC0|byte(svcOff>>8), byte(svcOff))
	b = appendRR(b, svcName, typePTR, ptrData)

	instPtr := []byte{0xC0 | byte(instRdataOff>>8), byte(instRdataOff)}
	srv := []byte{0, 0, 0, 0, 0x1D, 0x0D} // priority, weight, port 7437
	srv = append(srv, appendName(nil, "pi.local.")...)
	srvTargetOff := len(b) + len(instPtr) + 10 + 6
	b = appendRR(b, instPtr, typeSRV, srv)

	tgtPtr := []byte{0xC0 | byte(srvTargetOff>>8), byte(srvTargetOff)}
	b = appendRR(b, tgtPtr, typeA, []byte{192, 168, 1, 50})
	return b
}

func TestParseAvahiAnswer(t *testing.T) {
	url, ok := parseAnswer(avahiAnswer(), net.IPv4(10, 0, 0, 99))
	if !ok || url != "http://192.168.1.50:7437" {
		t.Fatalf("got %q %v", url, ok)
	}
}

func TestParseIgnoresOtherServices(t *testing.T) {
	b := make([]byte, 12)
	binary.BigEndian.PutUint16(b[6:], 1)
	b = appendRR(b, appendName(nil, "_airplay._tcp.local."), typePTR, appendName(nil, "tv._airplay._tcp.local."))
	if _, ok := parseAnswer(b, net.IPv4(10, 0, 0, 2)); ok {
		t.Fatal("should ignore")
	}
}

func TestParseRejectsGarbage(t *testing.T) {
	for _, b := range [][]byte{nil, {1, 2, 3}, append(make([]byte, 12), 0xC0, 0x0C), avahiAnswer()[:40]} {
		parseAnswer(b, nil) // must not panic
	}
	// Pointer loop.
	b := make([]byte, 12)
	binary.BigEndian.PutUint16(b[6:], 1)
	b = append(b, 0xC0, 12)
	if _, _, err := readName(b, 12); err == nil {
		t.Fatal("loop not detected")
	}
}

func TestBuildQuery(t *testing.T) {
	q := buildQuery(1, Service)
	name, off, err := readName(q, 12)
	if err != nil || name != Service || binary.BigEndian.Uint16(q[off:]) != typePTR {
		t.Fatalf("%q %v", name, err)
	}
}
