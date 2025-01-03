package rtr_test

import (
	"testing"

	"github.com/rohanthewiz/rweb/core/rtr"
	"github.com/rohanthewiz/rweb/core/rtr/testdata"
)

func BenchmarkBlog(b *testing.B) {
	routes := testdata.Routes("testdata/blog.txt")
	r := rtr.New[string]()

	for _, route := range routes {
		r.Add(route.Method, route.Path, "")
	}

	b.Run("Len1-Param0", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			r.LookupNoAlloc("GET", "/", noop)
		}
	})

	b.Run("Len1-Param1", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			r.LookupNoAlloc("GET", "/:id", noop)
		}
	})
}

func BenchmarkGitHub(b *testing.B) {
	routes := testdata.Routes("testdata/github.txt")
	r := rtr.New[string]()

	for _, route := range routes {
		r.Add(route.Method, route.Path, "")
	}

	b.Run("Len7-Param0", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			r.LookupNoAlloc("GET", "/issues", noop)
		}
	})

	b.Run("Len7-Param1", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			r.LookupNoAlloc("GET", "/gists/:id", noop)
		}
	})

	b.Run("Len7-Param2", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			r.LookupNoAlloc("GET", "/repos/:owner/:repo/issues", noop)
		}
	})
}

// noop serves as an empty addParameter function.
func noop(string, string) {}
