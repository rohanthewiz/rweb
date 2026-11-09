package rweb

import "testing"

// SetHeader and AddHeader are the only ways a header gets into a response,
// so they are where a value that would split the header block (CR, LF) or
// truncate it in some parsers (NUL) is neutralised, and where a key that is
// not an RFC 9110 token is refused. Header() must read back what will be
// sent.
func TestResponseHeaderSanitising(t *testing.T) {
	var res response

	res.SetHeader("Location", "/x\r\nSet-Cookie: sid=attacker")
	if got := res.Header("Location"); got != "/x  Set-Cookie: sid=attacker" {
		t.Errorf("SetHeader value: got %q", got)
	}

	res.AddHeader("X-Multi", "a\nb\rc\x00d")
	if got := res.Header("X-Multi"); got != "a b c d" {
		t.Errorf("AddHeader value: got %q", got)
	}

	// Updating an existing key goes through the same check.
	res.SetHeader("X-Multi", "e\r\nf")
	if got := res.Header("X-Multi"); got != "e  f" {
		t.Errorf("SetHeader update: got %q", got)
	}

	// Keys outside the token alphabet are dropped, by both methods.
	for _, key := range []string{"", "X-Bad Key", "X-Bad:Key", "X-Bad\r\nKey", "Bad\x00", "X-Ünï", "(comment)", "a/b"} {
		res.SetHeader(key, "v")
		res.AddHeader(key, "v")
	}
	for _, h := range res.headers {
		if h.Key != "Location" && h.Key != "X-Multi" {
			t.Errorf("invalid key %q was stored", h.Key)
		}
	}

	// Every tchar is accepted.
	weird := "!#$%&'*+-.^_`|~09azAZ"
	res.SetHeader(weird, "ok")
	if got := res.Header(weird); got != "ok" {
		t.Errorf("token key %q dropped", weird)
	}
}

// Clean values are the common case and must cost no allocation.
func TestResponseHeaderCleanValueNoAlloc(t *testing.T) {
	var res response
	res.AddHeader("X-Warm", "up") // give the slice capacity before measuring
	allocs := testing.AllocsPerRun(100, func() {
		res.headers = res.headers[:0]
		res.AddHeader("Set-Cookie", "sid=abc; Path=/; HttpOnly")
		res.SetHeader("Location", "/somewhere?a=1")
		res.SetHeader("Location", "/elsewhere")
	})
	if allocs != 0 {
		t.Errorf("clean SetHeader/AddHeader allocated %.1f times per run", allocs)
	}
}
