package main

import (
	"fmt"
	"io"
	"log"
	"os"
	"time"

	"github.com/rohanthewiz/rweb"
)

func main() {
	s := rweb.NewServer(rweb.ServerOptions{
		Address: "localhost:8080",
		Verbose: true, Debug: false,
		TLS: rweb.TLSCfg{
			UseTLS:   false,
			KeyFile:  "certs/localhost.key",
			CertFile: "certs/localhost.crt",
		},
	})

	// Middleware
	s.Use(func(ctx rweb.Context) error {
		start := time.Now()

		defer func() {
			fmt.Println(ctx.Request().Method(), ctx.Request().Path(), time.Since(start))
		}()

		return ctx.Next()
	})

	s.Use(func(ctx rweb.Context) error {
		fmt.Println("In Middleware 2")
		return ctx.Next()
	})

	s.Get("/", func(ctx rweb.Context) error {
		return ctx.WriteString("Welcome\n")
	})

	// Similar URLs, one with a parameter, other without - works great!
	s.Get("/greet/:name", func(ctx rweb.Context) error {
		return ctx.WriteString("Hello " + ctx.Request().Param("name"))
	})
	s.Get("/greet/city", func(ctx rweb.Context) error {
		return ctx.WriteString("Hi big city!")
	})

	// Long URL is not a problem
	s.Get("/long/long/long/url/:thing", func(ctx rweb.Context) error {
		return ctx.WriteString("Hello " + ctx.Request().Param("thing"))
	})
	s.Get("/long/long/long/url/otherthing", func(ctx rweb.Context) error {
		return ctx.WriteString("Hey other thing!")
	})

	s.Get("/home", func(ctx rweb.Context) error {
		return ctx.WriteHTML("<h1>Welcome home</h1>")
	})

	s.Get("/some-json", func(ctx rweb.Context) error {
		data := map[string]string{
			"message": "Hello, World!",
			"status":  "success",
		}
		return ctx.WriteJSON(data)
	})

	s.Get("/css", func(ctx rweb.Context) error {
		return rweb.CSS(ctx, "body{}")
	})

	s.Post("/post-form-data/:form_id", func(ctx rweb.Context) error {
		return ctx.WriteString("Posted - form_id: " + ctx.Request().Param("form_id"))
	})

	// We could do this for one specific file, but better to use s.StaticFiles to map a whole directory
	s.Get("/static/my.css", func(ctx rweb.Context) error {
		body, err := os.ReadFile("assets/my.css")
		if err != nil {
			return err
		}
		return rweb.File(ctx, "the.css", body)
	})

	// e.g. http://localhost:8080/static/images/laptop.png
	s.StaticFiles("static/images/", "/assets/images", 2)

	// e.g. http://localhost:8080/css/my.css
	s.StaticFiles("/css/", "assets/css", 1)

	// e.g. http://localhost:8080/.well-known/some-file.txt
	s.StaticFiles("/.well-known/", "/", 0)

	// File upload
	s.Post("/upload", func(c rweb.Context) error {
		req := c.Request()

		// Get form fields
		name := req.FormValue("vehicle")
		fmt.Println("vehicle:", name)

		// Get uploaded file
		file, _, err := req.GetFormFile("file")
		if err != nil {
			return err
		}
		defer file.Close()

		// Save the file
		data, err := io.ReadAll(file)
		if err != nil {
			return err
		}
		err = os.WriteFile("uploaded_file.txt", data, 0666)
		if err != nil {
			return err
		}
		return nil
	})

	// Server Sent Events
	eventsChan := make(chan any, 8)
	eventsChan <- "event 1"
	eventsChan <- "event 2"
	eventsChan <- "event 3"
	eventsChan <- "event 4"
	eventsChan <- "event 5"

	s.Get("/events", s.SSEHandler(eventsChan))

	// PROXY
	// e.g. curl -X POST http://localhost:8080/admin/post-form-data/330 -d '{"hi": "there"}' -H 'Content-Type: application/json'
	//
	err := s.Proxy("/admin", "http://localhost:8081/incoming")
	if err != nil {
		log.Fatal(err)
	}
	// For proxy this route can be setup on the target server
	/*	s.Post("/admin/post-form-data/:form_id", func(ctx rweb.Context) error {
			return ctx.WriteString("Posted to Admin - form_id: " + ctx.Request().Param("form_id") +
				"\n" + string(ctx.Request().Body()))
		})
	*/

	log.Fatal(s.Run())
}
