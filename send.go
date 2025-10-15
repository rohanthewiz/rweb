package rweb

import (
	"encoding/json"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/rohanthewiz/rweb/consts"
)

// CSS sends the body with the content type set to `text/css`.
func CSS(ctx Context, body string) error {
	ctx.Response().SetHeader("Content-Type", "text/css")
	return ctx.WriteString(body)
}

// CSV sends the body with the content type set to `text/csv`.
func CSV(ctx Context, body string) error {
	ctx.Response().SetHeader("Content-Type", "text/csv")
	return ctx.WriteString(body)
}

// HTML sends the body with the content type set to `text/html`.
func HTML(ctx Context, body string) error {
	ctx.Response().SetHeader("Content-Type", "text/html")
	return ctx.WriteString(body)
}

// setFileHeaders determines the MIME type from the file extension and sets
// appropriate headers for the file type. This optimizes header usage by only
// including headers necessary for each specific file category.
// If modTime is provided (non-zero), sets Last-Modified header for better caching.
func setFileHeaders(ctx Context, filename string, modTime time.Time) {
	ext := strings.ToLower(filepath.Ext(filename))

	// Determine MIME type based on extension
	var mimeType string
	var isDownloadable bool
	var isTextBased bool // Text-based types get charset=utf-8

	switch ext {
	// Text-based formats (typically viewable in browser)
	case ".html", ".htm":
		mimeType = consts.MIMEHTML
		isTextBased = true
	case ".css":
		mimeType = "text/css"
		isTextBased = true
	case ".js":
		mimeType = "text/javascript"
		isTextBased = true
	case ".json":
		mimeType = consts.MIMEJSON
		isTextBased = true
	case ".xml":
		mimeType = consts.MIMEXML
		isTextBased = true
	case ".txt", ".log":
		mimeType = consts.MIMETextPlain
		isTextBased = true
	case ".csv":
		mimeType = "text/csv"
		isTextBased = true

	// Image formats (viewable in browser)
	case ".png":
		mimeType = consts.MIMEPNG
	case ".jpg", ".jpeg":
		mimeType = consts.MIMEJPEG
	case ".gif":
		mimeType = consts.MIMEGIF
	case ".svg":
		mimeType = consts.MIMESVG
		isTextBased = true // SVG is XML-based
	case ".ico":
		mimeType = "image/x-icon"
	case ".webp":
		mimeType = "image/webp"

	// Document formats (typically downloadable)
	case ".pdf":
		mimeType = consts.MIMEPDF
	case ".doc":
		mimeType = "application/msword"
		isDownloadable = true
	case ".docx":
		mimeType = "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
		isDownloadable = true
	case ".xls":
		mimeType = "application/vnd.ms-excel"
		isDownloadable = true
	case ".xlsx":
		mimeType = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
		isDownloadable = true
	case ".ppt":
		mimeType = "application/vnd.ms-powerpoint"
		isDownloadable = true
	case ".pptx":
		mimeType = "application/vnd.openxmlformats-officedocument.presentationml.presentation"
		isDownloadable = true

	// Archive formats (downloadable)
	case ".zip":
		mimeType = consts.MIMEZIP
		isDownloadable = true
	case ".tar":
		mimeType = "application/x-tar"
		isDownloadable = true
	case ".gz", ".gzip":
		mimeType = "application/gzip"
		isDownloadable = true
	case ".rar":
		mimeType = "application/vnd.rar"
		isDownloadable = true
	case ".7z":
		mimeType = "application/x-7z-compressed"
		isDownloadable = true

	// Audio formats
	case ".mp3":
		mimeType = "audio/mpeg"
	case ".wav":
		mimeType = "audio/wav"
	case ".ogg":
		mimeType = "audio/ogg"
	case ".m4a":
		mimeType = "audio/mp4"

	// Video formats
	case ".mp4":
		mimeType = "video/mp4"
	case ".webm":
		mimeType = "video/webm"
	case ".avi":
		mimeType = "video/x-msvideo"
	case ".mov":
		mimeType = "video/quicktime"

	// Font formats
	case ".woff":
		mimeType = "font/woff"
	case ".woff2":
		mimeType = "font/woff2"
	case ".ttf":
		mimeType = "font/ttf"
	case ".otf":
		mimeType = "font/otf"

	// Default to binary
	default:
		mimeType = consts.MIMEOctetStream
		isDownloadable = true
	}

	// Set Content-Type header with charset for text-based content
	if isTextBased {
		ctx.Response().SetHeader(consts.HeaderContentType, mimeType+"; charset=utf-8")
	} else {
		ctx.Response().SetHeader(consts.HeaderContentType, mimeType)
	}

	// Set Date header (RFC 7231 requirement for origin servers)
	ctx.Response().SetHeader(consts.HeaderDate, time.Now().UTC().Format(time.RFC1123))

	// Set Last-Modified header if modification time is provided
	// This enables conditional requests (If-Modified-Since) for better caching
	if !modTime.IsZero() {
		ctx.Response().SetHeader(consts.HeaderLastModified, modTime.UTC().Format(time.RFC1123))
	}

	// For downloadable files, add additional headers to prompt download
	if isDownloadable {
		ctx.Response().SetHeader(consts.HeaderContentDisposition,
			"attachment; filename="+url.QueryEscape(filename))
		ctx.Response().SetHeader("x-filename", url.QueryEscape(filename))
		// These headers help ensure proper download behavior across browsers
		ctx.Response().SetHeader("Content-Description", "File Transfer")
		ctx.Response().SetHeader("Content-Transfer-Encoding", "binary")
		ctx.Response().SetHeader(consts.HeaderExpires, "0")
		ctx.Response().SetHeader(consts.HeaderCacheControl, "must-revalidate")
		ctx.Response().SetHeader(consts.HeaderPragma, "public")
		ctx.Response().SetHeader(consts.HeaderAccessControlExposeHeaders, "x-filename")
	}
}

// File sends a file with appropriate headers based on the file extension.
// For viewable files (images, text, etc.), sets Content-Type only.
// For downloadable files (archives, documents, etc.), adds download headers.
// Text-based files (HTML, CSS, JSON, etc.) include charset=utf-8.
// Always sets Date header per RFC 7231.
// Key Design Decisions:
//
// 1. Smart categorization: Files are categorized by extension into viewable vs downloadable
// 2. Minimal headers: Only necessary headers are set for each file type
// 3. Proper charset handling: UTF-8 only for text-based content
// 4. Better caching: Date + optional Last-Modified enable proper HTTP caching
// 5. New function: FileWithModTime() for when you have actual file metadata
//
// Example Usage:
//
// // Basic file serving (no Last-Modified)
// rweb.File(ctx, "image.png", imageData)
func File(ctx Context, filename string, body []byte) error {
	setFileHeaders(ctx, filename, time.Time{})
	return ctx.Bytes(body)
}

// FileWithModTime sends a file with appropriate headers and Last-Modified time.
// The modTime enables browser caching via conditional requests (If-Modified-Since).
// For viewable files (images, text, etc.), sets Content-Type only.
// For downloadable files (archives, documents, etc.), adds download headers.
// Text-based files (HTML, CSS, JSON, etc.) include charset=utf-8.
// Always sets Date header per RFC 7231.
// Example Usage:
//
// // With file modification time (enables caching)
// fileInfo, _ := os.Stat("document.pdf")
// rweb.FileWithModTime(ctx, "document.pdf", pdfData, fileInfo.ModTime())
func FileWithModTime(ctx Context, filename string, body []byte, modTime time.Time) error {
	setFileHeaders(ctx, filename, modTime)
	return ctx.Bytes(body)
}

// JS sends the body with the content type set to `text/javascript`.
func JS(ctx Context, body string) error {
	ctx.Response().SetHeader("Content-Type", "text/javascript")
	return ctx.WriteString(body)
}

// JSON encodes the object in JSON format and sends it with the content type set to `application/json`.
func JSON(ctx Context, object any) error {
	ctx.Response().SetHeader("Content-Type", "application/json")
	return json.NewEncoder(ctx.Response()).Encode(object)
}

// Text sends the body with the content type set to `text/plain`.
func Text(ctx Context, body string) error {
	ctx.Response().SetHeader("Content-Type", "text/plain")
	return ctx.WriteString(body)
}

// XML sends the body with the content type set to `text/xml`.
func XML(ctx Context, body string) error {
	ctx.Response().SetHeader("Content-Type", "text/xml")
	return ctx.WriteString(body)
}
