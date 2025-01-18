## Intro
RWeb is a light, high performance web server for Go. It makes a great proxy, for example.

It is a fork of Akyoto's [web](http://git.akyoto.dev/go/web) with some additional features and changes.

## Caution
- This is still in beta - use with caution.
- Note that Multipart forms (file upload) is not currently supported.

> Imitation is the sincerest form of flattery.

All thanks and credit to Akyoto!

## Features

- High performance
- Low latency
- Scales incredibly well with the number of routes

## Installation

```shell
go get github.com/rohanthewiz/rweb
```

## Usage

```go
s := web.NewServer()

// Static route
s.Get("/", func(ctx web.Context) error {
	return ctx.String("Hello")
})

// Parameter route
s.Get("/blog/:post", func(ctx web.Context) error {
	return ctx.String(ctx.Request().Param("post"))
})

// Wildcard route
s.Get("/images/*file", func(ctx web.Context) error {
	return ctx.String(ctx.Request().Param("file"))
})

// Middleware
s.Use(func(ctx web.Context) error {
	start := time.Now()

	defer func() {
		fmt.Println(ctx.Request().Path(), time.Since(start))
	}()

	return ctx.Next()
})

s.Run(":8080")
```

## Tests

```

```

## Benchmarks

![wrk Benchmark](https://i.imgur.com/6cDeZVA.png)

## License

Please see the [license documentation](https://akyoto.dev/license).

## Copyright

© 2024 Eduard Urbach
© 2024 Rohan Allison
