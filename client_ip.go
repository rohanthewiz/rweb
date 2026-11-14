package rweb

import (
	"net"
	"net/netip"
	"slices"
	"strings"

	"github.com/rohanthewiz/rweb/consts"
)

// Client address behind reverse proxies (N-055).
//
// X-Forwarded-For and X-Real-IP are request headers: any client can send
// them, with any value. They mean something only when written by a proxy
// this server trusts, so ClientIP reads them only when the connection's peer
// is in ServerOptions.TrustedProxies, and then believes only the entries that
// trusted proxies vouch for.
//
// How a chain of proxies builds X-Forwarded-For, each appending the address
// of the peer it accepted the request from:
//
//	client 203.0.113.7 sends "X-Forwarded-For: 1.2.3.4" (forged)
//	   │
//	   ▼  nginx 10.0.0.5 appends its peer:   "1.2.3.4, 203.0.113.7"
//	   │
//	   ▼  lb 10.0.0.9 appends its peer:      "1.2.3.4, 203.0.113.7, 10.0.0.5"
//	   │
//	   ▼  this server; peer = 10.0.0.9
//
// Only the entries to the right of the client's own are known to be true,
// so the header is read from the right:
//
//	peer 10.0.0.9 trusted?        yes -> read the header
//	10.0.0.5 (rightmost) trusted? yes -> keep walking left
//	203.0.113.7 trusted?          no  -> the client: nothing beyond it is
//	                                     vouched for by a trusted proxy
//	(1.2.3.4 is never looked at; the leftmost-entry rule used before N-055
//	 returned it)
//
// Rules of the walk, in ClientIP's order:
//
//	peer not trusted                    -> the peer's address; headers ignored
//	X-Forwarded-For, right to left:
//	  empty element ("a, , b")          -> skipped (allowed by the list syntax)
//	  not an IP address ("unknown")     -> stop: the chain is broken there;
//	                                       answer the last valid entry seen,
//	                                       if any
//	  not trusted                       -> answer it
//	  trusted                           -> keep walking
//	  ran out, all trusted              -> answer the leftmost valid entry
//	no answer from it, X-Real-IP is IP  -> X-Real-IP (set by nginx and others
//	                                       to the peer they accepted from)
//	otherwise                           -> the peer's address
//
// With every address trusted (TrustAllProxies) the walk never stops early and
// answers the leftmost valid entry: the behaviour before N-055, except that
// an entry that is not an IP address is no longer returned.
//
// A synthetic request (s.Request) has no peer. Its headers come from the
// caller's own code, not from a network hop that could forge them, so they
// are read as if from a trusted peer, unless no proxy is trusted at all.

// defaultTrustedProxies is what a nil ServerOptions.TrustedProxies selects:
// loopback and the private ranges, as Rails' RemoteIp does. They cover the
// usual deployments (nginx or Caddy on the same host, a Docker or Kubernetes
// network, a load balancer inside a VPC) without configuration, while a peer
// on the public internet can never have its headers believed. The cost: any
// host inside the private network can claim another client address.
//
// Shared by every server left at the default; never modified (see
// DefaultTrustedProxies, which hands out copies).
var defaultTrustedProxies = []netip.Prefix{
	netip.MustParsePrefix("127.0.0.0/8"),    // IPv4 loopback
	netip.MustParsePrefix("::1/128"),        // IPv6 loopback
	netip.MustParsePrefix("10.0.0.0/8"),     // RFC 1918
	netip.MustParsePrefix("172.16.0.0/12"),  // RFC 1918 (Docker's default bridge is 172.17/16)
	netip.MustParsePrefix("192.168.0.0/16"), // RFC 1918
	netip.MustParsePrefix("fc00::/7"),       // IPv6 unique local (RFC 4193)
}

// DefaultTrustedProxies returns the list a nil ServerOptions.TrustedProxies
// selects: loopback (127.0.0.0/8, ::1) and the private ranges (10.0.0.0/8,
// 172.16.0.0/12, 192.168.0.0/16, fc00::/7). The slice is a copy, so an app
// may append its own proxies to it:
//
//	TrustedProxies: append(rweb.DefaultTrustedProxies(), netip.MustParsePrefix("203.0.113.0/24"))
func DefaultTrustedProxies() []netip.Prefix {
	return slices.Clone(defaultTrustedProxies)
}

// TrustAllProxies returns a list holding every address (0.0.0.0/0, ::/0):
// ClientIP then believes X-Forwarded-For from any peer and answers its
// leftmost entry, as it did before N-055. Only for a server that cannot be
// reached except through proxies that overwrite the header; otherwise any
// client can choose the address ClientIP reports.
func TrustAllProxies() []netip.Prefix {
	return []netip.Prefix{netip.MustParsePrefix("0.0.0.0/0"), netip.MustParsePrefix("::/0")}
}

// NoTrustedProxies returns an empty, non-nil list: no peer is trusted, and
// ClientIP is always the connection's peer address. (A nil list selects
// DefaultTrustedProxies instead; this is the explicit "none".)
func NoTrustedProxies() []netip.Prefix {
	return []netip.Prefix{}
}

// resolveTrustedProxies turns ServerOptions.TrustedProxies into the list the
// server checks peers against: nil selects the default, anything else is
// copied (so the caller's slice can change afterwards without effect) with
// each prefix masked. An invalid prefix (the zero netip.Prefix) is a
// configuration mistake that would otherwise match nothing in silence, so it
// is logged and left out. The result is never nil for a non-nil input, which
// keeps NoTrustedProxies' "none" from turning into the default.
func resolveTrustedProxies(list []netip.Prefix, log Logger) []netip.Prefix {
	if list == nil {
		return defaultTrustedProxies
	}
	out := make([]netip.Prefix, 0, len(list))
	for i, p := range list {
		if !p.IsValid() {
			log.Error("rweb: TrustedProxies entry is not a valid prefix; ignored", "index", i)
			continue
		}
		out = append(out, p.Masked())
	}
	return out
}

// addrTrusted reports whether a falls in one of the prefixes. An IPv4 address
// arriving as IPv4-mapped IPv6 (::ffff:10.0.0.1, as a dual-stack listener can
// report it) is unmapped first, so it matches the IPv4 prefixes; and a zone
// is dropped, since netip.Prefix.Contains never matches an address with one.
func addrTrusted(a netip.Addr, prefixes []netip.Prefix) bool {
	a = a.Unmap().WithZone("")
	for _, p := range prefixes {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// parseForwardedAddr parses one address as proxies write it in
// X-Forwarded-For or X-Real-IP: a bare IPv4 or IPv6 address, or one with a
// port ("1.2.3.4:5678", "[2001:db8::1]:5678", as some cloud load balancers
// send), or a bracketed IPv6 address with no port. The result is unmapped
// and has no zone, so the same client gives the same string however a proxy
// spelled it.
func parseForwardedAddr(s string) (netip.Addr, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return netip.Addr{}, false
	}
	a, err := netip.ParseAddr(s)
	if err != nil {
		if ap, err2 := netip.ParseAddrPort(s); err2 == nil {
			a, err = ap.Addr(), nil
		} else if len(s) > 2 && s[0] == '[' && s[len(s)-1] == ']' {
			a, err = netip.ParseAddr(s[1 : len(s)-1])
		}
	}
	if err != nil || !a.IsValid() {
		return netip.Addr{}, false
	}
	return a.Unmap().WithZone(""), true
}

// trustedProxies is the list this request's server checks peers against;
// the default for a context built outside NewServer (tests).
func (ctx *context) trustedProxies() []netip.Prefix {
	if ctx.server == nil {
		return defaultTrustedProxies
	}
	return ctx.server.trustedProxies
}

// peerAddr is the connection's remote address, when there is a connection
// and its address is an IP (not, say, a Unix socket's path).
func (ctx *context) peerAddr() (netip.Addr, bool) {
	if ctx.conn == nil {
		return netip.Addr{}, false
	}
	ra := ctx.conn.RemoteAddr()
	if tcp, ok := ra.(*net.TCPAddr); ok {
		// The common case, without formatting and re-parsing the address.
		a := tcp.AddrPort().Addr()
		return a, a.IsValid()
	}
	ap, err := netip.ParseAddrPort(ra.String())
	if err != nil {
		return netip.Addr{}, false
	}
	return ap.Addr(), true
}

// forwardedTrusted reports whether this request's forwarding headers
// (X-Forwarded-For / -Proto / -Host, X-Real-IP) were written by a trusted
// hop: the peer is in the trusted list, or there is no peer (a synthetic
// request, whose headers are the caller's own) and some proxy is trusted.
func (ctx *context) forwardedTrusted() bool {
	prefixes := ctx.trustedProxies()
	if len(prefixes) == 0 {
		return false
	}
	if ctx.conn == nil {
		return true
	}
	a, ok := ctx.peerAddr()
	return ok && addrTrusted(a, prefixes)
}

// ClientIP returns the client's IP address: the connection's peer, or, when
// that peer is a trusted proxy (ServerOptions.TrustedProxies), the address
// the proxies' X-Forwarded-For (or X-Real-IP) gives for the client. See the
// top of this file for the walk and its diagram. An address taken from a
// header is returned in canonical form (IPv4-mapped IPv6 unmapped, no port,
// no zone); the peer's address is returned as the connection reports it,
// without the port. "" only for a synthetic request with no usable header.
func (ctx *context) ClientIP() string {
	if ctx.forwardedTrusted() {
		if a, ok := clientFromXFF(ctx.request.headers, ctx.trustedProxies()); ok {
			return a.String()
		}
		if a, ok := parseForwardedAddr(ctx.request.Header(consts.HeaderXRealIP)); ok {
			return a.String()
		}
	}
	return peerIP(ctx)
}

// clientFromXFF walks the X-Forwarded-For entries from the right, across
// every X-Forwarded-For line (a request may carry several; together they
// form one list, in order), and returns the first entry the trusted proxies
// do not vouch beyond; see the rules at the top of this file. ok is false
// when the header holds no valid address before the walk stops.
//
// The walk goes backwards through the header lines and, within a line,
// backwards from comma to comma, so it neither joins the lines nor builds a
// slice of entries.
func clientFromXFF(headers []Header, prefixes []netip.Prefix) (netip.Addr, bool) {
	var last netip.Addr // the leftmost valid entry seen so far
	for i := len(headers) - 1; i >= 0; i-- {
		if !strings.EqualFold(headers[i].Key, consts.HeaderXForwardedFor) {
			continue
		}
		rest := headers[i].Value
		for {
			entry := rest
			comma := strings.LastIndexByte(rest, ',')
			if comma >= 0 {
				entry, rest = rest[comma+1:], rest[:comma]
			}
			if strings.TrimSpace(entry) != "" {
				a, ok := parseForwardedAddr(entry)
				if !ok {
					// Broken chain. Whatever is left of here was not written
					// by a proxy we trust to append real addresses.
					return last, last.IsValid()
				}
				if !addrTrusted(a, prefixes) {
					return a, true
				}
				last = a
			}
			if comma < 0 {
				break
			}
		}
	}
	return last, last.IsValid()
}
