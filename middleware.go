package rweb

import (
	"fmt"
	"time"
)

// RequestInfo is a middleware giving basic request / response stats
func RequestInfo(ctx Context) error {
	start := time.Now()

	defer func() {
		fmt.Printf("%sZ %s %q -> %d [%s]\n",
			time.Now().UTC().Format("20060102T150405"),
			ctx.Request().Method(), ctx.Request().Path(), ctx.Response().Status(), time.Since(start))
	}()

	return ctx.Next()
}
