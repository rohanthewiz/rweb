# router

HTTP router based on radix trees.
Router for internal use only

## Features

- Efficient lookup
- Generic data structure
- Zero dependencies (excluding tests)

## Usage

```go
router := router.New[string]()

// Static routes
router.Add(consts.MethodGet, "/hello", "...")
router.Add(consts.MethodGet, "/world", "...")

// Parameter routes
router.Add(consts.MethodGet, "/users/:id", "...")
router.Add(consts.MethodGet, "/users/:id/comments", "...")

// Wildcard routes
router.Add(consts.MethodGet, "/images/*path", "...")

// Simple lookup
data, params := router.Lookup(consts.MethodGet, "/users/42")
fmt.Println(data, params)

// Efficient lookup
data := router.LookupNoAlloc(consts.MethodGet, "/users/42", func(key string, value string) {
	fmt.Println(key, value)
})
```

## Tests

```
PASS: TestStatic
PASS: TestParameter
PASS: TestWildcard
PASS: TestMap
PASS: TestMethods
PASS: TestGitHub
PASS: TestTrailingSlash
PASS: TestTrailingSlashOverwrite
PASS: TestOverwrite
PASS: TestInvalidMethod
coverage: 100.0% of statements
```

## Benchmarks

```
BenchmarkBlog/Len1-Param0-12            211814850                5.646 ns/op           0 B/op          0 allocs/op
BenchmarkBlog/Len1-Param1-12            132838722                8.978 ns/op           0 B/op          0 allocs/op
BenchmarkGitHub/Len7-Param0-12          84768382                14.14 ns/op            0 B/op          0 allocs/op
BenchmarkGitHub/Len7-Param1-12          55290044                20.74 ns/op            0 B/op          0 allocs/op
BenchmarkGitHub/Len7-Param2-12          26057244                46.08 ns/op            0 B/op          0 allocs/op
```

## License

Please see the [license documentation](https://akyoto.dev/license).

## Copyright

© 2023 Eduard Urbach
© 2024 Rohan Allison
