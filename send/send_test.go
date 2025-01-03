package send_test

import (
	"testing"

	"git.akyoto.dev/go/assert"
	"github.com/rohanthewiz/rweb"
	"github.com/rohanthewiz/rweb/consts"
	"github.com/rohanthewiz/rweb/send"
)

func TestContentTypes(t *testing.T) {
	s := rweb.NewServer()

	s.Get("/css", func(ctx rweb.Context) error {
		return send.CSS(ctx, "body{}")
	})

	s.Get("/csv", func(ctx rweb.Context) error {
		return send.CSV(ctx, "ID;Name\n")
	})

	s.Get("/html", func(ctx rweb.Context) error {
		return send.HTML(ctx, "<html></html>")
	})

	s.Get("/js", func(ctx rweb.Context) error {
		return send.JS(ctx, "console.log(42)")
	})

	s.Get("/json", func(ctx rweb.Context) error {
		return send.JSON(ctx, struct{ Name string }{Name: "User 1"})
	})

	s.Get("/text", func(ctx rweb.Context) error {
		return send.Text(ctx, "Hello")
	})

	s.Get("/xml", func(ctx rweb.Context) error {
		return send.XML(ctx, "<xml></xml>")
	})

	tests := []struct {
		Method      string
		URL         string
		Body        string
		Status      int
		Response    string
		ContentType string
	}{
		{Method: consts.MethodGet, URL: "/css", Status: 200, Response: "body{}", ContentType: "text/css"},
		{Method: consts.MethodGet, URL: "/csv", Status: 200, Response: "ID;Name\n", ContentType: "text/csv"},
		{Method: consts.MethodGet, URL: "/html", Status: 200, Response: "<html></html>", ContentType: "text/html"},
		{Method: consts.MethodGet, URL: "/js", Status: 200, Response: "console.log(42)", ContentType: "text/javascript"},
		{Method: consts.MethodGet, URL: "/json", Status: 200, Response: "{\"Name\":\"User 1\"}\n", ContentType: "application/json"},
		{Method: consts.MethodGet, URL: "/text", Status: 200, Response: "Hello", ContentType: "text/plain"},
		{Method: consts.MethodGet, URL: "/xml", Status: 200, Response: "<xml></xml>", ContentType: "text/xml"},
	}

	for _, test := range tests {
		t.Run(test.URL, func(t *testing.T) {
			response := s.Request(test.Method, "http://example.com"+test.URL, nil, nil)
			assert.Equal(t, response.Status(), test.Status)
			assert.Equal(t, response.Header("Content-Type"), test.ContentType)
			assert.Equal(t, string(response.Body()), test.Response)
		})
	}
}
