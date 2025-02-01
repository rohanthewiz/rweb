package main

import (
	"fmt"
	"log"
	"os"
	"time"

	"github.com/rohanthewiz/rweb"
)

func main() {
	s := rweb.NewServer(rweb.ServerOptions{
		Address: "localhost:8081", // be sure to use a different port that the hello example
		Verbose: true, Debug: false,
		// TLS: rweb.TLSCfg{
		// 	UseTLS:   false,
		// 	KeyFile:  "certs/localhost.key",
		// 	CertFile: "certs/localhost.crt",
		// },
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
		fmt.Printf("%s - %s\n", ctx.Request().Method(), ctx.Request().Path())
		return ctx.Next()
	})

	s.Get("/", func(ctx rweb.Context) error {
		return ctx.WriteString("Welcome to the Admin Server\n")
	})

	s.Get("/incoming/admin/home", func(ctx rweb.Context) error {
		return ctx.WriteHTML("<h1>Welcome Admin's home!</h1>")
	})

	s.Get("/incoming/admin/status", func(ctx rweb.Context) error {
		data := map[string]string{
			"message": "Everything's good",
			"status":  "success",
		}
		return ctx.WriteJSON(data)
	})

	s.Post("/incoming/admin/post-form-data/:form_id", func(ctx rweb.Context) error {
		return ctx.WriteString("Posted to Admin - form_id: " + ctx.Request().Param("form_id") +
			"\n" + string(ctx.Request().Body()))
	})

	// We could do this for one specific file, but better to use s.StaticFiles to map a whole directory
	s.Get("/static/my.css", func(ctx rweb.Context) error {
		body, err := os.ReadFile("assets/my.css")
		if err != nil {
			return err
		}
		return rweb.File(ctx, "the.css", body)
	})

	log.Fatal(s.Run())
}
