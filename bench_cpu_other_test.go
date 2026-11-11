//go:build !unix

package rweb

// processCPUTime is not available here; benchKeepAlive then reports
// ns/op alone.
func processCPUTime() (ns int64, ok bool) { return 0, false }
