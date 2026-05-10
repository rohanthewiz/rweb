// Regression tests for the form-handling fixes landed in Request.go,
// Context.go, and Server.go.
//
// The bugs being guarded against were all rooted in the fact that contexts
// are pooled per *connection* (see Server.handleConnection): two consecutive
// requests on the same keep-alive connection share the same *context, so any
// state on req.multipartForm / req.postArgs that survived Clean() would leak
// into the next request. Each test below forces connection reuse via a
// shared *http.Transport (or a raw net.Conn for the parse-error case) and
// uses httptrace to *verify* reuse — without that verification, a test that
// happened to land on a fresh connection would silently pass.
package rweb_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptrace"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/rohanthewiz/assert"
	"github.com/rohanthewiz/rweb"
)

// keepAliveClient builds an http.Client whose underlying Transport will pool
// idle connections so a follow-up request to the same host:port reuses the
// previous connection. We pin MaxConnsPerHost=1 to make reuse the only option.
func keepAliveClient() (*http.Client, *http.Transport) {
	tr := &http.Transport{
		MaxIdleConns:        1,
		MaxIdleConnsPerHost: 1,
		MaxConnsPerHost:     1,
		IdleConnTimeout:     30 * time.Second,
		DisableKeepAlives:   false,
	}
	return &http.Client{Transport: tr, Timeout: 5 * time.Second}, tr
}

// doWithReuseFlag executes req and records, via httptrace, whether the
// dialer found an existing connection in the pool. The returned *bool is
// non-nil and points to true iff this request reused a prior connection.
func doWithReuseFlag(client *http.Client, req *http.Request) (*http.Response, *bool, error) {
	reused := new(bool)
	trace := &httptrace.ClientTrace{
		GotConn: func(info httptrace.GotConnInfo) {
			*reused = info.Reused
		},
	}
	req = req.WithContext(httptrace.WithClientTrace(req.Context(), trace))
	resp, err := client.Do(req)
	return resp, reused, err
}

// TestMultipartDoesNotLeakIntoFollowingGet guards FormValue's Content-Type
// dispatch. Pre-fix, FormValue checked `req.multipartForm != nil` rather
// than the current request's Content-Type — so a GET that landed on a
// connection where the prior request was multipart could read string
// values out of the previous request's *multipart.Form.
func TestMultipartDoesNotLeakIntoFollowingGet(t *testing.T) {
	readyChan := make(chan struct{}, 1)
	clientDone := make(chan struct{})

	s := rweb.NewServer(rweb.ServerOptions{
		ReadyChan: readyChan,
		Address:   "localhost:",
	})

	// Captures what the GET handler observed. A leaked value would land
	// here as "request-one" instead of empty.
	var getSawValue atomic.Pointer[string]

	s.Post("/multipart", func(ctx rweb.Context) error {
		// Sanity: confirm request 1 actually saw its own value, otherwise
		// a regression that simply broke parsing would also pass the
		// next assertion (vacuously).
		if v := ctx.Request().FormValue("leaktest"); v != "request-one" {
			return ctx.WriteString("first-mismatch:" + v)
		}
		return ctx.WriteString("ok-1")
	})

	s.Get("/check", func(ctx rweb.Context) error {
		v := ctx.Request().FormValue("leaktest")
		getSawValue.Store(&v)
		return ctx.WriteString("ok-2")
	})

	go func() {
		defer close(clientDone)
		defer func() { _ = syscall.Kill(syscall.Getpid(), syscall.SIGTERM) }()
		<-readyChan

		client, tr := keepAliveClient()
		defer tr.CloseIdleConnections()

		// --- Request 1: multipart POST with the marker field. ---
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		_ = mw.WriteField("leaktest", "request-one")
		assert.Nil(t, mw.Close())

		url1 := fmt.Sprintf("http://127.0.0.1:%s/multipart", s.GetListenPort())
		req1, _ := http.NewRequest(http.MethodPost, url1, &buf)
		req1.Header.Set("Content-Type", mw.FormDataContentType())
		resp1, _, err := doWithReuseFlag(client, req1)
		assert.Nil(t, err)
		_, _ = io.Copy(io.Discard, resp1.Body) // drain so conn returns to pool
		_ = resp1.Body.Close()

		// --- Request 2: plain GET on the same connection. ---
		url2 := fmt.Sprintf("http://127.0.0.1:%s/check", s.GetListenPort())
		req2, _ := http.NewRequest(http.MethodGet, url2, nil)
		resp2, reused, err := doWithReuseFlag(client, req2)
		assert.Nil(t, err)
		_, _ = io.Copy(io.Discard, resp2.Body)
		_ = resp2.Body.Close()

		// Without connection reuse the test would not exercise the bug.
		assert.Equal(t, *reused, true)

		got := getSawValue.Load()
		if got == nil {
			t.Errorf("GET handler did not run")
			return
		}
		if *got != "" {
			t.Errorf("multipart→GET leak: FormValue('leaktest') on GET = %q (want empty)", *got)
		}
	}()

	_ = s.Run()
	<-clientDone
}

// TestMultipartDoesNotLeakIntoFollowingMultipart guards CleanupMultipartForm.
// The first request submits `leaktest`; the second submits a different field
// only. Pre-fix, multipartForm survived Cleanup (RemoveAll() doesn't clear
// Form.Value), and ParseMultipartForm short-circuited on the still-non-nil
// pointer — so request 2 saw request 1's values. The fix nils out
// multipartForm in CleanupMultipartForm.
func TestMultipartDoesNotLeakIntoFollowingMultipart(t *testing.T) {
	readyChan := make(chan struct{}, 1)
	clientDone := make(chan struct{})

	s := rweb.NewServer(rweb.ServerOptions{
		ReadyChan: readyChan,
		Address:   "localhost:",
	})

	var (
		req1Saw atomic.Pointer[string]
		req2Saw atomic.Pointer[string]
	)

	s.Post("/upload", func(ctx rweb.Context) error {
		v := ctx.Request().FormValue("leaktest")
		// Distinguish the two requests by a header the client sets.
		switch ctx.Request().Header("X-Phase") {
		case "1":
			req1Saw.Store(&v)
		case "2":
			req2Saw.Store(&v)
		}
		return ctx.WriteString("ok")
	})

	go func() {
		defer close(clientDone)
		defer func() { _ = syscall.Kill(syscall.Getpid(), syscall.SIGTERM) }()
		<-readyChan

		client, tr := keepAliveClient()
		defer tr.CloseIdleConnections()
		urlBase := fmt.Sprintf("http://127.0.0.1:%s/upload", s.GetListenPort())

		// --- Request 1: multipart with leaktest=request-one. ---
		var buf1 bytes.Buffer
		mw1 := multipart.NewWriter(&buf1)
		_ = mw1.WriteField("leaktest", "request-one")
		assert.Nil(t, mw1.Close())

		req1, _ := http.NewRequest(http.MethodPost, urlBase, &buf1)
		req1.Header.Set("Content-Type", mw1.FormDataContentType())
		req1.Header.Set("X-Phase", "1")
		resp1, _, err := doWithReuseFlag(client, req1)
		assert.Nil(t, err)
		_, _ = io.Copy(io.Discard, resp1.Body)
		_ = resp1.Body.Close()

		// --- Request 2: multipart that does NOT include `leaktest`. ---
		var buf2 bytes.Buffer
		mw2 := multipart.NewWriter(&buf2)
		_ = mw2.WriteField("other", "request-two")
		assert.Nil(t, mw2.Close())

		req2, _ := http.NewRequest(http.MethodPost, urlBase, &buf2)
		req2.Header.Set("Content-Type", mw2.FormDataContentType())
		req2.Header.Set("X-Phase", "2")
		resp2, reused, err := doWithReuseFlag(client, req2)
		assert.Nil(t, err)
		_, _ = io.Copy(io.Discard, resp2.Body)
		_ = resp2.Body.Close()

		assert.Equal(t, *reused, true)

		// Sanity: request 1 must have seen its own value (otherwise a
		// regression that broke parsing entirely would also pass the
		// next assertion vacuously).
		v1 := req1Saw.Load()
		if v1 == nil || *v1 != "request-one" {
			s := ""
			if v1 != nil {
				s = *v1
			}
			t.Errorf("request 1 sanity failed: FormValue('leaktest') = %q", s)
			return
		}

		v2 := req2Saw.Load()
		if v2 == nil {
			t.Errorf("request 2 handler did not run")
			return
		}
		if *v2 != "" {
			t.Errorf("multipart→multipart leak: FormValue('leaktest') on request 2 = %q (want empty)", *v2)
		}
	}()

	_ = s.Run()
	<-clientDone
}

// TestPostArgsDoNotLeakIntoFollowingNonFormPost guards Context.Clean's
// postArgs reset.
//
// Subtle but important: this test deliberately sends `Content-Type:
// application/json` on request 2 — *not* a GET. Reason: parsePostArgs
// returns early when Content-Type doesn't match BytFormData, and on a GET
// (no Content-Type header) the now-cleared `req.ContentType` cache routes
// through that early-return path BUT also resets postArgs as a side-effect
// of the ParseBytes(empty) call when ContentType happens to be carried over
// (the original ContentType-leak bug). To pin down the postArgs reset fix
// without those interactions, we send a request that is unambiguously
// non-form: parsePostArgs early-returns, and the only thing standing
// between the handler and the prior request's data is the explicit
// postArgs.Reset() in Clean().
func TestPostArgsDoNotLeakIntoFollowingNonFormPost(t *testing.T) {
	readyChan := make(chan struct{}, 1)
	clientDone := make(chan struct{})

	s := rweb.NewServer(rweb.ServerOptions{
		ReadyChan: readyChan,
		Address:   "localhost:",
	})

	var checkSawValue atomic.Pointer[string]

	s.Post("/post", func(ctx rweb.Context) error {
		// Sanity that request 1 sees its own value.
		if v := ctx.Request().GetPostValue("leaktest"); v != "request-one" {
			return ctx.WriteString("first-mismatch:" + v)
		}
		return ctx.WriteString("ok-1")
	})

	s.Post("/check", func(ctx rweb.Context) error {
		v := ctx.Request().GetPostValue("leaktest")
		checkSawValue.Store(&v)
		return ctx.WriteString("ok-2")
	})

	go func() {
		defer close(clientDone)
		defer func() { _ = syscall.Kill(syscall.Getpid(), syscall.SIGTERM) }()
		<-readyChan

		client, tr := keepAliveClient()
		defer tr.CloseIdleConnections()

		// --- Request 1: urlencoded POST. ---
		body := strings.NewReader("leaktest=request-one&other=foo")
		req1, _ := http.NewRequest(http.MethodPost,
			fmt.Sprintf("http://127.0.0.1:%s/post", s.GetListenPort()), body)
		req1.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		resp1, _, err := doWithReuseFlag(client, req1)
		assert.Nil(t, err)
		_, _ = io.Copy(io.Discard, resp1.Body)
		_ = resp1.Body.Close()

		// --- Request 2: JSON POST on the same connection. ---
		// Content-Type is application/json, NOT application/x-www-form-urlencoded.
		// This forces parsePostArgs to early-return on Content-Type mismatch,
		// so the only thing keeping postArgs from leaking is the explicit
		// Reset() we added in Clean().
		jsonBody := strings.NewReader(`{"hello":"world"}`)
		req2, _ := http.NewRequest(http.MethodPost,
			fmt.Sprintf("http://127.0.0.1:%s/check", s.GetListenPort()), jsonBody)
		req2.Header.Set("Content-Type", "application/json")
		resp2, reused, err := doWithReuseFlag(client, req2)
		assert.Nil(t, err)
		_, _ = io.Copy(io.Discard, resp2.Body)
		_ = resp2.Body.Close()

		assert.Equal(t, *reused, true)

		got := checkSawValue.Load()
		if got == nil {
			t.Errorf("check handler did not run")
			return
		}
		if *got != "" {
			t.Errorf("urlencoded→JSON leak: GetPostValue('leaktest') on JSON POST = %q (want empty)", *got)
		}
	}()

	_ = s.Run()
	<-clientDone
}

// TestMalformedMultipartSurfacesParseError guards the error-caching path:
// when ParseMultipartForm fails (here, because the Content-Type lacks a
// boundary parameter), the cached error must reach the handler via
// GetFormFile rather than being swallowed by the server-side eager parse
// and replaced with the generic "no multipart form data" message.
//
// Uses a raw net.Conn because net/http would auto-add a boundary; we need
// to hand-craft a malformed Content-Type.
func TestMalformedMultipartSurfacesParseError(t *testing.T) {
	readyChan := make(chan struct{}, 1)
	clientDone := make(chan struct{})

	s := rweb.NewServer(rweb.ServerOptions{
		ReadyChan: readyChan,
		Address:   "localhost:",
	})

	var sawErr atomic.Pointer[string]

	s.Post("/upload", func(ctx rweb.Context) error {
		_, _, err := ctx.Request().GetFormFile("anything")
		if err != nil {
			msg := err.Error()
			sawErr.Store(&msg)
		}
		return ctx.WriteString("done")
	})

	go func() {
		defer close(clientDone)
		defer func() { _ = syscall.Kill(syscall.Getpid(), syscall.SIGTERM) }()
		<-readyChan

		// Hand-crafted HTTP/1.0 request: Content-Type declares multipart
		// but provides no boundary parameter. ParseMultipartForm must
		// fail and the error must propagate to GetFormFile.
		conn, err := net.Dial("tcp", "127.0.0.1:"+s.GetListenPort())
		assert.Nil(t, err)
		defer func() { _ = conn.Close() }()

		body := "garbage-no-multipart-structure"
		raw := "POST /upload HTTP/1.0\r\n" +
			"Host: 127.0.0.1\r\n" +
			"Content-Type: multipart/form-data\r\n" + // missing boundary=...
			fmt.Sprintf("Content-Length: %d\r\n", len(body)) +
			"\r\n" +
			body
		_, err = conn.Write([]byte(raw))
		assert.Nil(t, err)

		// Drain response so the server completes the handler.
		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		_, _ = io.ReadAll(conn)

		got := sawErr.Load()
		if got == nil {
			t.Errorf("handler did not observe an error from GetFormFile")
			return
		}
		// The fix routes the underlying parse error ("no boundary found
		// in multipart form data") through GetFormFile. Pre-fix, the
		// commented-out ParseMultipartForm call meant the handler only
		// ever saw the generic "no multipart form data" sentinel.
		if *got == "no multipart form data" {
			t.Errorf("GetFormFile masked the real parse error with the generic sentinel: %q", *got)
		}
		if !strings.Contains(*got, "boundary") {
			t.Errorf("expected error to mention boundary; got %q", *got)
		}
	}()

	_ = s.Run()
	<-clientDone
}

// TestContentTypeDoesNotLeakAcrossRequests guards Context.Clean's reset of
// the cached `req.ContentType` byte slice.
//
// Pre-fix, ContentType was populated only when a "Content-Type" header was
// observed during request parsing. A follow-up request that sent NO
// Content-Type header inherited the previous request's value via the
// uncleared cache. The visible symptom was that internal dispatch
// (parsePostArgs / FormValue / ParseMultipartForm) routed against the
// wrong content type. This test asserts the observable behavior: a GET
// following a multipart POST must not have FormValue treat it as multipart.
//
// We probe via a fingerprint that's only reachable when ParseMultipartForm
// runs against the leaked ContentType: with the fix, FormValue's
// Content-Type check fails and the function falls through to GetPostValue
// (which also returns ""), giving an empty result. Without the fix,
// FormValue tries to parse, and the multipartParseErr would be cached —
// detectable via GetFormFile returning that error.
func TestContentTypeDoesNotLeakAcrossRequests(t *testing.T) {
	readyChan := make(chan struct{}, 1)
	clientDone := make(chan struct{})

	s := rweb.NewServer(rweb.ServerOptions{
		ReadyChan: readyChan,
		Address:   "localhost:",
	})

	var (
		req1Sanity     atomic.Pointer[string]
		req2FormFileEr atomic.Pointer[string]
	)

	s.Post("/multipart", func(ctx rweb.Context) error {
		v := ctx.Request().FormValue("marker")
		req1Sanity.Store(&v)
		return ctx.WriteString("ok-1")
	})

	s.Get("/probe", func(ctx rweb.Context) error {
		// On a fresh GET (no Content-Type), GetFormFile must return the
		// "no multipart form data" sentinel — not a parse error from
		// trying to re-parse against a leaked multipart Content-Type.
		_, _, err := ctx.Request().GetFormFile("anything")
		if err != nil {
			msg := err.Error()
			req2FormFileEr.Store(&msg)
		}
		return ctx.WriteString("ok-2")
	})

	go func() {
		defer close(clientDone)
		defer func() { _ = syscall.Kill(syscall.Getpid(), syscall.SIGTERM) }()
		<-readyChan

		client, tr := keepAliveClient()
		defer tr.CloseIdleConnections()

		// --- Request 1: multipart with a recognizable boundary. ---
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		_ = mw.WriteField("marker", "value-one")
		assert.Nil(t, mw.Close())

		req1, _ := http.NewRequest(http.MethodPost,
			fmt.Sprintf("http://127.0.0.1:%s/multipart", s.GetListenPort()), &buf)
		req1.Header.Set("Content-Type", mw.FormDataContentType())
		resp1, _, err := doWithReuseFlag(client, req1)
		assert.Nil(t, err)
		_, _ = io.Copy(io.Discard, resp1.Body)
		_ = resp1.Body.Close()

		// --- Request 2: GET, no Content-Type, on the same connection. ---
		req2, _ := http.NewRequest(http.MethodGet,
			fmt.Sprintf("http://127.0.0.1:%s/probe", s.GetListenPort()), nil)
		resp2, reused, err := doWithReuseFlag(client, req2)
		assert.Nil(t, err)
		_, _ = io.Copy(io.Discard, resp2.Body)
		_ = resp2.Body.Close()
		assert.Equal(t, *reused, true)

		// Sanity for request 1.
		if s := req1Sanity.Load(); s == nil || *s != "value-one" {
			got := ""
			if s != nil {
				got = *s
			}
			t.Errorf("request 1 sanity: FormValue('marker') = %q (want 'value-one')", got)
			return
		}

		// On the GET, the handler must observe the generic
		// "no multipart form data" sentinel — meaning the ContentType
		// was correctly nil-reset and ParseMultipartForm short-circuited
		// at the Content-Type prefix check. Pre-fix, the leaked
		// "multipart/form-data; boundary=..." Content-Type would push
		// through to ReadForm, producing a parse-time error here.
		got := req2FormFileEr.Load()
		if got == nil {
			t.Errorf("GET handler did not capture an error from GetFormFile")
			return
		}
		if *got != "no multipart form data" && *got != "not a multipart form request" {
			t.Errorf("ContentType leaked: GetFormFile saw %q (expected 'no multipart form data' or 'not a multipart form request')", *got)
		}
	}()

	_ = s.Run()
	<-clientDone
}

// Compile-time guard that we still import context (used implicitly by
// httptrace.WithClientTrace via req.Context()). Remove if unused warnings
// appear during refactor.
var _ = context.Background
