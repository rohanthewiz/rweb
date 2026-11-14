package rweb

import (
	"time"
)

// RequestInfo is a middleware giving basic request / response stats: one
// info-level line per request, through the server's Logger (method, path,
// status, duration). The logger stamps the time, so the line carries none
// of its own. It is logged whatever ServerOptions.Verbose says: adding the
// middleware is the request for it.
func RequestInfo(ctx Context) error {
	start := time.Now()

	defer func() {
		ctxLogger(ctx).Info("rweb: request served", "method", ctx.Request().Method(),
			"path", ctx.Request().Path(), "status", ctx.Response().Status(),
			"duration", time.Since(start).String())
	}()

	return ctx.Next()
}
