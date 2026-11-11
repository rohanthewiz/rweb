//go:build unix

package rweb

import "syscall"

// processCPUTime returns the user plus system CPU time the process has used
// so far, in nanoseconds; ok is false if it cannot be read. benchKeepAlive
// reports it per request as cpu-ns/op (see there for why).
func processCPUTime() (ns int64, ok bool) {
	var ru syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &ru); err != nil {
		return 0, false
	}
	return ru.Utime.Nano() + ru.Stime.Nano(), true
}
