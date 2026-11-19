// Package discover finds the Pixregie display on the local network by
// asking mDNS for the _pixregie._tcp service, so agents need no address.
//
// It sends one "legacy unicast" query (RFC 6762 §6.7): from an ordinary UDP
// port, so responders answer us directly and no multicast membership or
// extra dependency is needed.
package discover

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"
)

const Service = "_pixregie._tcp.local."

// Find returns the first display that answers (https://ip:port).
func Find(ctx context.Context, timeout time.Duration) (string, error) {
	all, err := find(ctx, timeout, true)
	if err != nil {
		return "", err
	}
	return all[0], nil
}

// FindAll returns every display that answered within the timeout (several
// boards can share a network).
func FindAll(ctx context.Context, timeout time.Duration) ([]string, error) {
	return find(ctx, timeout, false)
}

func find(ctx context.Context, timeout time.Duration, first bool) ([]string, error) {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{})
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	dst := &net.UDPAddr{IP: net.IPv4(224, 0, 0, 251), Port: 5353}
	q := buildQuery(0x5058, Service)

	deadline := time.Now().Add(timeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	buf := make([]byte, 9000)
	var found []string
	seen := map[string]bool{}
	// Ask a few times: the first packet is sometimes lost on Wi-Fi.
	for attempt := 0; time.Now().Before(deadline); attempt++ {
		if attempt < 3 {
			conn.WriteToUDP(q, dst)
		}
		conn.SetReadDeadline(minTime(deadline, time.Now().Add(700*time.Millisecond)))
		n, from, err := conn.ReadFromUDP(buf)
		if err != nil {
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				if ctx.Err() != nil {
					break
				}
				continue
			}
			return nil, err
		}
		if url, ok := parseAnswer(buf[:n], from.IP); ok && !seen[url] {
			seen[url] = true
			found = append(found, url)
			if first {
				return found, nil
			}
		}
	}
	if len(found) == 0 {
		return nil, errors.New("no Pixregie display found on the network")
	}
	return found, nil
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

const (
	typeA   = 1
	typePTR = 12
	typeSRV = 33
)

func appendName(b []byte, name string) []byte {
	for _, label := range strings.Split(strings.TrimSuffix(name, "."), ".") {
		b = append(b, byte(len(label)))
		b = append(b, label...)
	}
	return append(b, 0)
}

func buildQuery(id uint16, name string) []byte {
	b := make([]byte, 12)
	binary.BigEndian.PutUint16(b[0:], id)
	binary.BigEndian.PutUint16(b[4:], 1) // one question
	b = appendName(b, name)
	return binary.BigEndian.AppendUint32(b, typePTR<<16|1) // PTR, class IN
}

// readName decodes a possibly compressed name starting at off and returns
// it with the offset just after it in the original stream.
func readName(msg []byte, off int) (string, int, error) {
	var labels []string
	end := -1
	for hops := 0; hops < 64; hops++ {
		if off >= len(msg) {
			return "", 0, errors.New("name out of bounds")
		}
		l := int(msg[off])
		switch {
		case l == 0:
			if end < 0 {
				end = off + 1
			}
			return strings.Join(labels, ".") + ".", end, nil
		case l&0xC0 == 0xC0:
			if off+1 >= len(msg) {
				return "", 0, errors.New("bad pointer")
			}
			if end < 0 {
				end = off + 2
			}
			off = int(binary.BigEndian.Uint16(msg[off:]) & 0x3FFF)
		default:
			if off+1+l > len(msg) {
				return "", 0, errors.New("label out of bounds")
			}
			labels = append(labels, string(msg[off+1:off+1+l]))
			off += 1 + l
		}
	}
	return "", 0, errors.New("name loop")
}

type rr struct {
	name  string
	typ   uint16
	data  []byte
	start int // offset of data in msg, for names inside rdata
}

func parseRRs(msg []byte) ([]rr, error) {
	if len(msg) < 12 {
		return nil, errors.New("short message")
	}
	qd := int(binary.BigEndian.Uint16(msg[4:]))
	total := int(binary.BigEndian.Uint16(msg[6:])) + int(binary.BigEndian.Uint16(msg[8:])) + int(binary.BigEndian.Uint16(msg[10:]))
	off := 12
	for i := 0; i < qd; i++ {
		_, n, err := readName(msg, off)
		if err != nil {
			return nil, err
		}
		off = n + 4
	}
	var out []rr
	for i := 0; i < total; i++ {
		name, n, err := readName(msg, off)
		if err != nil {
			return nil, err
		}
		if n+10 > len(msg) {
			return nil, errors.New("rr out of bounds")
		}
		typ := binary.BigEndian.Uint16(msg[n:])
		rdlen := int(binary.BigEndian.Uint16(msg[n+8:]))
		start := n + 10
		if start+rdlen > len(msg) {
			return nil, errors.New("rdata out of bounds")
		}
		out = append(out, rr{strings.ToLower(name), typ, msg[start : start+rdlen], start})
		off = start + rdlen
	}
	return out, nil
}

// parseAnswer extracts https://ip:port from a response. from is the
// responder's address, used when it sent no A record.
func parseAnswer(msg []byte, from net.IP) (string, bool) {
	rrs, err := parseRRs(msg)
	if err != nil {
		return "", false
	}
	var instance string
	for _, r := range rrs {
		if r.typ == typePTR && r.name == Service {
			instance, _, _ = readName(msg, r.start)
			instance = strings.ToLower(instance)
		}
	}
	if instance == "" {
		return "", false
	}
	var target string
	var port uint16
	for _, r := range rrs {
		if r.typ == typeSRV && r.name == instance && len(r.data) >= 7 {
			port = binary.BigEndian.Uint16(r.data[4:])
			target, _, _ = readName(msg, r.start+6)
			target = strings.ToLower(target)
		}
	}
	if port == 0 {
		return "", false
	}
	ip := from
	for _, r := range rrs {
		if r.typ == typeA && r.name == target && len(r.data) == 4 {
			ip = net.IP(r.data)
		}
	}
	if ip == nil || ip.To4() == nil {
		return "", false
	}
	return fmt.Sprintf("https://%s:%d", ip.To4(), port), true
}
