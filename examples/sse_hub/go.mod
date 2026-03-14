module sse-hub-example

go 1.23.0

toolchain go1.23.4

replace github.com/rohanthewiz/rweb => ../..

require (
	github.com/rohanthewiz/element v0.5.6
	github.com/rohanthewiz/rweb v0.0.0-00010101000000-000000000000
)

require github.com/rohanthewiz/serr v1.3.0 // indirect
