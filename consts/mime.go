package consts

const (
	MIMETextPlain         = "text/plain"
	MIMEOctetStream       = "application/octet-stream"
	MIMEFormData          = "application/x-www-form-urlencoded"
	MIMEMultipartFormData = "multipart/form-data"
	MIMEJSON              = "application/json"
	MIMEXML               = "application/xml"
	MIMEHTML              = "text/html"
	MIMEPDF               = "application/pdf"
	MIMEPNG               = "image/png"
	MIMEJPEG              = "image/jpeg"
	MIMEGIF               = "image/gif"
	MIMESVG               = "image/svg"
	MIMEZIP               = "application/zip"
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
