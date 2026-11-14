package rweb

import (
	"io"
	"net"
	"net/http"
	"net/netip"
	"testing"
	"time"

	"github.com/rohanthewiz/assert"
)

// N-055: ClientIP believed X-Forwarded-For (leftmost entry) and X-Real-IP
// from any peer, so any client could choose the address it reported. Now the
// headers are read only from a peer in ServerOptions.TrustedProxies (default:
// loopback and private ranges), and X-Forwarded-For is walked from the right
// past the trusted proxies, so an entry the client wrote ahead of the
// proxies' own is never the answer.

// peerConn is a net.Conn whose only working method is RemoteAddr, which is
// all ClientIP asks of the connection. It lets a test put any peer address
// (public, private, IPv4-mapped, not an IP at all) behind a context.
type peerConn struct {
	net.Conn
	addr net.Addr
}

func (c peerConn) RemoteAddr() net.Addr { return c.addr }

// pipeAddr is a peer address that is not an IP (as net.Pipe reports).
type pipeAddr struct{}

func (pipeAddr) Network() string { return "pipe" }
func (pipeAddr) String() string  { return "pipe" }

// clientIPFor builds a context on s with the given peer ("" for none, as on
// a synthetic request) and request headers, and returns its ClientIP.
func clientIPFor(s *Server, peer net.Addr, headers ...Header) string {
	ctx := s.newContext()
	if peer != nil {
		ctx.conn = peerConn{addr: peer}
	}
	ctx.request.headers = append(ctx.request.headers[:0], headers...)
	return ctx.ClientIP()
}

func tcpPeer(ip string) net.Addr {
	return &net.TCPAddr{IP: net.ParseIP(ip), Port: 4711}
}

func xff(v string) Header { return Header{Key: "X-Forwarded-For", Value: v} }
func xri(v string) Header { return Header{Key: "X-Real-IP", Value: v} }

func TestClientIPTrustedProxies(t *testing.T) {
	def := NewServer() // default list: loopback + private
	none := NewServer(ServerOptions{TrustedProxies: NoTrustedProxies()})
	all := NewServer(ServerOptions{TrustedProxies: TrustAllProxies()})
	custom := NewServer(ServerOptions{TrustedProxies: []netip.Prefix{netip.MustParsePrefix("203.0.113.0/24")}})

	cases := []struct {
		name    string
		s       *Server
		peer    net.Addr
		headers []Header
		want    string
	}{
		// The spoofing this item is about: a public peer's headers are ignored.
		{"public peer, forged XFF ignored", def, tcpPeer("203.0.113.50"), []Header{xff("1.2.3.4")}, "203.0.113.50"},
		{"public peer, forged X-Real-IP ignored", def, tcpPeer("203.0.113.50"), []Header{xri("1.2.3.4")}, "203.0.113.50"},
		{"public peer, no headers", def, tcpPeer("203.0.113.50"), nil, "203.0.113.50"},

		// Trusted peers: the walk from the right (the file's diagram).
		{"chain: first untrusted from the right", def, tcpPeer("10.0.0.9"),
			[]Header{xff("1.2.3.4, 203.0.113.7, 10.0.0.5")}, "203.0.113.7"},
		{"nginx on the host", def, tcpPeer("127.0.0.1"), []Header{xff("203.0.113.7")}, "203.0.113.7"},
		{"IPv6 loopback peer", def, tcpPeer("::1"), []Header{xff("203.0.113.7")}, "203.0.113.7"},
		{"Docker bridge peer", def, tcpPeer("172.17.0.1"), []Header{xff("203.0.113.7")}, "203.0.113.7"},
		{"outside 172.16/12 is public", def, tcpPeer("172.32.0.1"), []Header{xff("203.0.113.7")}, "172.32.0.1"},
		{"IPv6 ULA peer", def, tcpPeer("fd00::1"), []Header{xff("203.0.113.7")}, "203.0.113.7"},
		{"IPv4-mapped peer is unmapped", def, tcpPeer("::ffff:10.0.0.9"), []Header{xff("203.0.113.7")}, "203.0.113.7"},
		{"walk crosses header lines", def, tcpPeer("10.0.0.9"),
			[]Header{xff("1.1.1.1"), xff("203.0.113.7, 10.0.0.5")}, "203.0.113.7"},
		{"walk crosses header lines, client on the earlier line", def, tcpPeer("10.0.0.9"),
			[]Header{xff("203.0.113.7"), xff("10.0.0.5")}, "203.0.113.7"},
		{"all trusted: leftmost", def, tcpPeer("10.0.0.9"), []Header{xff("10.0.0.1, 10.0.0.2")}, "10.0.0.1"},
		{"empty elements skipped", def, tcpPeer("10.0.0.9"), []Header{xff("203.0.113.7, , 10.0.0.5,")}, "203.0.113.7"},

		// A broken chain stops the walk at the last valid entry.
		{"invalid entry stops the walk", def, tcpPeer("10.0.0.9"), []Header{xff("garbage, 10.0.0.2")}, "10.0.0.2"},
		{"rightmost invalid: peer", def, tcpPeer("10.0.0.9"), []Header{xff("203.0.113.7, unknown")}, "10.0.0.9"},
		{"rightmost invalid: X-Real-IP", def, tcpPeer("10.0.0.9"),
			[]Header{xff("unknown"), xri("198.51.100.5")}, "198.51.100.5"},
		{"blank XFF: X-Real-IP", def, tcpPeer("10.0.0.9"), []Header{xff("  "), xri("198.51.100.6")}, "198.51.100.6"},
		{"X-Real-IP not an IP: peer", def, tcpPeer("10.0.0.9"), []Header{xri("not-an-ip")}, "10.0.0.9"},
		{"XFF ahead of X-Real-IP", def, tcpPeer("10.0.0.9"),
			[]Header{xri("198.51.100.5"), xff("203.0.113.7")}, "203.0.113.7"},

		// Address spellings proxies use; answers are canonical.
		{"IPv6 with port", def, tcpPeer("10.0.0.9"), []Header{xff("[2001:db8::1]:4711, 10.0.0.5")}, "2001:db8::1"},
		{"IPv4 with port", def, tcpPeer("10.0.0.9"), []Header{xff("203.0.113.7:5678")}, "203.0.113.7"},
		{"bracketed IPv6", def, tcpPeer("10.0.0.9"), []Header{xff("[2001:db8::2]")}, "2001:db8::2"},
		{"upper-case IPv6", def, tcpPeer("10.0.0.9"), []Header{xff("2001:DB8::3")}, "2001:db8::3"},
		{"IPv4-mapped entry", def, tcpPeer("10.0.0.9"), []Header{xff("::ffff:203.0.113.7")}, "203.0.113.7"},
		{"trusted-range entry written mapped", def, tcpPeer("10.0.0.9"),
			[]Header{xff("203.0.113.7, ::ffff:10.0.0.5")}, "203.0.113.7"},

		// The explicit settings.
		{"none: loopback peer's XFF ignored", none, tcpPeer("127.0.0.1"), []Header{xff("1.2.3.4")}, "127.0.0.1"},
		{"all: leftmost, as before N-055", all, tcpPeer("203.0.113.50"), []Header{xff("1.2.3.4, 5.6.7.8")}, "1.2.3.4"},
		{"all: X-Real-IP", all, tcpPeer("203.0.113.50"), []Header{xri("1.2.3.4")}, "1.2.3.4"},
		{"custom list: peer in it", custom, tcpPeer("203.0.113.50"),
			[]Header{xff("198.51.100.1, 10.0.0.5")}, "10.0.0.5"}, // 10/8 is not in this list
		{"custom list: loopback no longer trusted", custom, tcpPeer("127.0.0.1"), []Header{xff("1.2.3.4")}, "127.0.0.1"},

		// No usable peer address.
		{"synthetic request reads headers", def, nil, []Header{xff("203.0.113.7")}, "203.0.113.7"},
		{"synthetic request, none trusted", none, nil, []Header{xff("203.0.113.7")}, ""},
		{"non-IP peer is untrusted, reported as is", def, pipeAddr{}, []Header{xff("203.0.113.7")}, "pipe"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, clientIPFor(tc.s, tc.peer, tc.headers...))
		})
	}
}

// How ServerOptions.TrustedProxies is resolved: nil is the default list,
// NoTrustedProxies and WithTrustedProxies() mean none (not the default),
// the list is masked and copied, an invalid prefix is logged and dropped,
// and WithOptions carries it over.
func TestTrustedProxiesResolution(t *testing.T) {
	assert.Equal(t, len(defaultTrustedProxies), len(NewServer().trustedProxies))

	assert.Equal(t, 0, len(NewServer(ServerOptions{TrustedProxies: NoTrustedProxies()}).trustedProxies))
	assert.Equal(t, 0, len(NewServerWithOptions(WithTrustedProxies()).trustedProxies))
	assert.Equal(t, false, NewServerWithOptions(WithTrustedProxies()).trustedProxies == nil)

	// Masked: an address with a prefix length acts as its network.
	s := NewServerWithOptions(WithTrustedProxies(netip.MustParsePrefix("198.51.100.77/24")))
	assert.Equal(t, "203.0.113.7", clientIPFor(s, tcpPeer("198.51.100.1"), xff("203.0.113.7")))

	// Copied: changing the caller's slice afterwards has no effect.
	list := []netip.Prefix{netip.MustParsePrefix("198.51.100.0/24")}
	s = NewServer(ServerOptions{TrustedProxies: list})
	list[0] = netip.MustParsePrefix("192.0.2.0/24")
	assert.Equal(t, "203.0.113.7", clientIPFor(s, tcpPeer("198.51.100.1"), xff("203.0.113.7")))

	// DefaultTrustedProxies hands out a copy that can be appended to.
	d := DefaultTrustedProxies()
	d[0] = netip.MustParsePrefix("192.0.2.0/24")
	assert.Equal(t, "127.0.0.0/8", defaultTrustedProxies[0].String())

	// An invalid (zero) prefix is reported and left out.
	logs := &captureLogger{}
	s = NewServer(ServerOptions{Logger: logs, TrustedProxies: []netip.Prefix{{}, netip.MustParsePrefix("198.51.100.0/24")}})
	assert.Equal(t, 1, len(s.trustedProxies))
	_, found := logs.find("error", "TrustedProxies entry is not a valid prefix")
	assert.Equal(t, true, found)

	// WithOptions copies the field.
	s = NewServerWithOptions(WithOptions(ServerOptions{TrustedProxies: NoTrustedProxies()}))
	assert.Equal(t, "127.0.0.1", clientIPFor(s, tcpPeer("127.0.0.1"), xff("203.0.113.7")))
}

// Over a real connection (a *net.TCPAddr peer on loopback): the default
// trusts the loopback client's X-Forwarded-For, a server trusting none
// reports the loopback address instead.
func TestClientIPOverTheWire(t *testing.T) {
	for _, tc := range []struct {
		name string
		opts ServerOptions
		want string
	}{
		{"default trusts loopback", ServerOptions{}, "203.0.113.7"},
		{"none trusted", ServerOptions{TrustedProxies: NoTrustedProxies()}, "127.0.0.1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := NewServer(tc.opts)
			s.Get("/ip", func(ctx Context) error { return ctx.WriteString(ctx.ClientIP()) })
			addr := serveTest(t, s, transport{name: "tcp"})

			req, _ := http.NewRequest(http.MethodGet, "http://"+addr+"/ip", nil)
			req.Header.Set("X-Forwarded-For", "203.0.113.7")
			resp, err := (&http.Client{Timeout: 3 * time.Second}).Do(req)
			if err != nil {
				t.Fatal(err)
			}
			body, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			assert.Equal(t, tc.want, string(body))
		})
	}
}
