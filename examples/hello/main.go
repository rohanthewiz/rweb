package main

import (
	"fmt"
	"log"
	"os"
	"time"

	"github.com/rohanthewiz/rweb"
	"github.com/rohanthewiz/rweb/send"
)

func main() {
	s := rweb.NewServer(rweb.ServerOptions{Verbose: true})

	// Middleware
	s.Use(func(ctx rweb.Context) error {
		start := time.Now()

		defer func() {
			fmt.Println(ctx.Request().Method(), ctx.Request().Path(), time.Since(start))
		}()

		return ctx.Next()
	})

	s.Use(func(ctx rweb.Context) error {
		fmt.Println("Middleware 2")
		return ctx.Next()
	})

	s.Get("/", func(ctx rweb.Context) error {
		return ctx.String("Welcome home\n")
	})

	s.Get("/css", func(ctx rweb.Context) error {
		return send.CSS(ctx, "body{}")
	})

	s.Post("/post-form-data/:form_id", func(ctx rweb.Context) error {
		return ctx.String("Posted - form_id: " + ctx.Request().Param("form_id"))
	})

	s.Get("/static/my.css", func(ctx rweb.Context) error {
		body, err := os.ReadFile("assets/my.css")
		if err != nil {
			return err
		}
		return send.File(ctx, "the.css", body)
	})

	// Similar URLs, one with a parameter, other without - works great!
	s.Get("/greet/:name", func(ctx rweb.Context) error {
		return ctx.String("Hello " + ctx.Request().Param("name"))
	})
	s.Get("/greet/city", func(ctx rweb.Context) error {
		return ctx.String("Hi big city!")
	})

	// Long URL is not a problem
	s.Get("/long/long/long/url/:thing", func(ctx rweb.Context) error {
		return ctx.String("Hello " + ctx.Request().Param("thing"))
	})
	s.Get("/long/long/long/url/otherthing", func(ctx rweb.Context) error {
		return ctx.String("Hey other thing!")
	})

	// fmt.Println("Launching server")
	log.Fatal(s.Run(":8080"))
}
