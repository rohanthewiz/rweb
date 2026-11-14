package rweb_test

// Tests for N-050: response header names match case-insensitively in
// Header and SetHeader, so a handler cannot send the same header twice by
// spelling it differently, and SetHeader replaces every value of a name.

import (
	"io"
	"net"
	"strings"
	"testing"

	"github.com/rohanthewiz/assert"
	"github.com/rohanthewiz/rweb"
)

// rawGet sends one GET over a fresh connection and returns the response's
// header block lines (status line excluded), so duplicate lines show.
func rawGet(t *testing.T, port, path string) []string {
	t.Helper()
	conn, err := net.Dial("tcp", "127.0.0.1:"+port)
	assert.Nil(t, err)
	defer conn.Close()
	_, _ = conn.Write([]byte("GET " + path + " HTTP/1.1\r\nHost: x\r\nConnection: close\r\n\r\n"))
	raw, _ := io.ReadAll(conn)
	head, _, _ := strings.Cut(string(raw), "\r\n\r\n")
	lines := strings.Split(head, "\r\n")
	return lines[1:]
}

// countHeader counts the header lines named name, any case.
func countHeader(lines []string, name string) (n int, values []string) {
	for _, l := range lines {
		k, v, ok := strings.Cut(l, ": ")
		if ok && strings.EqualFold(k, name) {
			n++
			values = append(values, v)
		}
	}
	return
}

func TestResponseHeaderCaseInsensitive(t *testing.T) {
	s := rweb.NewServer()
	s.Get("/", func(ctx rweb.Context) error {
		res := ctx.Response()
		res.SetHeader("X-Thing", "one")

		// Lookup in any case finds it.
		assert.Equal(t, "one", res.Header("x-thing"))
		assert.Equal(t, "one", res.Header("X-THING"))
		// A key differing only by Unicode folding is a different name: the
		// Kelvin sign (U+212A) folds to "k" but is not an ASCII "k".
		res.SetHeader("X-Kind", "ascii")
		assert.Equal(t, "", res.Header("X-Kind"))

		// Set in another case replaces rather than adds.
		res.SetHeader("x-thing", "two")
		assert.Equal(t, "two", res.Header("X-Thing"))
		return nil
	})
	resp := s.Request("GET", "/", nil, nil)
	assert.Equal(t, 200, resp.Status())
	assert.Equal(t, "two", resp.Header("X-THING"))
}

// The reported case on the wire: a lower-case content-type set before
// WriteJSON yields one Content-Type line, WriteJSON's; set after, the
// handler's. SetHeader also collapses AddHeader duplicates of any case,
// and AddHeader still repeats.
func TestResponseHeaderNoDuplicatesOnWire(t *testing.T) {
	readyChan := make(chan struct{}, 1)
	s := rweb.NewServer(rweb.ServerOptions{ReadyChan: readyChan, Address: "localhost:"})
	s.Get("/before", func(ctx rweb.Context) error {
		ctx.Response().SetHeader("content-type", "text/plain")
		return ctx.WriteJSON(map[string]int{"a": 1})
	})
	s.Get("/after", func(ctx rweb.Context) error {
		err := ctx.WriteJSON(map[string]int{"a": 1})
		ctx.Response().SetHeader("content-type", "application/vnd.api+json")
		return err
	})
	s.Get("/collapse", func(ctx rweb.Context) error {
		res := ctx.Response()
		res.AddHeader("X-Multi", "1")
		res.SetHeader("X-Middle", "m")
		res.AddHeader("x-multi", "2")
		res.AddHeader("X-MULTI", "3")
		res.SetHeader("X-Multi", "final")
		res.AddHeader("Set-Cookie", "a=1")
		res.AddHeader("set-cookie", "b=2")
		return nil
	})

	go func() {
		defer stopRun(s)
		<-readyChan
		port := s.GetListenPort()

		n, vals := countHeader(rawGet(t, port, "/before"), "Content-Type")
		assert.Equal(t, 1, n)
		assert.True(t, strings.HasPrefix(vals[0], "application/json"))

		n, vals = countHeader(rawGet(t, port, "/after"), "Content-Type")
		assert.Equal(t, 1, n)
		assert.Equal(t, "application/vnd.api+json", vals[0])

		lines := rawGet(t, port, "/collapse")
		n, vals = countHeader(lines, "X-Multi")
		assert.Equal(t, 1, n)
		assert.Equal(t, "final", vals[0])
		// The kept entry stays where the first one was: before X-Middle.
		iMulti, iMiddle := -1, -1
		for i, l := range lines {
			if strings.HasPrefix(l, "X-Multi:") {
				iMulti = i
			}
			if strings.HasPrefix(l, "X-Middle:") {
				iMiddle = i
			}
		}
		assert.True(t, iMulti >= 0 && iMulti < iMiddle)
		n, _ = countHeader(lines, "Set-Cookie")
		assert.Equal(t, 2, n)
	}()

	_ = s.Run()
}
