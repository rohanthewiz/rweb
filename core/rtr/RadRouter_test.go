package rtr_test

import (
	"strings"
	"testing"

	"github.com/rohanthewiz/assert"
	"github.com/rohanthewiz/rweb/consts"
	"github.com/rohanthewiz/rweb/core/rtr"
	"github.com/rohanthewiz/rweb/core/rtr/testdata"
)

func TestHello(t *testing.T) {
	r := rtr.New[string]()
	r.Add(consts.MethodGet, "/blog", "Blog")
	r.Add(consts.MethodGet, "/blog/post", "Blog post")

	data, params := r.Lookup(consts.MethodGet, "/blog")
	assert.Equal(t, len(params), 0)
	assert.Equal(t, data, "Blog")

	data, params = r.Lookup(consts.MethodGet, "/blog/post")
	assert.Equal(t, len(params), 0)
	assert.Equal(t, data, "Blog post")
}

func TestStatic(t *testing.T) {
	r := rtr.New[string]()
	r.Add(consts.MethodGet, "/hello", "Hello")
	r.Add(consts.MethodGet, "/world", "World")

	data, params := r.Lookup(consts.MethodGet, "/hello")
	assert.Equal(t, len(params), 0)
	assert.Equal(t, data, "Hello")

	data, params = r.Lookup(consts.MethodGet, "/world")
	assert.Equal(t, len(params), 0)
	assert.Equal(t, data, "World")

	notFound := []string{
		"",
		"?",
		"/404",
		"/hell",
		"/hall",
		"/helloo",
	}

	for _, path := range notFound {
		data, params = r.Lookup(consts.MethodGet, path)
		assert.Equal(t, len(params), 0)
		assert.Equal(t, data, "")
	}
}

func TestParameter(t *testing.T) {
	r := rtr.New[string]()
	r.Add(consts.MethodGet, "/blog/:post", "Blog post")
	r.Add(consts.MethodGet, "/blog/:post/comments/:id", "Comment")

	data, params := r.Lookup(consts.MethodGet, "/blog/hello-world")
	assert.Equal(t, len(params), 1)
	assert.Equal(t, params[0].Key, "post")
	assert.Equal(t, params[0].Value, "hello-world")
	assert.Equal(t, data, "Blog post")

	data, params = r.Lookup(consts.MethodGet, "/blog/hello-world/comments/123")
	assert.Equal(t, len(params), 2)
	assert.Equal(t, params[0].Key, "post")
	assert.Equal(t, params[0].Value, "hello-world")
	assert.Equal(t, params[1].Key, "id")
	assert.Equal(t, params[1].Value, "123")
	assert.Equal(t, data, "Comment")
}

func TestWildcard(t *testing.T) {
	r := rtr.New[string]()
	r.Add(consts.MethodGet, "/", "Front page")
	r.Add(consts.MethodGet, "/users/:id", "Parameter")
	r.Add(consts.MethodGet, "/images/static", "Static")
	r.Add(consts.MethodGet, "/images/*path", "Wildcard")
	r.Add(consts.MethodGet, "/:post", "Blog post")
	r.Add(consts.MethodGet, "/*any", "Wildcard")
	r.Add(consts.MethodGet, "*root", "Root wildcard")

	data, params := r.Lookup(consts.MethodGet, "/")
	assert.Equal(t, len(params), 0)
	assert.Equal(t, data, "Front page")

	data, params = r.Lookup(consts.MethodGet, "/blog-post")
	assert.Equal(t, len(params), 1)
	assert.Equal(t, params[0].Key, "post")
	assert.Equal(t, params[0].Value, "blog-post")
	assert.Equal(t, data, "Blog post")

	data, params = r.Lookup(consts.MethodGet, "/users/42")
	assert.Equal(t, len(params), 1)
	assert.Equal(t, params[0].Key, "id")
	assert.Equal(t, params[0].Value, "42")
	assert.Equal(t, data, "Parameter")

	data, _ = r.Lookup(consts.MethodGet, "/users/42/test.txt")
	assert.Equal(t, data, "Wildcard")

	data, params = r.Lookup(consts.MethodGet, "/images/static")
	assert.Equal(t, len(params), 0)
	assert.Equal(t, data, "Static")

	data, params = r.Lookup(consts.MethodGet, "/images/ste")
	assert.Equal(t, len(params), 1)
	assert.Equal(t, params[0].Key, "path")
	assert.Equal(t, params[0].Value, "ste")
	assert.Equal(t, data, "Wildcard")

	data, params = r.Lookup(consts.MethodGet, "/images/sta")
	assert.Equal(t, len(params), 1)
	assert.Equal(t, params[0].Key, "path")
	assert.Equal(t, params[0].Value, "sta")
	assert.Equal(t, data, "Wildcard")

	data, params = r.Lookup(consts.MethodGet, "/images/favicon/256.png")
	assert.Equal(t, len(params), 1)
	assert.Equal(t, params[0].Key, "path")
	assert.Equal(t, params[0].Value, "favicon/256.png")
	assert.Equal(t, data, "Wildcard")

	data, params = r.Lookup(consts.MethodGet, "not-a-path")
	assert.Equal(t, len(params), 1)
	assert.Equal(t, params[0].Key, "root")
	assert.Equal(t, params[0].Value, "not-a-path")
	assert.Equal(t, data, "Root wildcard")
}

func TestMap(t *testing.T) {
	r := rtr.New[string]()
	r.Add(consts.MethodGet, "/hello", "Hello")
	r.Add(consts.MethodGet, "/world", "World")
	r.Add(consts.MethodGet, "/user/:user", "User")
	r.Add(consts.MethodGet, "/*path", "Path")
	r.Add(consts.MethodGet, "*root", "Root")

	r.Map(func(data string) string {
		return strings.Repeat(data, 2)
	})

	data, params := r.Lookup(consts.MethodGet, "/hello")
	assert.Equal(t, len(params), 0)
	assert.Equal(t, data, "HelloHello")

	data, params = r.Lookup(consts.MethodGet, "/world")
	assert.Equal(t, len(params), 0)
	assert.Equal(t, data, "WorldWorld")

	data, params = r.Lookup(consts.MethodGet, "/user/123")
	assert.Equal(t, len(params), 1)
	assert.Equal(t, data, "UserUser")

	data, params = r.Lookup(consts.MethodGet, "/test.txt")
	assert.Equal(t, len(params), 1)
	assert.Equal(t, data, "PathPath")

	data, params = r.Lookup(consts.MethodGet, "test.txt")
	assert.Equal(t, len(params), 1)
	assert.Equal(t, data, "RootRoot")
}

func TestMethods(t *testing.T) {
	methods := []string{
		consts.MethodGet,
		consts.MethodPost,
		consts.MethodDelete,
		consts.MethodPut,
		consts.MethodPatch,
		consts.MethodHead,
		consts.MethodConnect,
		consts.MethodTrace,
		consts.MethodOptions,
	}

	r := rtr.New[string]()

	for _, method := range methods {
		r.Add(method, "/", method)
	}

	for _, method := range methods {
		data, _ := r.Lookup(method, "/")
		assert.Equal(t, data, method)
	}
}

// A method with no tree is "no route", not a nil-tree panic, and only the
// exact string "GET" reaches the GET tree.
func TestUnknownMethod(t *testing.T) {
	r := rtr.New[string]()
	r.Add(consts.MethodGet, "/", "root")
	r.Add(consts.MethodGet, "/users/:id", "user")

	for _, method := range []string{"FOO", "GOT", "G", ""} {
		data, params := r.Lookup(method, "/users/42")
		assert.Equal(t, data, "")
		assert.Equal(t, len(params), 0)

		added := 0
		data = r.LookupNoAlloc(method, "/", func(string, string) { added++ })
		assert.Equal(t, data, "")
		assert.Equal(t, added, 0)
	}
}

func TestGitHub(t *testing.T) {
	routes := testdata.Routes("testdata/github.txt")
	r := rtr.New[string]()

	for _, route := range routes {
		r.Add(route.Method, route.Path, "octocat")
	}

	for _, route := range routes {
		data, _ := r.Lookup(route.Method, route.Path)
		assert.Equal(t, data, "octocat")

		data = r.LookupNoAlloc(route.Method, route.Path, func(string, string) {})
		assert.Equal(t, data, "octocat")
	}
}

func TestTrailingSlash(t *testing.T) {
	r := rtr.New[string]()
	r.Add(consts.MethodGet, "/hello", "Hello 1")

	data, params := r.Lookup(consts.MethodGet, "/hello")
	assert.Equal(t, len(params), 0)
	assert.Equal(t, data, "Hello 1")

	data, params = r.Lookup(consts.MethodGet, "/hello/")
	assert.Equal(t, len(params), 0)
	assert.Equal(t, data, "Hello 1")
}

func TestTrailingSlashOverwrite(t *testing.T) {
	r := rtr.New[string]()
	r.Add(consts.MethodGet, "/hello", "route 1")
	r.Add(consts.MethodGet, "/hello/", "route 2")
	r.Add(consts.MethodGet, "/:param", "route 3")
	r.Add(consts.MethodGet, "/:param/", "route 4")
	r.Add(consts.MethodGet, "/*any", "route 5")

	data, params := r.Lookup(consts.MethodGet, "/hello")
	assert.Equal(t, len(params), 0)
	assert.Equal(t, data, "route 1")

	data, params = r.Lookup(consts.MethodGet, "/hello/")
	assert.Equal(t, len(params), 0)
	assert.Equal(t, data, "route 2")

	data, params = r.Lookup(consts.MethodGet, "/param")
	assert.Equal(t, len(params), 1)
	assert.Equal(t, data, "route 3")

	data, params = r.Lookup(consts.MethodGet, "/param/")
	assert.Equal(t, len(params), 1)
	assert.Equal(t, data, "route 4")

	data, _ = r.Lookup(consts.MethodGet, "/wild/card/")
	assert.Equal(t, data, "route 5")
}

func TestOverwrite(t *testing.T) {
	r := rtr.New[string]()
	r.Add(consts.MethodGet, "/", "1")
	r.Add(consts.MethodGet, "/", "2")
	r.Add(consts.MethodGet, "/", "3")
	r.Add(consts.MethodGet, "/", "4")
	r.Add(consts.MethodGet, "/", "5")

	data, params := r.Lookup(consts.MethodGet, "/")
	assert.Equal(t, len(params), 0)
	assert.Equal(t, data, "5")
}

func TestInvalidMethod(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.FailNow()
		}
	}()

	r := rtr.New[string]()
	r.Add("?", "/hello", "Hello")
}

func TestMemoryUsage(t *testing.T) {
	escape := func(a any) {}

	result := testing.Benchmark(func(b *testing.B) {
		r := rtr.New[string]()
		escape(r)
	})

	t.Logf("%d bytes", result.MemBytes)
}

// Same contract for the hash router: only the exact string "GET" reaches
// the GET map, and an unknown or empty method finds nothing.
func TestHashRouterUnknownMethod(t *testing.T) {
	r := rtr.NewHashRouter[string]()
	r.Add(consts.MethodGet, "/x", "x")

	assert.Equal(t, r.Lookup(consts.MethodGet, "/x"), "x")
	for _, method := range []string{"FOO", "GOT", "G", ""} {
		assert.Equal(t, r.Lookup(method, "/x"), "")
	}
}
