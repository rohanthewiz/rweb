package rweb_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"syscall"
	"testing"

	"git.akyoto.dev/go/assert"
	"github.com/rohanthewiz/rweb"
	"github.com/rohanthewiz/rweb/consts"
)

func TestProxy(t *testing.T) {
	// US Server
	usTgtReadyChan := make(chan struct{}, 1)
	usTgt := rweb.NewServer(rweb.ServerOptions{Verbose: true, ReadyChan: usTgtReadyChan, Address: "localhost:"})
	usTgt.Get("/", func(ctx rweb.Context) error {
		return ctx.WriteString("Hi from US server root")
	})

	usTgt.Get("/us/proxy-incoming", func(ctx rweb.Context) error {
		return ctx.WriteHTML("<h1>Welcome to the US server landing!</h1>")
	})

	usTgt.Post("/us/proxy-incoming/status", func(ctx rweb.Context) error {
		data := map[string]string{
			"status": "success",
		}
		return ctx.WriteJSON(data)
	})

	// Euro Server
	euTgtReadyChan := make(chan struct{}, 1)
	euTgt := rweb.NewServer(rweb.ServerOptions{Verbose: true, ReadyChan: euTgtReadyChan, Address: "localhost:"})
	euTgt.Post("/", func(ctx rweb.Context) error {
		return ctx.WriteString("Hi from EU server root")
	})

	euTgt.Get("/proxy-incoming", func(ctx rweb.Context) error {
		return ctx.WriteHTML("<h1>Welcome to the EU server landing!</h1>")
	})

	euTgt.Post("/proxy-incoming/status", func(ctx rweb.Context) error {
		fv := ctx.Request().FormValue("abc")
		return ctx.WriteString(ctx.Request().GetPostValue("def") + fv)
	})

	go func() {
		_ = usTgt.Run() // run with high-order port
	}()

	go func() {
		_ = euTgt.Run()
	}()

	<-usTgtReadyChan // wait for US server
	<-euTgtReadyChan // wait for EUR server

	// Proxy
	pxyReadyChan := make(chan struct{}, 1)
	pxy := rweb.NewServer(rweb.ServerOptions{Verbose: true, ReadyChan: pxyReadyChan, Address: "localhost:"})

	pxy.Get("/", func(ctx rweb.Context) error {
		return ctx.WriteString("Hi from proxy server root")
	})

	// e.g. curl http://localhost:8080/via-proxy/us/status
	// 		- This will proxy to http://localhost:8081/usa/proxy-incoming/status
	// e.g. curl http://localhost:8080/via-proxy/us
	err := pxy.Proxy("/via-proxy/us",
		fmt.Sprintf("http://localhost:%s/proxy-incoming", usTgt.GetListenPort()), 1)
	if err != nil {
		log.Fatal(err)
	}

	// e.g. curl http://localhost:8080/via-proxy/eu/status
	err = pxy.Proxy("/via-proxy/eu",
		fmt.Sprintf("http://localhost:%s/proxy-incoming", euTgt.GetListenPort()), 2)
	if err != nil {
		log.Fatal(err)
	}

	go func() {
		defer func() {
			_ = syscall.Kill(syscall.Getpid(), syscall.SIGTERM)
		}()

		<-pxyReadyChan // wait for proxy

		// Proxy to US server status
		resp, err := http.Post(fmt.Sprintf("http://127.0.0.1:%s/via-proxy/us/status", pxy.GetListenPort()),
			string(consts.BytFormData), bytes.NewReader([]byte("abc=123&def=456")))
		assert.Nil(t, err)
		assert.Equal(t, resp.Status, consts.OK200)

		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()

		respMap := map[string]string{}
		_ = json.Unmarshal(body, &respMap)

		if st, ok := respMap["status"]; ok {
			assert.Equal(t, st, "success")
		} else {
			t.Error("Response map does not contain 'status'")
		}

		// Proxy to US server root
		resp, err = http.Get(fmt.Sprintf("http://127.0.0.1:%s/via-proxy/us", pxy.GetListenPort()))
		assert.Nil(t, err)
		assert.Equal(t, resp.Status, consts.OK200)
		body, _ = io.ReadAll(resp.Body)
		defer func() {
			_ = resp.Body.Close()
		}()
		assert.Equal(t, string(body), "<h1>Welcome to the US server landing!</h1>")

		// Proxy to EU
		resp, err = http.Post(fmt.Sprintf("http://127.0.0.1:%s/via-proxy/eu/status?abc=xyz", pxy.GetListenPort()),
			string(consts.BytFormData), bytes.NewReader([]byte("abc=xyz&def=456")))
		assert.Nil(t, err)
		assert.Equal(t, resp.Status, consts.OK200)

		body, _ = io.ReadAll(resp.Body)
		defer func() {
			_ = resp.Body.Close()
		}()
		assert.Equal(t, string(body), "456xyz")
	}()

	_ = pxy.Run()
}

/*// TestProxySkewed tests proxying to one server by default and another server based on a prefix
func TestProxySkewed(t *testing.T) {
	// Init US Server
	usTgtReadyChan := make(chan struct{}, 1)
	usTgt := rweb.NewServer(rweb.ServerOptions{
		Verbose: true, ReadyChan: usTgtReadyChan, Address: "localhost:", // run with high-order port
	})
	usTgt.Get("/", func(ctx rweb.Context) error {
		return ctx.WriteString("Hi from US server root")
	})

	usTgt.Get("/proxy-incoming", func(ctx rweb.Context) error {
		return ctx.WriteHTML("<h1>Welcome to the US server landing!</h1>")
	})

	usTgt.Post("/proxy-incoming/status", func(ctx rweb.Context) error {
		data := map[string]string{
			"status": "success",
		}
		return ctx.WriteJSON(data)
	})

	// Init Euro Server
	euTgtReadyChan := make(chan struct{}, 1)
	euTgt := rweb.NewServer(rweb.ServerOptions{Verbose: true, ReadyChan: euTgtReadyChan, Address: "localhost:"})
	euTgt.Post("/", func(ctx rweb.Context) error {
		return ctx.WriteString("Hi from EU server root")
	})

	euTgt.Get("/proxy-incoming", func(ctx rweb.Context) error {
		return ctx.WriteHTML("<h1>Welcome to the EU server landing!</h1>")
	})

	euTgt.Post("/proxy-incoming/status", func(ctx rweb.Context) error {
		fv := ctx.Request().FormValue("abc")
		return ctx.WriteString(ctx.Request().GetPostValue("def") + fv)
	})

	go func() {
		_ = usTgt.Run()
	}()

	go func() {
		_ = euTgt.Run()
	}()

	<-usTgtReadyChan // wait for US server
	<-euTgtReadyChan // wait for EUR server

	// Init Proxy
	pxyReadyChan := make(chan struct{}, 1)
	pxy := rweb.NewServer(rweb.ServerOptions{Verbose: true, ReadyChan: pxyReadyChan, Address: "localhost:"})

	// e.g. curl http://localhost:8080/via-proxy/us/status
	// 		- This will proxy to http://localhost:8081/usa/proxy-incoming/status
	// e.g. curl http://localhost:8080/via-proxy/us
	err := pxy.Proxy("/",
		fmt.Sprintf("http://localhost:%s/proxy-incoming", usTgt.GetListenPort()), 0)
	if err != nil {
		log.Fatal(err)
	}

	// e.g. curl http://localhost:8080/via-proxy/eu/status
	err = pxy.Proxy("/eu",
		fmt.Sprintf("http://localhost:%s/proxy-incoming", euTgt.GetListenPort()), 1)
	if err != nil {
		log.Fatal(err)
	}

	pxy.Get("/admin*", func(ctx rweb.Context) error {
		return ctx.WriteString("At proxy server admin")
	})

	go func() {
		defer func() {
			_ = syscall.Kill(syscall.Getpid(), syscall.SIGTERM)
		}()

		<-pxyReadyChan // wait for proxy

		// Proxy to US server status
		resp, err := http.Post(fmt.Sprintf("http://127.0.0.1:%s/status", pxy.GetListenPort()),
			string(consts.BytFormData), bytes.NewReader([]byte("abc=123&def=456")))
		assert.Nil(t, err)
		assert.Equal(t, resp.Status, consts.OK200)

		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()

		respMap := map[string]string{}
		_ = json.Unmarshal(body, &respMap)

		if st, ok := respMap["status"]; ok {
			assert.Equal(t, st, "success")
		} else {
			t.Error("Response map does not contain 'status'")
		}

		// Call a Non-proxied route
		resp, err = http.Post(fmt.Sprintf("http://127.0.0.1:%s/admin/home", pxy.GetListenPort()),
			string(consts.BytFormData), bytes.NewReader([]byte("abc=123&def=456")))
		assert.Nil(t, err)
		assert.Equal(t, resp.Status, consts.OK200)

		body, _ = io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		assert.Equal(t, string(body), "At proxy server admin")

		// Proxy to US server root
		resp, err = http.Get(fmt.Sprintf("http://127.0.0.1:%s", pxy.GetListenPort()))
		assert.Nil(t, err)
		assert.Equal(t, resp.Status, consts.OK200)
		body, _ = io.ReadAll(resp.Body)
		defer func() {
			_ = resp.Body.Close()
		}()
		assert.Equal(t, string(body), "<h1>Welcome to the US server landing!</h1>")

		// Proxy to EU
		resp, err = http.Post(fmt.Sprintf("http://127.0.0.1:%s/eu/status?abc=xyz", pxy.GetListenPort()),
			string(consts.BytFormData), bytes.NewReader([]byte("abc=xyz&def=456")))
		assert.Nil(t, err)
		assert.Equal(t, resp.Status, consts.OK200)

		body, _ = io.ReadAll(resp.Body)
		defer func() {
			_ = resp.Body.Close()
		}()
		assert.Equal(t, string(body), "456xyz")
	}()

	_ = pxy.Run()
}
*/
