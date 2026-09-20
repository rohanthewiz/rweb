package rweb_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/rohanthewiz/rweb"
	"github.com/rohanthewiz/rweb/consts"
)

// staticFixture lays out two unrelated trees and makes one of them the
// working directory:
//
//	<cwd>/                      the process working directory
//	    safe/allowed.txt        "from-cwd"     served by the relative tests
//	    top.txt                 "cwd-top"
//	<abs>/                      somewhere else entirely
//	    root/allowed.txt        "from-abs"     served by the absolute tests
//	    root/sub/deep.txt       "abs-deep"
//	    secret.txt              "off-root-secret"   sibling of the served root
//
// The same relative name (allowed.txt) exists in both with different bodies,
// so a response shows which tree it came from — a wrong anchor cannot pass
// by coincidence.
func staticFixture(t *testing.T) (absRoot string) {
	t.Helper()
	write := func(p, body string) {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cwd, abs := t.TempDir(), t.TempDir()
	write(filepath.Join(cwd, "safe", "allowed.txt"), "from-cwd")
	write(filepath.Join(cwd, "top.txt"), "cwd-top")
	write(filepath.Join(abs, "root", "allowed.txt"), "from-abs")
	write(filepath.Join(abs, "root", "sub", "deep.txt"), "abs-deep")
	write(filepath.Join(abs, "secret.txt"), "off-root-secret")

	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(cwd); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })
	return filepath.Join(abs, "root")
}

func get(s *rweb.Server, url string) (int, string) {
	r := s.Request(consts.MethodGet, url, nil, nil)
	return int(r.Status()), string(r.Body())
}

func TestStaticFilesAbsServesFromTheAbsoluteDir(t *testing.T) {
	root := staticFixture(t)
	s := rweb.NewServer()
	s.StaticFilesAbs("/files/", root, 1)

	for url, want := range map[string]string{
		"/files/allowed.txt":  "from-abs", // not "from-cwd": the cwd has a file of the same name
		"/files/sub/deep.txt": "abs-deep",
	} {
		if status, body := get(s, url); status != 200 || body != want {
			t.Errorf("GET %s = %d %q, want 200 %q", url, status, body, want)
		}
	}
	if status, _ := get(s, "/files/missing.txt"); status != 404 {
		t.Errorf("missing file: status %d, want 404", status)
	}
	if status, _ := get(s, "/files/sub"); status == 200 {
		t.Error("a directory was served")
	}
}

// Token stripping must behave as it does in StaticFiles: with nothing
// stripped, the request's own prefix is part of the path on disk.
func TestStaticFilesAbsTokenStripping(t *testing.T) {
	root := staticFixture(t)
	s := rweb.NewServer()
	s.StaticFilesAbs("/sub/", root, 0) // /sub/deep.txt -> <root>/sub/deep.txt
	if status, body := get(s, "/sub/deep.txt"); status != 200 || body != "abs-deep" {
		t.Errorf("strip 0: got %d %q", status, body)
	}
}

// The same attack list StaticFiles is tested with, against the absolute
// mode, whose sibling secret.txt must stay unreachable.
func TestStaticFilesAbsPathTraversal(t *testing.T) {
	root := staticFixture(t)
	s := rweb.NewServer()
	s.StaticFilesAbs("/files/", root, 1)

	for _, url := range []string{
		"/files/../secret.txt",
		"/files/%2E%2E/secret.txt",
		"/files/%2e%2e/secret.txt",
		"/files/sub/../../secret.txt",
		"/files/%2e%2e%2fsecret.txt",
		"/files/allowed.txt%00.evil",
		"/files//etc/passwd",
		"/files/%2fetc/passwd",
	} {
		r := s.Request(consts.MethodGet, url, nil, nil)
		if int(r.Status()) == 200 {
			t.Errorf("traversal %q succeeded with 200", url)
		}
		if bytes.Contains(r.Body(), []byte("off-root-secret")) {
			t.Errorf("traversal %q leaked the off-root file", url)
		}
	}
}

// A relative targetDir is a caller mistake. It must not be quietly served
// relative to the cwd — that is StaticFiles' job — so no route is registered.
func TestStaticFilesAbsRefusesARelativeDir(t *testing.T) {
	staticFixture(t)
	s := rweb.NewServer()
	s.StaticFilesAbs("/files/", "safe", 1)
	if status, body := get(s, "/files/allowed.txt"); status == 200 {
		t.Errorf("relative dir was served: %q", body)
	}
}

func TestGroupStaticFilesAbs(t *testing.T) {
	root := staticFixture(t)
	s := rweb.NewServer()
	s.Group("/admin").StaticFilesAbs("/files/", root, 2) // strip "admin" and "files"
	if status, body := get(s, "/admin/files/allowed.txt"); status != 200 || body != "from-abs" {
		t.Errorf("got %d %q", status, body)
	}
}

// StaticFiles' existing contract, pinned so that adding StaticFilesAbs — or
// any later "smarter" handling of absolute paths — cannot change it: the
// target is relative to the working directory with or without a leading "/".
func TestStaticFilesStaysRelativeToCWD(t *testing.T) {
	root := staticFixture(t)

	cases := []struct {
		name, reqDir, targetDir string
		strip                   int
		url, want               string
	}{
		{"plain relative dir", "/a/", "safe", 1, "/a/allowed.txt", "from-cwd"},
		{"leading slash is still relative (README example 1)", "/b/", "/safe", 1, "/b/allowed.txt", "from-cwd"},
		{"dot-relative dir", "/c/", "./safe", 1, "/c/allowed.txt", "from-cwd"},
		{`"/" means the working directory (README example 3)`, "/safe/", "/", 0, "/safe/allowed.txt", "from-cwd"},
	}
	for _, c := range cases {
		s := rweb.NewServer()
		s.StaticFiles(c.reqDir, c.targetDir, c.strip)
		if status, body := get(s, c.url); status != 200 || body != c.want {
			t.Errorf("%s: GET %s = %d %q, want 200 %q", c.name, c.url, status, body, c.want)
		}
	}

	// And the converse: handing StaticFiles a real absolute path does not
	// reach that path. It stays anchored at the cwd, where no such
	// directory exists, so the file that does exist at the absolute
	// location is not served.
	s := rweb.NewServer()
	s.StaticFiles("/d/", root, 1)
	if status, body := get(s, "/d/allowed.txt"); status == 200 {
		t.Errorf("StaticFiles followed an absolute path and served %q", body)
	}
}
