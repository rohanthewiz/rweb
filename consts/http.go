package consts

const (
	MethodGet     = "GET"
	MethodPost    = "POST"
	MethodPut     = "PUT"
	MethodPatch   = "PATCH"
	MethodDelete  = "DELETE"
	MethodHead    = "HEAD"
	MethodOptions = "OPTIONS"
	MethodConnect = "CONNECT"
	MethodTrace   = "TRACE"
)

const ( // HTTP status codes
	StatusOK              = 200
	StatusCreated         = 201
	StatusAccepted        = 202
	StatusNoContent       = 204
	StatusResetContent    = 205
	StatusPartialContent  = 206
	StatusMultiStatus     = 207
	StatusAlreadyReported = 208
	StatusIMUsed          = 226

	StatusMultipleChoices  = 300
	StatusMovedPermanently = 301
	StatusFound            = 302
	StatusSeeOther         = 303
	StatusNotModified      = 304
	// StatusUseProxy            = 305
	StatusTemporaryRedirect = 307
	StatusPermanentRedirect = 308

	StatusBadRequest        = 400
	StatusUnauthorized      = 401
	StatusPaymentRequired   = 402
	StatusForbidden         = 403
	StatusNotFound          = 404
	StatusMethodNotAllowed  = 405
	StatusNotAcceptable     = 406
	StatusProxyAuthRequired = 407
	StatusRequestTimeout    = 408
	StatusConflict          = 409
	StatusGone              = 410

	StatusInternalServerError     = 500
	StatusNotImplemented          = 501
	StatusBadGateway              = 502
	StatusServiceUnavailable      = 503
	StatusGatewayTimeout          = 504
	StatusHTTPVersionNotSupported = 505
)

var StatusTextFromCode = map[int]string{
	StatusOK:              "OK",
	StatusCreated:         "Created",
	StatusAccepted:        "Accepted",
	StatusNoContent:       "No Content",
	StatusResetContent:    "Reset Content",
	StatusPartialContent:  "Partial Content",
	StatusMultiStatus:     "Multi-Status",
	StatusAlreadyReported: "Already Reported",
	StatusIMUsed:          "IM Used",

	StatusMultipleChoices:   "Multiple Choices",
	StatusMovedPermanently:  "Moved Permanently",
	StatusFound:             "Found",
	StatusSeeOther:          "See Other",
	StatusNotModified:       "Not Modified",
	StatusTemporaryRedirect: "Temporary Redirect",
	StatusPermanentRedirect: "Permanent Redirect",

	StatusBadRequest:        "Bad Request",
	StatusUnauthorized:      "Unauthorized",
	StatusPaymentRequired:   "Payment Required",
	StatusForbidden:         "Forbidden",
	StatusNotFound:          "Not Found",
	StatusMethodNotAllowed:  "Method Not Allowed",
	StatusNotAcceptable:     "Not Acceptable",
	StatusProxyAuthRequired: "Proxy Authentication Required",
	StatusRequestTimeout:    "Request Timeout",
	StatusConflict:          "Conflict",
	StatusGone:              "Gone",

	StatusInternalServerError:     "Internal Server Error",
	StatusNotImplemented:          "Not Implemented",
	StatusBadGateway:              "Bad Gateway",
	StatusServiceUnavailable:      "Service Unavailable",
	StatusGatewayTimeout:          "Gateway Timeout",
	StatusHTTPVersionNotSupported: "HTTP Version Not Supported",
}

const ( // HTTP messages
	HTTPBadRequest = "HTTP/1.1 400 Bad Request\r\n\r\n"
	HTTPBadMethod  = "BAD-METHOD / HTTP/1.1\r\n\r\n"

	ProtocolTCP = "tcp"
)
