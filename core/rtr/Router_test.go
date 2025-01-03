package rtr_test

import (
	"strings"
	"testing"

	"git.akyoto.dev/go/assert"
	"github.com/rohanthewiz/rweb/core/rtr"
	"github.com/rohanthewiz/rweb/core/rtr/testdata"
)

func TestHello(t *testing.T) {
	r := rtr.New[string]()
	r.Add("GET", "/blog", "Blog")
	r.Add("GET", "/blog/post", "Blog post")

	data, params := r.Lookup("GET", "/blog")
	assert.Equal(t, len(params), 0)
	assert.Equal(t, data, "Blog")

	data, params = r.Lookup("GET", "/blog/post")
	assert.Equal(t, len(params), 0)
	assert.Equal(t, data, "Blog post")
}

func TestStatic(t *testing.T) {
	r := rtr.New[string]()
	r.Add("GET", "/hello", "Hello")
	r.Add("GET", "/world", "World")

	data, params := r.Lookup("GET", "/hello")
	assert.Equal(t, len(params), 0)
	assert.Equal(t, data, "Hello")

	data, params = r.Lookup("GET", "/world")
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
		data, params = r.Lookup("GET", path)
		assert.Equal(t, len(params), 0)
		assert.Equal(t, data, "")
	}
}

func TestParameter(t *testing.T) {
	r := rtr.New[string]()
	r.Add("GET", "/blog/:post", "Blog post")
	r.Add("GET", "/blog/:post/comments/:id", "Comment")

	data, params := r.Lookup("GET", "/blog/hello-world")
	assert.Equal(t, len(params), 1)
	assert.Equal(t, params[0].Key, "post")
	assert.Equal(t, params[0].Value, "hello-world")
	assert.Equal(t, data, "Blog post")

	data, params = r.Lookup("GET", "/blog/hello-world/comments/123")
	assert.Equal(t, len(params), 2)
	assert.Equal(t, params[0].Key, "post")
	assert.Equal(t, params[0].Value, "hello-world")
	assert.Equal(t, params[1].Key, "id")
	assert.Equal(t, params[1].Value, "123")
	assert.Equal(t, data, "Comment")
}

func TestWildcard(t *testing.T) {
	r := rtr.New[string]()
	r.Add("GET", "/", "Front page")
	r.Add("GET", "/users/:id", "Parameter")
	r.Add("GET", "/images/static", "Static")
	r.Add("GET", "/images/*path", "Wildcard")
	r.Add("GET", "/:post", "Blog post")
	r.Add("GET", "/*any", "Wildcard")
	r.Add("GET", "*root", "Root wildcard")

	data, params := r.Lookup("GET", "/")
	assert.Equal(t, len(params), 0)
	assert.Equal(t, data, "Front page")

	data, params = r.Lookup("GET", "/blog-post")
	assert.Equal(t, len(params), 1)
	assert.Equal(t, params[0].Key, "post")
	assert.Equal(t, params[0].Value, "blog-post")
	assert.Equal(t, data, "Blog post")

	data, params = r.Lookup("GET", "/users/42")
	assert.Equal(t, len(params), 1)
	assert.Equal(t, params[0].Key, "id")
	assert.Equal(t, params[0].Value, "42")
	assert.Equal(t, data, "Parameter")

	data, _ = r.Lookup("GET", "/users/42/test.txt")
	assert.Equal(t, data, "Wildcard")

	data, params = r.Lookup("GET", "/images/static")
	assert.Equal(t, len(params), 0)
	assert.Equal(t, data, "Static")

	data, params = r.Lookup("GET", "/images/ste")
	assert.Equal(t, len(params), 1)
	assert.Equal(t, params[0].Key, "path")
	assert.Equal(t, params[0].Value, "ste")
	assert.Equal(t, data, "Wildcard")

	data, params = r.Lookup("GET", "/images/sta")
	assert.Equal(t, len(params), 1)
	assert.Equal(t, params[0].Key, "path")
	assert.Equal(t, params[0].Value, "sta")
	assert.Equal(t, data, "Wildcard")

	data, params = r.Lookup("GET", "/images/favicon/256.png")
	assert.Equal(t, len(params), 1)
	assert.Equal(t, params[0].Key, "path")
	assert.Equal(t, params[0].Value, "favicon/256.png")
	assert.Equal(t, data, "Wildcard")

	data, params = r.Lookup("GET", "not-a-path")
	assert.Equal(t, len(params), 1)
	assert.Equal(t, params[0].Key, "root")
	assert.Equal(t, params[0].Value, "not-a-path")
	assert.Equal(t, data, "Root wildcard")
}

func TestMap(t *testing.T) {
	r := rtr.New[string]()
	r.Add("GET", "/hello", "Hello")
	r.Add("GET", "/world", "World")
	r.Add("GET", "/user/:user", "User")
	r.Add("GET", "/*path", "Path")
	r.Add("GET", "*root", "Root")

	r.Map(func(data string) string {
		return strings.Repeat(data, 2)
	})

	data, params := r.Lookup("GET", "/hello")
	assert.Equal(t, len(params), 0)
	assert.Equal(t, data, "HelloHello")

	data, params = r.Lookup("GET", "/world")
	assert.Equal(t, len(params), 0)
	assert.Equal(t, data, "WorldWorld")

	data, params = r.Lookup("GET", "/user/123")
	assert.Equal(t, len(params), 1)
	assert.Equal(t, data, "UserUser")

	data, params = r.Lookup("GET", "/test.txt")
	assert.Equal(t, len(params), 1)
	assert.Equal(t, data, "PathPath")

	data, params = r.Lookup("GET", "test.txt")
	assert.Equal(t, len(params), 1)
	assert.Equal(t, data, "RootRoot")
}

func TestMethods(t *testing.T) {
	methods := []string{
		"GET",
		"POST",
		"DELETE",
		"PUT",
		"PATCH",
		"HEAD",
		"CONNECT",
		"TRACE",
		"OPTIONS",
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
	r.Add("GET", "/hello", "Hello 1")

	data, params := r.Lookup("GET", "/hello")
	assert.Equal(t, len(params), 0)
	assert.Equal(t, data, "Hello 1")

	data, params = r.Lookup("GET", "/hello/")
	assert.Equal(t, len(params), 0)
	assert.Equal(t, data, "Hello 1")
}

func TestTrailingSlashOverwrite(t *testing.T) {
	r := rtr.New[string]()
	r.Add("GET", "/hello", "route 1")
	r.Add("GET", "/hello/", "route 2")
	r.Add("GET", "/:param", "route 3")
	r.Add("GET", "/:param/", "route 4")
	r.Add("GET", "/*any", "route 5")

	data, params := r.Lookup("GET", "/hello")
	assert.Equal(t, len(params), 0)
	assert.Equal(t, data, "route 1")

	data, params = r.Lookup("GET", "/hello/")
	assert.Equal(t, len(params), 0)
	assert.Equal(t, data, "route 2")

	data, params = r.Lookup("GET", "/param")
	assert.Equal(t, len(params), 1)
	assert.Equal(t, data, "route 3")

	data, params = r.Lookup("GET", "/param/")
	assert.Equal(t, len(params), 1)
	assert.Equal(t, data, "route 4")

	data, _ = r.Lookup("GET", "/wild/card/")
	assert.Equal(t, data, "route 5")
}

func TestOverwrite(t *testing.T) {
	r := rtr.New[string]()
	r.Add("GET", "/", "1")
	r.Add("GET", "/", "2")
	r.Add("GET", "/", "3")
	r.Add("GET", "/", "4")
	r.Add("GET", "/", "5")

	data, params := r.Lookup("GET", "/")
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
