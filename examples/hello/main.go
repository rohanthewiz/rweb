package main

import (
	"fmt"
	"io"
	"log"
	"os"
	"time"

	"github.com/rohanthewiz/rweb"
	"github.com/rohanthewiz/rweb/send"
)

func main() {
	s := rweb.NewServer(rweb.ServerOptions{
		Address: "localhost:8080",
		Verbose: true, Debug: true,
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
		return send.CSS(ctx, "body{}")
	})

	s.Post("/post-form-data/:form_id", func(ctx rweb.Context) error {
		return ctx.WriteString("Posted - form_id: " + ctx.Request().Param("form_id"))
	})

	s.Get("/static/my.css", func(ctx rweb.Context) error {
		body, err := os.ReadFile("assets/my.css")
		if err != nil {
			return err
		}
		return send.File(ctx, "the.css", body)
	})

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

	log.Fatal(s.Run())
}
