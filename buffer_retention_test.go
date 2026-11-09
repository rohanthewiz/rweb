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
