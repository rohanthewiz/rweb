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
	StatusOK                  = 200
	StatusCreated             = 201
	StatusAccepted            = 202
	StatusNoContent           = 204
	StatusBadRequest          = 400
	StatusUnauthorized        = 401
	StatusForbidden           = 403
	StatusNotFound            = 404
	StatusInternalServerError = 500
	StatusNotImplemented      = 501
	StatusBadGateway          = 502
	StatusServiceUnavailable  = 503
)

const ( // HTTP messages
	HTTPBadRequest = "HTTP/1.1 400 Bad Request\r\n\r\n"
	HTTPBadMethod  = "BAD-METHOD / HTTP/1.1\r\n\r\n"
)
