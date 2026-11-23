package rweb

import (
	"errors"

	"github.com/rohanthewiz/logger"
	"github.com/rohanthewiz/serr"
)

// Logger is where the server writes its own log lines: errors it cannot
// return to anyone, warnings about misuse, and (when asked for) traces of
// what it is doing. Set it with ServerOptions.Logger or WithLogger; nil
// selects the default, which writes through github.com/rohanthewiz/logger.
//
// The shape is the leveled, key-value one that rohanthewiz/logger's package
// functions and the standard library's *slog.Logger share, so a
// *slog.Logger plugs in as it is:
//
//	s := rweb.NewServer(rweb.ServerOptions{Logger: slog.Default()})
//
// keyVals are alternating keys and values ("path", "/x", "error", err). The
// server always passes string keys and an even count. Methods may be called
// from any goroutine, concurrently.
//
// # What the server logs, and when
//
// The level says what kind of line it is; the server's own options decide
// whether the chatty kinds are produced at all. Errors and warnings are
// always produced. Info and Debug lines are produced only when Verbose or
// Debug asks for them, so a server is quiet by default whatever level the
// logger itself lets through (rohanthewiz/logger's InitLog defaults to
// debug, so leaving the gate to the logger alone would make every server
// chatty):
//
//	level  produced when   for
//	-----  --------------  ----------------------------------------------------
//	Error  always          a handler's error (default error handler), a
//	                       recovered panic, a WebSocket handler's error, a bad
//	                       cookie key, a static route refused at registration
//	Warn   always          a response header with an invalid name (dropped),
//	                       an accept error (retried), a ReadyChan with no buffer,
//	                       a request body that could not be read when the
//	                       handler was given an empty Body() or form value and
//	                       never checked Request().BodyErr() (refuseBody)
//	Info   Verbose         listening address, proxy routes and targets, SSE
//	                       stream start and end
//	Debug  Debug           routing trace, request bodies, and failures that are
//	                       the client's doing: it left mid-response, a stream
//	                       was cut short, a WebSocket upgrade was refused, a
//	                       request body could not be read (and the handler
//	                       was told, or never looked)
//
// A Debug line must also get past the logger's own level. With the default
// logger that is logrus's level, which is info until the app calls
// logger.InitLog (whose default level is debug) or logger.SetLogLevel.
type Logger interface {
	Debug(msg string, keyVals ...any)
	Info(msg string, keyVals ...any)
	Warn(msg string, keyVals ...any)
	Error(msg string, keyVals ...any)
}

// rohanLogger is the default Logger. rohanthewiz/logger is a set of package
// functions over the global logrus logger rather than a value, so this
// empty struct only gives those functions a method set. Configuring it
// (format, level, Slack or Teams hooks) is the app's business, through
// logger.InitLog; the server never calls InitLog or changes the level, since
// that state is process-wide and belongs to the app.
type rohanLogger struct{}

func (rohanLogger) Debug(msg string, keyVals ...any) { logger.Debug(msg, keyVals...) }
func (rohanLogger) Info(msg string, keyVals ...any)  { logger.Info(msg, keyVals...) }
func (rohanLogger) Warn(msg string, keyVals ...any)  { logger.Warn(msg, keyVals...) }

// Error sends a line that carries a serr error through logger.LogErr, and
// any other through logger.Error.
//
// A handler that returns serr.Wrap(err, "userId", id) has put its context
// in the error: attributes, and the location of each Wrap. logger.Error
// formats values with %v, which keeps only the error's text; LogErr
// unpacks the attributes into fields, which is the point of pairing
// rohanthewiz/logger with serr. LogErr makes the error's text the line's
// message, so the server's own message goes under "event":
//
//	rweb call:  Error("rweb: request failed", "code", "ABCD-1234", "error", serrErr)
//	logged as:  level=error msg=boom event="rweb: request failed" code=ABCD-1234
//	            userId=42 location="rweb/logging.go:NN -> app/handlers.go:31" ...
//
// LogErr also adds where it was called from, which here is this method (the
// first location and function); the Wrap locations after it say where the
// error came from.
//
// An error that wraps a serr error (fmt.Errorf("...: %w", serrErr)) is
// found with errors.As; the serr error is logged so its fields are kept,
// and the full text goes under the key the error was passed with.
func (rohanLogger) Error(msg string, keyVals ...any) {
	for i := 1; i < len(keyVals); i += 2 {
		err, ok := keyVals[i].(error)
		if !ok {
			continue
		}
		var ser *serr.SErr
		if !errors.As(err, &ser) {
			continue
		}
		// The other pairs ride along as attributes, in their order.
		rest := make([]any, 0, len(keyVals)+2)
		rest = append(rest, "event", msg)
		rest = append(rest, keyVals[:i-1]...)
		rest = append(rest, keyVals[i+1:]...)
		if error(ser) != err {
			rest = append(rest, keyVals[i-1], err.Error())
		}
		logger.LogErr(ser, rest...)
		return
	}
	logger.Error(msg, keyVals...)
}

// defaultLogger is what a nil ServerOptions.Logger resolves to, and the
// fallback for code that may run without a server (a context or response
// built outside NewServer in tests, an SSEHub with no Logger option).
var defaultLogger Logger = rohanLogger{}

// orDefaultLogger returns l, or the default logger when l is nil.
func orDefaultLogger(l Logger) Logger {
	if l == nil {
		return defaultLogger
	}
	return l
}

// ctxLogger returns the logger of the server ctx belongs to, or the
// default. For package-level code that is handed a Context rather than a
// *Server (the default error handler, the RequestInfo middleware). A
// Context from outside NewServer may have no server.
func ctxLogger(ctx Context) Logger {
	if ctx != nil {
		if s := ctx.Server(); s != nil {
			return orDefaultLogger(s.log)
		}
	}
	return defaultLogger
}
