package rweb

// Location / Content-Location mapping for Proxy routes (N-065): an absolute
// URL the upstream sends back on its own scheme and host, under the target's
// base path, is mapped onto the route's prefix (proxyRedirect, proxy.go);
// everything else passes through as sent.

import (
	"net/url"
	"testing"
)

func TestProxyRedirectRewrite(t *testing.T) {
	// route mirrors Server.proxy: the prefix without its leading slash, and
	// that prefix with its first strip tokens removed.
	route := func(prefix, target, stripped string) *proxyRedirect {
		u, err := url.Parse(target)
		if err != nil {
			t.Fatal(err)
		}
		return newProxyRedirect(u, prefix, stripped)
	}
	stripAll := route("api", "http://up:9000", "") // Proxy("/api", "http://up:9000", 1)
	keep := route("api", "http://up:9000", "api")  // Proxy("/api", "http://up:9000", 0)
	based := route("a/b", "http://up/base", "b")   // Proxy("/a/b", "http://up/base", 1)
	root := route("", "http://up/base", "")        // Proxy("/", "http://up/base", 0)
	tls := route("api", "https://up", "")          // default port 443
	v6 := route("api", "http://[::1]:9000", "")    // IP literal
	for _, c := range []struct {
		name string
		m    *proxyRedirect
		in   string
		want string
	}{
		{"target URL mapped under the prefix", stripAll, "http://up:9000/login", "/api/login"},
		{"query, fragment and host case kept", stripAll, "http://UP:9000/login?a=1&b#top", "/api/login?a=1&b#top"},
		{"empty query kept", stripAll, "http://up:9000/x?", "/api/x?"},
		{"target root", stripAll, "http://up:9000", "/api/"},
		{"escapes kept", stripAll, "http://up:9000/a%20b/c%2Fd", "/api/a%20b/c%2Fd"},
		{"relative path left alone", stripAll, "/new", "/new"},
		{"relative reference left alone", stripAll, "next?x=1", "next?x=1"},
		{"another host left alone", stripAll, "http://other:9000/x", "http://other:9000/x"},
		{"another port left alone", stripAll, "http://up:9001/x", "http://up:9001/x"},
		{"another scheme left alone", stripAll, "https://up:9000/x", "https://up:9000/x"},
		{"userinfo left alone", stripAll, "http://u@up:9000/x", "http://u@up:9000/x"},
		{"unparsable left alone", stripAll, "http://up:9000/%zz", "http://up:9000/%zz"},
		{"unstripped prefix: path kept", keep, "http://up:9000/api/x", "/api/x"},
		{"unstripped prefix: outside the base", keep, "http://up:9000/other", "http://up:9000/other"},
		{"base is a segment, not a string prefix", keep, "http://up:9000/apix", "http://up:9000/apix"},
		{"target path mapped", based, "http://up/b/base/x", "/a/b/x"},
		{"target path itself", based, "http://up/b/base", "/a/b"},
		{"default port equals none", based, "http://up:80/b/base/x", "/a/b/x"},
		{"mounted at the root", root, "http://up/base/x", "/x"},
		{"mounted at the root, base itself", root, "http://up/base", "/"},
		{"https default port", tls, "https://up:443/x", "/api/x"},
		{"IPv6 literal", v6, "http://[::1]:9000/x", "/api/x"},
	} {
		if got := c.m.rewrite(c.in); got != c.want {
			t.Errorf("%s: rewrite(%q) = %q, want %q", c.name, c.in, got, c.want)
		}
	}
}
