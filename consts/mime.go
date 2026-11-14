package consts

const (
	MIMETextPlain         = "text/plain"
	MIMEOctetStream       = "application/octet-stream"
	MIMETextEventStream   = "text/event-stream"
	MIMEFormData          = "application/x-www-form-urlencoded"
	MIMEMultipartFormData = "multipart/form-data"
	MIMEJSON              = "application/json"
	MIMEXML               = "application/xml"
	MIMEHTML              = "text/html"
	MIMEPDF               = "application/pdf"
	MIMEPNG               = "image/png"
	MIMEJPEG              = "image/jpeg"
	MIMEGIF               = "image/gif"
	MIMESVG               = "image/svg+xml"
	MIMEZIP               = "application/zip"
	MIMECSS               = "text/css"
	MIMEJavaScript        = "text/javascript" // RFC 9239
	MIMECSV               = "text/csv"
)

// UTF-8 forms of the text types, sent by the text write helpers
// (WriteHTML, WriteText and their Bytes forms; rweb.HTML, Text, CSS, JS, CSV).
//
// Why a charset at all: a text/* type without one leaves the client to guess.
// A browser given an HTML page with no BOM and no <meta charset> falls back
// to a locale default (windows-1252 in most), so "café" written from a Go
// string (UTF-8 bytes) shows as "cafÃ©". The header value also outranks a
// <meta charset> in the page (only a BOM beats it), which is what a handler
// writing Go strings wants.
//
// The bare constants above keep their values on purpose: they are media
// types, not header values, and code builds on or compares against them
// (setFileHeaders in send.go appends the charset itself; middleware/critical
// matches "text/html" with strings.Contains). To test a received
// Content-Type, use mime.ParseMediaType rather than ==.
//
// Deliberately absent: JSON and XML. RFC 8259 §11 defines no charset
// parameter for application/json (JSON is UTF-8 by definition), and for
// application/xml a charset parameter would override the document's own
// encoding declaration (RFC 7303 §3.2), whose absence already means UTF-8.
const (
	CharsetUTF8 = "charset=utf-8"

	MIMETextPlainUTF8  = MIMETextPlain + "; " + CharsetUTF8
	MIMEHTMLUTF8       = MIMEHTML + "; " + CharsetUTF8
	MIMECSSUTF8        = MIMECSS + "; " + CharsetUTF8
	MIMEJavaScriptUTF8 = MIMEJavaScript + "; " + CharsetUTF8
	MIMECSVUTF8        = MIMECSV + "; " + CharsetUTF8
)

var (
	BytTextPlain          = []byte(MIMETextPlain)
	BytFormData           = []byte(MIMEFormData)
	BytJSONData           = []byte(MIMEJSON)
	BytDefaultContentType = []byte(MIMEOctetStream)
	BytMultipartFormData  = []byte(MIMEMultipartFormData)
	BytApplicationSlash   = []byte("application/")
	BytImageSVG           = []byte(MIMESVG)
	BytImageIcon          = []byte("image/x-icon")
	BytFontSlash          = []byte("font/")
	BytMultipartSlash     = []byte("multipart/")
	BytTextSlash          = []byte("text/")
)
