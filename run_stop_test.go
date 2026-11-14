package rweb_test

import (
	"context"
	"time"

	"github.com/rohanthewiz/rweb"
)

// stopRun ends servers a test started with s.Run(), by calling Shutdown on
// each.
//
// These tests used to stop Run by sending SIGTERM to the test process, which
// Run answers by shutting its server down. That had two costs:
//   - The package's tests did not build on Windows, which has no
//     syscall.Kill and no SIGTERM to send a process (N-071).
//   - A SIGTERM that arrives while no Run is listening (a test that failed
//     early, or a server that never started) kills the whole test binary.
//
// One signal also stopped every Run in the process at once, so a test with
// several servers (an upstream behind a Proxy, say) passes all of them here.
//
// How Run ends this way (runUntilSignal, Server.go): Shutdown closes the
// listener, serve returns ErrServerClosed, and Run takes that to mean a
// Shutdown called directly is in charge of draining, so it returns at once.
// With SIGTERM, Run did the draining itself before returning. So Run may now
// return while this call is still draining; a test that must see the server
// fully drained waits for stopRun, not for Run.
//
//	client goroutine            test goroutine
//	────────────────            ──────────────
//	<-readyChan                 s.Run() ─┐ blocked in serve
//	requests, asserts                    │
//	stopRun(s) ─► Shutdown ─► listener closed ─► Run returns
//	           ◄─ drained (or 10 s timeout, then forced)
//
// A Shutdown after ReadyChan is never undone by Run's re-arming
// (beginServing runs before serve fires ReadyChan, and skips the re-arm once
// a Shutdown has begun).
//
// The error is ignored, as Run's was in these tests: past the timeout
// Shutdown has closed whatever remained, which is all a test needs. It must
// not go to t, since the test function may already have returned.
func stopRun(servers ...*rweb.Server) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, s := range servers {
		_ = s.Shutdown(ctx)
	}
}
