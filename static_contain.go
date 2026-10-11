package rweb

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// errStaticEscape reports a static file whose resolved location is outside
// the served root. The handler turns every open error into a 404, so this
// never reaches a client; it only keeps the reason legible in a debugger.
var errStaticEscape = errors.New("rweb: static file resolves outside the served root")

// openContained opens rel (a path relative to rootDir, already checked
// lexically by the caller) such that the file opened is inside rootDir,
// with no window between the check and the open. It backs
// ServerOptions.StaticContainSymlinks.
//
// The containment comes from os.Root: the root directory is opened once,
// and the name is walked from that descriptor one component at a time
// (openat-style), with every symlink met on the way resolved and refused
// if it leaves the root. The check is the open, so a link swapped in by
// someone with write access to the tree cannot redirect a file that has
// already passed. The previous EvalSymlinks-then-Open approach could only
// narrow that window.
//
// os.Root is stricter than "resolves inside the root" in one way: it
// refuses every absolute symlink, even one that points back inside. Links
// written as `ln -s "$PWD/img/logo.png" logo.png` are common, and serving
// them is part of the option's contract, so a refused open gets a second
// try:
//
//	root.Open(rel) ── ok ─────────────────────────────────────► serve
//	      │
//	      └─ not found ──────────────────────────────────────► 404
//	      └─ other (escape, absolute link) ─┐
//	                                        ▼
//	   EvalSymlinks(root), EvalSymlinks(root/rel) → realRel
//	      │
//	      └─ fails, or realRel leaves the root ──────────────► 404
//	      └─ realRel inside ─► root.Open(realRel) ─ ok ──────► serve
//
// The second try stays race-free because it, too, opens through the same
// Root: realRel was link-free when EvalSymlinks walked it, and if a link is
// planted in it afterwards, root.Open follows that link only as far as the
// root allows. The worst a swap can do is turn a 200 into a 404.
//
// The Root is opened per request, not once per route, so the served path
// is resolved afresh each time, exactly as os.Open did: a root that is a
// "current -> releases/N" link moves with the link, instead of pinning the
// release directory that was current at registration. That costs an extra
// open and close per request, and only when the option is on.
//
// The returned *os.File is independent of the Root: closing the Root here
// does not close the file the caller streams from.
func openContained(rootDir, rel string) (*os.File, error) {
	root, err := os.OpenRoot(rootDir)
	if err != nil {
		return nil, err
	}
	defer root.Close()

	f, err := root.Open(rel)
	if err == nil {
		return f, nil
	}
	// Plain misses skip the fallback: it could only resolve the same
	// missing path again, at the cost of two EvalSymlinks walks per 404.
	if errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}

	// Fallback for in-root absolute links. The root is resolved too, since
	// OpenRoot followed any links in rootDir itself (a "current" release
	// link, macOS's /var -> /private/var), and realRel must be relative to
	// the directory the Root actually holds.
	rootReal, rootErr := filepath.EvalSymlinks(rootDir)
	candReal, candErr := filepath.EvalSymlinks(filepath.Join(rootDir, rel))
	if rootErr != nil || candErr != nil {
		return nil, err
	}
	realRel, relErr := filepath.Rel(rootReal, candReal)
	if relErr != nil || realRel == ".." ||
		strings.HasPrefix(realRel, ".."+string(filepath.Separator)) {
		return nil, errStaticEscape
	}
	return root.Open(realRel)
}
