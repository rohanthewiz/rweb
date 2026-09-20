package rweb

import "testing"

// isValidHostHeader is table-tested directly because the interesting inputs
// are the hostile ones, and most HTTP clients refuse to send them.
func TestIsValidHostHeader(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		// accepted
		{"", true}, // legal: target URI without an authority
		{"example.com", true},
		{"example.com:8080", true},
		{"example.com:", true}, // port = *DIGIT, so empty is in the grammar
		{"127.0.0.1:8080", true},
		{"my_host-1.internal", true},
		{"[::1]", true},
		{"[::1]:8080", true},
		{"[::ffff:192.0.2.1]:443", true},

		// rejected: anything that changes meaning when echoed
		{"example.com/path", false},
		{"example.com?x=1", false},
		{"example.com#frag", false},
		{"user@example.com", false},
		{"exa mple.com", false},
		{"example.com\tevil", false},
		{"example.com\x00", false},
		{"example.com\\evil", false},
		{"example.com, evil.com", false}, // two hosts folded into one line

		// rejected: structurally wrong
		{":8080", false},
		{"example.com:80:90", false},
		{"example.com:http", false},
		{"[::1", false},
		{"[]", false},
		{"[::1]8080", false},
		{"[::1%eth0]", false}, // zone IDs have no place in a Host header
		{"[::g]", false},
	}
	for _, c := range cases {
		if got := isValidHostHeader(c.in); got != c.want {
			t.Errorf("isValidHostHeader(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}
