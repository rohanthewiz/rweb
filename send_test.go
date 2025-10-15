package rweb_test

import (
	"testing"
	"time"

	"github.com/rohanthewiz/assert"
	"github.com/rohanthewiz/rweb"
	"github.com/rohanthewiz/rweb/consts"
)

func TestContentTypes(t *testing.T) {
	s := rweb.NewServer()

	s.Get("/css", func(ctx rweb.Context) error {
		return rweb.CSS(ctx, "body{}")
	})

	s.Get("/csv", func(ctx rweb.Context) error {
		return rweb.CSV(ctx, "ID;Name\n")
	})

	s.Get("/html", func(ctx rweb.Context) error {
		return rweb.HTML(ctx, "<html></html>")
	})

	s.Get("/js", func(ctx rweb.Context) error {
		return rweb.JS(ctx, "console.log(42)")
	})

	s.Get("/json", func(ctx rweb.Context) error {
		return rweb.JSON(ctx, struct{ Name string }{Name: "User 1"})
	})

	s.Get("/text", func(ctx rweb.Context) error {
		return rweb.Text(ctx, "Hello")
	})

	s.Get("/xml", func(ctx rweb.Context) error {
		return rweb.XML(ctx, "<xml></xml>")
	})

	tests := []struct {
		Method      string
		URL         string
		Body        string
		Status      int
		Response    string
		ContentType string
	}{
		{Method: consts.MethodGet, URL: "/css", Status: 200, Response: "body{}", ContentType: "text/css"},
		{Method: consts.MethodGet, URL: "/csv", Status: 200, Response: "ID;Name\n", ContentType: "text/csv"},
		{Method: consts.MethodGet, URL: "/html", Status: 200, Response: "<html></html>", ContentType: "text/html"},
		{Method: consts.MethodGet, URL: "/js", Status: 200, Response: "console.log(42)", ContentType: "text/javascript"},
		{Method: consts.MethodGet, URL: "/json", Status: 200, Response: "{\"Name\":\"User 1\"}\n", ContentType: "application/json"},
		{Method: consts.MethodGet, URL: "/text", Status: 200, Response: "Hello", ContentType: "text/plain"},
		{Method: consts.MethodGet, URL: "/xml", Status: 200, Response: "<xml></xml>", ContentType: "text/xml"},
	}

	for _, test := range tests {
		t.Run(test.URL, func(t *testing.T) {
			response := s.Request(test.Method, "http://example.com"+test.URL, nil, nil)
			assert.Equal(t, response.Status(), test.Status)
			assert.Equal(t, response.Header("Content-Type"), test.ContentType)
			assert.Equal(t, string(response.Body()), test.Response)
		})
	}
}

// TestFileHeaders tests that File() sets appropriate headers based on file extension
func TestFileHeaders(t *testing.T) {
	s := rweb.NewServer()

	// Setup routes for different file types
	s.Get("/image", func(ctx rweb.Context) error {
		return rweb.File(ctx, "photo.png", []byte("fake-png-data"))
	})

	s.Get("/document", func(ctx rweb.Context) error {
		return rweb.File(ctx, "report.pdf", []byte("fake-pdf-data"))
	})

	s.Get("/archive", func(ctx rweb.Context) error {
		return rweb.File(ctx, "archive.zip", []byte("fake-zip-data"))
	})

	s.Get("/text", func(ctx rweb.Context) error {
		return rweb.File(ctx, "readme.txt", []byte("fake-text-data"))
	})

	s.Get("/video", func(ctx rweb.Context) error {
		return rweb.File(ctx, "video.mp4", []byte("fake-video-data"))
	})

	s.Get("/font", func(ctx rweb.Context) error {
		return rweb.File(ctx, "font.woff2", []byte("fake-font-data"))
	})

	s.Get("/unknown", func(ctx rweb.Context) error {
		return rweb.File(ctx, "data.unknown", []byte("fake-data"))
	})

	s.Get("/svg", func(ctx rweb.Context) error {
		return rweb.File(ctx, "image.svg", []byte("<svg></svg>"))
	})

	tests := []struct {
		name                 string
		url                  string
		expectedContentType  string
		shouldHaveDownload   bool // Should have Content-Disposition header
		shouldHaveCharset    bool // Should have charset=utf-8
		expectedResponseBody string
	}{
		{
			name:                 "PNG Image - viewable, no download headers, no charset",
			url:                  "/image",
			expectedContentType:  "image/png",
			shouldHaveDownload:   false,
			shouldHaveCharset:    false,
			expectedResponseBody: "fake-png-data",
		},
		{
			name:                 "PDF Document - has MIME but no forced download",
			url:                  "/document",
			expectedContentType:  "application/pdf",
			shouldHaveDownload:   false,
			shouldHaveCharset:    false,
			expectedResponseBody: "fake-pdf-data",
		},
		{
			name:                 "ZIP Archive - downloadable",
			url:                  "/archive",
			expectedContentType:  "application/zip",
			shouldHaveDownload:   true,
			shouldHaveCharset:    false,
			expectedResponseBody: "fake-zip-data",
		},
		{
			name:                 "Text File - viewable, with charset",
			url:                  "/text",
			expectedContentType:  "text/plain; charset=utf-8",
			shouldHaveDownload:   false,
			shouldHaveCharset:    true,
			expectedResponseBody: "fake-text-data",
		},
		{
			name:                 "SVG - viewable, with charset (XML-based)",
			url:                  "/svg",
			expectedContentType:  "image/svg; charset=utf-8",
			shouldHaveDownload:   false,
			shouldHaveCharset:    true,
			expectedResponseBody: "<svg></svg>",
		},
		{
			name:                 "MP4 Video - viewable, no download headers",
			url:                  "/video",
			expectedContentType:  "video/mp4",
			shouldHaveDownload:   false,
			shouldHaveCharset:    false,
			expectedResponseBody: "fake-video-data",
		},
		{
			name:                 "WOFF2 Font - viewable, no download headers",
			url:                  "/font",
			expectedContentType:  "font/woff2",
			shouldHaveDownload:   false,
			shouldHaveCharset:    false,
			expectedResponseBody: "fake-font-data",
		},
		{
			name:                 "Unknown Extension - defaults to octet-stream with download",
			url:                  "/unknown",
			expectedContentType:  "application/octet-stream",
			shouldHaveDownload:   true,
			shouldHaveCharset:    false,
			expectedResponseBody: "fake-data",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response := s.Request(consts.MethodGet, "http://example.com"+tt.url, nil, nil)

			// Check status code
			assert.Equal(t, response.Status(), 200)

			// Check Content-Type (including charset if applicable)
			assert.Equal(t, response.Header("Content-Type"), tt.expectedContentType)

			// Check Date header is present (RFC 7231 requirement)
			assert.NotEqual(t, response.Header("Date"), "")

			// Check download headers
			contentDisposition := response.Header("Content-Disposition")
			if tt.shouldHaveDownload {
				// Should have download headers
				assert.NotEqual(t, contentDisposition, "")
				assert.NotEqual(t, response.Header("x-filename"), "")
				assert.NotEqual(t, response.Header("Content-Description"), "")
				assert.NotEqual(t, response.Header("Content-Transfer-Encoding"), "")
			} else {
				// Should NOT have download headers
				assert.Equal(t, contentDisposition, "")
				assert.Equal(t, response.Header("x-filename"), "")
				assert.Equal(t, response.Header("Content-Description"), "")
				assert.Equal(t, response.Header("Content-Transfer-Encoding"), "")
			}

			// Check response body
			assert.Equal(t, string(response.Body()), tt.expectedResponseBody)
		})
	}
}

// TestFileMimeTypeExtensions tests various file extensions map to correct MIME types
func TestFileMimeTypeExtensions(t *testing.T) {
	s := rweb.NewServer()

	tests := []struct {
		filename     string
		mimeType     string
		downloadable bool
	}{
		// Text formats (should include charset=utf-8)
		{"file.html", "text/html; charset=utf-8", false},
		{"file.htm", "text/html; charset=utf-8", false},
		{"file.css", "text/css; charset=utf-8", false},
		{"file.js", "text/javascript; charset=utf-8", false},
		{"file.json", "application/json; charset=utf-8", false},
		{"file.xml", "application/xml; charset=utf-8", false},
		{"file.txt", "text/plain; charset=utf-8", false},
		{"file.log", "text/plain; charset=utf-8", false},
		{"file.csv", "text/csv; charset=utf-8", false},

		// Images (binary formats, no charset)
		{"file.png", "image/png", false},
		{"file.jpg", "image/jpeg", false},
		{"file.jpeg", "image/jpeg", false},
		{"file.gif", "image/gif", false},
		{"file.svg", "image/svg; charset=utf-8", false}, // SVG is text-based (XML)
		{"file.ico", "image/x-icon", false},
		{"file.webp", "image/webp", false},

		// Documents (binary formats, no charset)
		{"file.pdf", "application/pdf", false},
		{"file.doc", "application/msword", true},
		{"file.docx", "application/vnd.openxmlformats-officedocument.wordprocessingml.document", true},
		{"file.xls", "application/vnd.ms-excel", true},
		{"file.xlsx", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", true},
		{"file.ppt", "application/vnd.ms-powerpoint", true},
		{"file.pptx", "application/vnd.openxmlformats-officedocument.presentationml.presentation", true},

		// Archives (binary formats, no charset)
		{"file.zip", "application/zip", true},
		{"file.tar", "application/x-tar", true},
		{"file.gz", "application/gzip", true},
		{"file.gzip", "application/gzip", true},
		{"file.rar", "application/vnd.rar", true},
		{"file.7z", "application/x-7z-compressed", true},

		// Audio (binary formats, no charset)
		{"file.mp3", "audio/mpeg", false},
		{"file.wav", "audio/wav", false},
		{"file.ogg", "audio/ogg", false},
		{"file.m4a", "audio/mp4", false},

		// Video (binary formats, no charset)
		{"file.mp4", "video/mp4", false},
		{"file.webm", "video/webm", false},
		{"file.avi", "video/x-msvideo", false},
		{"file.mov", "video/quicktime", false},

		// Fonts (binary formats, no charset)
		{"file.woff", "font/woff", false},
		{"file.woff2", "font/woff2", false},
		{"file.ttf", "font/ttf", false},
		{"file.otf", "font/otf", false},

		// Case insensitive
		{"FILE.PNG", "image/png", false},
		{"FILE.ZIP", "application/zip", true},
	}

	for _, tt := range tests {
		t.Run(tt.filename, func(t *testing.T) {
			// Create a route for this test
			s.Get("/test-"+tt.filename, func(ctx rweb.Context) error {
				return rweb.File(ctx, tt.filename, []byte("test-data"))
			})

			response := s.Request(consts.MethodGet, "http://example.com/test-"+tt.filename, nil, nil)

			// Verify MIME type
			assert.Equal(t, response.Header("Content-Type"), tt.mimeType)

			// Verify downloadable status
			hasContentDisposition := response.Header("Content-Disposition") != ""
			assert.Equal(t, hasContentDisposition, tt.downloadable)
		})
	}
}

// TestFileWithModTime tests that FileWithModTime sets Last-Modified header correctly
func TestFileWithModTime(t *testing.T) {
	s := rweb.NewServer()

	// Create a fixed modification time for testing
	modTime := time.Date(2024, 10, 14, 12, 30, 0, 0, time.UTC)

	s.Get("/file-with-modtime", func(ctx rweb.Context) error {
		return rweb.FileWithModTime(ctx, "document.pdf", []byte("pdf-content"), modTime)
	})

	s.Get("/file-without-modtime", func(ctx rweb.Context) error {
		return rweb.File(ctx, "document.pdf", []byte("pdf-content"))
	})

	t.Run("File with modification time sets Last-Modified header", func(t *testing.T) {
		response := s.Request(consts.MethodGet, "http://example.com/file-with-modtime", nil, nil)

		// Check that Last-Modified header is set
		lastModified := response.Header("Last-Modified")
		assert.NotEqual(t, lastModified, "")

		// Verify the format is RFC1123
		expectedLastModified := modTime.UTC().Format(time.RFC1123)
		assert.Equal(t, lastModified, expectedLastModified)

		// Check Date header is also present
		assert.NotEqual(t, response.Header("Date"), "")
	})

	t.Run("File without modification time has no Last-Modified header", func(t *testing.T) {
		response := s.Request(consts.MethodGet, "http://example.com/file-without-modtime", nil, nil)

		// Check that Last-Modified header is NOT set
		lastModified := response.Header("Last-Modified")
		assert.Equal(t, lastModified, "")

		// But Date header should still be present
		assert.NotEqual(t, response.Header("Date"), "")
	})
}
