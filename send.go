package rweb

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
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
	case ".js", ".mjs":
		// .mjs is the standard extension for ES modules; both share the JS MIME type per RFC 9239
		mimeType = "text/javascript"
		isTextBased = true
	case ".json":
		mimeType = consts.MIMEJSON
		isTextBased = true
	case ".map":
		// Source maps are JSON; serving them with a JSON type lets DevTools fetch them cleanly
		mimeType = consts.MIMEJSON
		isTextBased = true
	case ".xml":
		mimeType = consts.MIMEXML
		isTextBased = true
	case ".yaml", ".yml":
		// RFC 9512 (Feb 2024) registered application/yaml as the canonical type
		mimeType = "application/yaml"
		isTextBased = true
	case ".md":
		// RFC 7763
		mimeType = "text/markdown"
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
	case ".avif":
		mimeType = "image/avif"
	case ".bmp":
		mimeType = "image/bmp"

	// WebAssembly — browsers' streaming compile (instantiateStreaming) requires this exact type
	case ".wasm":
		mimeType = "application/wasm"

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
	case ".flac":
		mimeType = "audio/flac"
	case ".aac":
		mimeType = "audio/aac"

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

	// Defense-in-depth: keep browsers from MIME-sniffing user-controlled static
	// content into a script type even when our Content-Type is exactly right.
	ctx.Response().SetHeader("X-Content-Type-Options", "nosniff")

	// Set Date header (RFC 7231 requirement for origin servers).
	// http.TimeFormat is RFC1123 with the zone hardcoded to "GMT" — HTTP
	// dates MUST use GMT; time.RFC1123 with a UTC time renders "UTC",
	// which standard parsers (incl. http.ParseTime) reject.
	ctx.Response().SetHeader(consts.HeaderDate, time.Now().UTC().Format(http.TimeFormat))

	// Set Last-Modified header if modification time is provided
	// This enables conditional requests (If-Modified-Since) for better caching
	if !modTime.IsZero() {
		ctx.Response().SetHeader(consts.HeaderLastModified, modTime.UTC().Format(http.TimeFormat))
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

// Stream sends r as the response body without reading it into memory: the
// handler returns, and r is copied to the client as the response is
// written. size is r's length in bytes when known, and the response
// carries it as Content-Length. A negative size means "until EOF"; the
// body then goes out with chunked transfer encoding, so a body whose size
// is unknown until it ends (a dump piped from a subprocess, an export
// built row by row) needs no temp file and no buffer of its own size.
// Anything already written to the body is sent before r.
//
// The response owns r: it is closed, if it is an io.Closer, once sent,
// replaced, or dropped. Because the copy happens after the handler
// returns, a producer must not be torn down by a defer in the handler.
// Run it in a goroutine that writes to an io.Pipe instead:
//
//	pr, pw := io.Pipe()
//	go func() {
//		// ctx.Request().Context() stays live until the body is sent.
//		pw.CloseWithError(dump(ctx.Request().Context(), pw))
//	}()
//	ctx.Response().SetHeader("Content-Disposition", `attachment; filename="db.dump"`)
//	return rweb.Stream(ctx, pr, -1)
//
// If the producer fails (CloseWithError with a non-nil error) or a sized
// r ends short, the connection is closed before the body is complete, so
// the client sees a failed download rather than a short file that looks
// whole. If the client goes away, r is closed and the producer's next
// write fails with io.ErrClosedPipe.
//
// The stream stays pending until the response is written, so middleware
// can still read or rewrite it after Next(), at the cost of reading it
// into memory (see response.bodyStream). A handler returning an error
// after Stream drops the stream: the error response replaces it. A HEAD
// request gets the headers alone and r is never read. Chunked encoding is
// HTTP/1.1; an HTTP/1.0 client cannot read a body of unknown size.
func Stream(ctx Context, r io.Reader, size int64) error {
	// The Context here is always our *context; the fallback only keeps a
	// foreign Response implementation working, at the cost of buffering.
	if res, ok := ctx.Response().(*response); ok {
		res.setBodyStream(r, size)
		return nil
	}
	defer closeStream(r)
	if size >= 0 {
		r = io.LimitReader(r, size)
	}
	_, err := io.Copy(ctx.Response(), r)
	return err
}

// SendFile streams the file at path as the response, with headers chosen
// by its extension as in File, Last-Modified from the file, and a 304 for
// a client whose cached copy is still current. The file is never read
// into memory: it is copied to the connection after the handler returns,
// by sendfile(2) on plain TCP (see Stream). Downloadable types are offered
// under the file's base name; use SendFileAs to choose another.
//
// A missing file, or a directory, is returned as an error that matches
// os.ErrNotExist, for the handler to turn into a 404 if it likes.
func SendFile(ctx Context, path string) error {
	return SendFileAs(ctx, path, filepath.Base(path))
}

// SendFileAs is SendFile with name in place of the file's own base name:
// it picks the Content-Type and, for downloadable types, is the name the
// browser saves the file as. Useful when the file on disk has a temporary
// or internal name, such as a dump written to a scratch directory.
func SendFileAs(ctx Context, path, name string) error {
	// Open first and Stat the open file: the size and mod time then
	// describe the very file whose bytes are sent, not whatever the path
	// pointed at a moment earlier.
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return err
	}
	if info.IsDir() {
		_ = f.Close()
		return &os.PathError{Op: "send", Path: path, Err: errIsDir}
	}

	if notModifiedSince(ctx, info.ModTime()) {
		_ = f.Close()
		ctx.SetStatus(consts.StatusNotModified)
		return nil
	}

	setFileHeaders(ctx, name, info.ModTime())
	return Stream(ctx, f, info.Size())
}

// errIsDir is what SendFile reports for a directory. It wraps
// os.ErrNotExist because, to a client asking for a file, a directory at
// that path is no file at all, and it keeps one errors.Is check enough to
// map both cases to a 404.
var errIsDir = fmt.Errorf("is a directory: %w", os.ErrNotExist)

// notModifiedSince reports whether the request's If-Modified-Since shows
// the client already holds the version last modified at modTime, so a 304
// can be sent instead of the body. Last-Modified has one-second
// resolution, so the comparison is made at that grain. An absent or
// unparseable header means the body must be sent.
func notModifiedSince(ctx Context, modTime time.Time) bool {
	ims := ctx.Request().Header(consts.HeaderIfModifiedSince)
	if ims == "" {
		return false
	}
	t, err := http.ParseTime(ims)
	return err == nil && !modTime.Truncate(time.Second).After(t)
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

// XML sends the body with the content type set to `application/xml`.
func XML(ctx Context, body string) error {
	ctx.Response().SetHeader("Content-Type", consts.MIMEXML)
	return ctx.WriteString(body)
}
