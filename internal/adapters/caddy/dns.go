package caddy

import (
	"context"
	"encoding/binary"
	"errors"
	"net"
	"strings"
)

// A minimal DNS responder for DNSEdge: it answers A queries for the names
// it knows with the addresses lookup returns, AAAA queries with no
// answers, and everything else with NXDOMAIN. Answers carry a one-second
// TTL. It serves only OAP's own network.

const (
	typeA    = 1
	typeAAAA = 28
	classIN  = 1
)

// question is a parsed DNS query.
type question struct {
	id    uint16
	flags uint16
	name  string // lower case, with the trailing dot
	qtype uint16
	raw   []byte // the question section, echoed in the answer
}

var errBadQuery = errors.New("caddy: malformed DNS query")

func parseQuery(b []byte) (question, error) {
	var q question
	if len(b) < 12 {
		return q, errBadQuery
	}
	q.id = binary.BigEndian.Uint16(b[0:2])
	q.flags = binary.BigEndian.Uint16(b[2:4])
	if q.flags&0x8000 != 0 || binary.BigEndian.Uint16(b[4:6]) != 1 {
		return q, errBadQuery // a response, or not exactly one question
	}
	var labels []string
	i := 12
	for {
		if i >= len(b) {
			return q, errBadQuery
		}
		n := int(b[i])
		i++
		if n == 0 {
			break
		}
		if n > 63 || i+n > len(b) {
			return q, errBadQuery
		}
		labels = append(labels, strings.ToLower(string(b[i:i+n])))
		i += n
	}
	if i+4 > len(b) {
		return q, errBadQuery
	}
	q.qtype = binary.BigEndian.Uint16(b[i : i+2])
	q.name = strings.Join(labels, ".") + "."
	q.raw = b[12 : i+4]
	return q, nil
}

// answer builds the response to q with the given IPv4 addresses; known
// says whether the name exists.
func answer(q question, known bool, ips []net.IP) []byte {
	var a []net.IP
	if known && q.qtype == typeA {
		for _, ip := range ips {
			if v4 := ip.To4(); v4 != nil {
				a = append(a, v4)
			}
		}
	}
	out := make([]byte, 12, 12+len(q.raw)+16*len(a))
	binary.BigEndian.PutUint16(out[0:2], q.id)
	flags := uint16(0x8000 | 0x0400) // response, authoritative
	flags |= q.flags & 0x0100        // recursion desired, echoed
	if !known {
		flags |= 3 // NXDOMAIN
	}
	binary.BigEndian.PutUint16(out[2:4], flags)
	binary.BigEndian.PutUint16(out[4:6], 1)
	binary.BigEndian.PutUint16(out[6:8], uint16(len(a)))
	out = append(out, q.raw...)
	for _, ip := range a {
		out = append(out, 0xC0, 0x0C) // the name, by pointer to the question
		out = binary.BigEndian.AppendUint16(out, typeA)
		out = binary.BigEndian.AppendUint16(out, classIN)
		out = binary.BigEndian.AppendUint32(out, 1) // TTL
		out = binary.BigEndian.AppendUint16(out, 4)
		out = append(out, ip...)
	}
	return out
}

// serveDNS answers queries on conn until ctx ends. resolve returns the
// addresses for a name and whether the name exists.
func serveDNS(ctx context.Context, conn net.PacketConn, resolve func(ctx context.Context, name string) ([]net.IP, bool)) error {
	go func() {
		<-ctx.Done()
		conn.Close()
	}()
	buf := make([]byte, 512)
	for {
		n, from, err := conn.ReadFrom(buf)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
		q, err := parseQuery(buf[:n])
		if err != nil {
			continue
		}
		ips, known := resolve(ctx, q.name)
		conn.WriteTo(answer(q, known, ips), from)
	}
}
