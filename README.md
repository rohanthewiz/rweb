## Intro
**Note**: this is not yet production ready .

*This is a fork of Akyoto's [web](http://git.akyoto.dev/go/web)*.

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
PASS: TestBytes
PASS: TestString
PASS: TestError
PASS: TestErrorMultiple
PASS: TestRedirect
PASS: TestRequest
PASS: TestRequestHeader
PASS: TestRequestParam
PASS: TestWrite
PASS: TestWriteString
PASS: TestResponseCompression
PASS: TestResponseHeader
PASS: TestResponseHeaderOverwrite
PASS: TestPanic
PASS: TestRun
PASS: TestBadRequest
PASS: TestBadRequestHeader
PASS: TestBadRequestMethod
PASS: TestBadRequestProtocol
PASS: TestEarlyClose
PASS: TestUnavailablePort
coverage: 100.0% of statements
```

## Benchmarks

![wrk Benchmark](https://i.imgur.com/6cDeZVA.png)

## License

Please see the [license documentation](https://akyoto.dev/license).

## Copyright

© 2024 Eduard Urbach
© 2024 Rohan Allison
