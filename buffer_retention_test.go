package rweb

// Clean keeps a pooled context's buffers for the next request, but only up
// to maxRetainedBuf: one large upload or response must not pin its size for
// the life of a keep-alive connection, nor in the pool afterwards. Internal
// test so it can look at the capacities directly.

import (
	"bytes"
	"io"
	"testing"
)

func TestCleanReleasesOversizedBuffers(t *testing.T) {
	s := NewServer()
	big := bytes.Repeat([]byte("x"), 4*maxRetainedBuf)
	s.Post("/big", func(ctx Context) error {
		return ctx.Bytes(big)
	})

	ctx := s.newContext()
	ctx.request.body = append(ctx.request.body, big...)
	ctx.request.baseCtx = s.baseContext()
	s.handleRequest(ctx, "POST", "/big", io.Discard)
	// The status line and headers of a normal response are small; grow the
	// scratch buffer as a response with very large headers would.
	ctx.respBuf.Grow(4 * maxRetainedBuf)

	if cap(ctx.request.body) <= maxRetainedBuf || cap(ctx.response.body) <= maxRetainedBuf ||
		ctx.respBuf.Cap() <= maxRetainedBuf {
		t.Fatalf("test setup: buffers did not grow past the cap (%d, %d, %d)",
			cap(ctx.request.body), cap(ctx.response.body), ctx.respBuf.Cap())
	}

	ctx.Clean()

	if c := cap(ctx.request.body); c > maxRetainedBuf {
		t.Errorf("request body capacity %d kept after Clean, cap is %d", c, maxRetainedBuf)
	}
	if c := cap(ctx.response.body); c > maxRetainedBuf {
		t.Errorf("response body capacity %d kept after Clean, cap is %d", c, maxRetainedBuf)
	}
	if c := ctx.respBuf.Cap(); c > maxRetainedBuf {
		t.Errorf("respBuf capacity %d kept after Clean, cap is %d", c, maxRetainedBuf)
	}
	if len(ctx.request.body) != 0 || len(ctx.response.body) != 0 || ctx.respBuf.Len() != 0 {
		t.Errorf("buffers not empty after Clean")
	}
}

// Buffers within the cap are kept, so the common small request reuses them
// rather than allocating afresh.
func TestCleanKeepsSmallBuffers(t *testing.T) {
	s := NewServer()
	ctx := s.newContext()
	ctx.request.body = append(ctx.request.body, make([]byte, 8<<10)...)
	ctx.response.body = append(ctx.response.body, make([]byte, 8<<10)...)
	reqCap, respCap := cap(ctx.request.body), cap(ctx.response.body)

	ctx.Clean()

	if cap(ctx.request.body) != reqCap || cap(ctx.response.body) != respCap {
		t.Errorf("small buffers released: request %d -> %d, response %d -> %d",
			reqCap, cap(ctx.request.body), respCap, cap(ctx.response.body))
	}
}

// The header lists get the same treatment (N-069): a 1 MB head of tiny
// header lines grows request.headers to about 8 MB of entries, which Clean
// must not keep.
func TestCleanReleasesOversizedHeaderLists(t *testing.T) {
	s := NewServer()
	ctx := s.newContext()
	for range 100_000 {
		ctx.request.headers = append(ctx.request.headers, Header{Key: "a", Value: ""})
		ctx.response.headers = append(ctx.response.headers, Header{Key: "b", Value: ""})
	}

	ctx.Clean()

	if c := cap(ctx.request.headers); c > maxRetainedHeaders {
		t.Errorf("request header capacity %d kept after Clean, cap is %d", c, maxRetainedHeaders)
	}
	if c := cap(ctx.response.headers); c > maxRetainedHeaders {
		t.Errorf("response header capacity %d kept after Clean, cap is %d", c, maxRetainedHeaders)
	}
	if len(ctx.request.headers) != 0 || len(ctx.response.headers) != 0 {
		t.Errorf("header lists not empty after Clean")
	}
}

// A header list within the cap keeps its array, but with every entry zeroed,
// so the previous request's keys and values are not kept reachable past len.
func TestCleanKeepsSmallHeaderListsZeroed(t *testing.T) {
	s := NewServer()
	ctx := s.newContext()
	big := string(bytes.Repeat([]byte("v"), 1<<20))
	for range 20 {
		ctx.request.headers = append(ctx.request.headers, Header{Key: "X-Big", Value: big})
		ctx.response.headers = append(ctx.response.headers, Header{Key: "X-Big", Value: big})
	}
	reqCap, respCap := cap(ctx.request.headers), cap(ctx.response.headers)

	ctx.Clean()

	if cap(ctx.request.headers) != reqCap || cap(ctx.response.headers) != respCap {
		t.Errorf("small header lists released: request %d -> %d, response %d -> %d",
			reqCap, cap(ctx.request.headers), respCap, cap(ctx.response.headers))
	}
	for i, h := range ctx.request.headers[:reqCap] {
		if h != (Header{}) {
			t.Fatalf("request header slot %d still holds %q after Clean", i, h.Key)
		}
	}
	for i, h := range ctx.response.headers[:respCap] {
		if h != (Header{}) {
			t.Fatalf("response header slot %d still holds %q after Clean", i, h.Key)
		}
	}
}
