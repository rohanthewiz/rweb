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

const (
	HTTP   = "http"
	HTTPS  = "https"
	HTTP1  = "HTTP/1.1"
	HTTP10 = "HTTP/1.0"
	HTTP2  = "HTTP/2.0"
	OK200  = "200 OK"

	ProtocolTCP     = "tcp"
	ProtocolUDP     = "udp"
	SchemeDelimiter = "://"
	Localhost       = "localhost"

	HTTPBadRequest = "HTTP/1.1 400 Bad Request\r\n\r\n"
	// HTTPBadMethod is a request line with a method no router serves; tests
	// send it. It is not a response: the server answers HTTPNotImplemented.
	HTTPBadMethod = "BAD-METHOD / HTTP/1.1\r\n\r\n"
	// HTTPNotImplemented answers a request method the server does not
	// recognize (RFC 9110 §15.6.2). Content-Length: 0 frames the empty body;
	// the connection is closed after it.
	HTTPNotImplemented  = "HTTP/1.1 501 Not Implemented\r\nContent-Length: 0\r\nConnection: close\r\n\r\n"
	HTTPInternalError   = "HTTP/1.1 500 Internal Server Error\r\nConnection: close\r\n\r\n"
	HTTPPayloadTooLarge = "HTTP/1.1 413 Request Entity Too Large\r\nConnection: close\r\n\r\n"
	// HTTPRequestTimeout answers a request whose head (or body, under
	// ReadTimeout) did not arrive in time; the connection is closed after it.
	HTTPRequestTimeout = "HTTP/1.1 408 Request Timeout\r\nContent-Length: 0\r\nConnection: close\r\n\r\n"
	// HTTPHeaderTooLarge answers a request whose line and headers exceed
	// MaxHeaderBytes (RFC 6585 §5); the connection is closed after it.
	HTTPHeaderTooLarge = "HTTP/1.1 431 Request Header Fields Too Large\r\nContent-Length: 0\r\nConnection: close\r\n\r\n"
	// HTTPExpectationFailed answers an Expect header other than
	// 100-continue (RFC 9110 §10.1.1); the connection is closed after it.
	HTTPExpectationFailed = "HTTP/1.1 417 Expectation Failed\r\nContent-Length: 0\r\nConnection: close\r\n\r\n"

	// ExpectContinue is the one Expect value defined (RFC 9110 §10.1.1),
	// matched case-insensitively.
	ExpectContinue = "100-continue"

	// ConnectionClose is the Connection option that ends a connection after
	// the current response (RFC 9112 §9.6).
	ConnectionClose = "close"
)

var ( // HTTP messages
	BytHTTP               = []byte(HTTP)
	BytHTTPS              = []byte(HTTPS)
	BytHTTP1              = []byte(HTTP1)
	BytResponseContinue   = []byte("HTTP/1.1 100 Continue\r\n\r\n")
	BytExpect             = []byte(HeaderExpect)
	BytConnection         = []byte(HeaderConnection)
	BytContentLength      = []byte(HeaderContentLength)
	BytContentType        = []byte(HeaderContentType)
	BytDate               = []byte(HeaderDate)
	BytHost               = []byte(HeaderHost)
	BytReferer            = []byte(HeaderReferer)
	BytServer             = []byte(HeaderServer)
	BytTransferEncoding   = []byte(HeaderTransferEncoding)
	BytContentEncoding    = []byte(HeaderContentEncoding)
	BytAcceptEncoding     = []byte(HeaderAcceptEncoding)
	BytUserAgent          = []byte(HeaderUserAgent)
	BytCookie             = []byte(HeaderCookie)
	BytSetCookie          = []byte(HeaderSetCookie)
	BytLocation           = []byte(HeaderLocation)
	BytIfModifiedSince    = []byte(HeaderIfModifiedSince)
	BytLastModified       = []byte(HeaderLastModified)
	BytAcceptRanges       = []byte(HeaderAcceptRanges)
	BytRange              = []byte(HeaderRange)
	BytContentRange       = []byte(HeaderContentRange)
	BytAuthorization      = []byte(HeaderAuthorization)
	BytTE                 = []byte(HeaderTE)
	BytTrailer            = []byte(HeaderTrailer)
	BytMaxForwards        = []byte(HeaderMaxForwards)
	BytProxyConnection    = []byte(HeaderProxyConnection)
	BytProxyAuthenticate  = []byte(HeaderProxyAuthenticate)
	BytProxyAuthorization = []byte(HeaderProxyAuthorization)
	BytWWWAuthenticate    = []byte(HeaderWWWAuthenticate)
	BytVary               = []byte(HeaderVary)

	BytCookieExpires        = []byte("expires")
	BytCookieDomain         = []byte("domain")
	BytCookiePath           = []byte("path")
	BytCookieHTTPOnly       = []byte("HttpOnly")
	BytCookieSecure         = []byte("secure")
	BytCookiePartitioned    = []byte("Partitioned")
	BytCookieMaxAge         = []byte("max-age")
	BytCookieSameSite       = []byte("SameSite")
	BytCookieSameSiteLax    = []byte("Lax")
	BytCookieSameSiteStrict = []byte("Strict")
	BytCookieSameSiteNone   = []byte("None")
)
