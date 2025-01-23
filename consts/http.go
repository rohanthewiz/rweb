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
	HTTP  = "http"
	HTTPS = "https"
	HTTP1 = "HTTP/1.1"
	HTTP2 = "HTTP/2.0"
	OK200 = "200 OK"

	ProtocolTCP = "tcp"
	ProtocolUDP = "udp"

	HTTPBadRequest = "HTTP/1.1 400 Bad Request\r\n\r\n"
	HTTPBadMethod  = "BAD-METHOD / HTTP/1.1\r\n\r\n"
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
