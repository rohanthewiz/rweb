package send

import (
	"encoding/json"
	"net/url"

	"github.com/rohanthewiz/rweb"
)

// CSS sends the body with the content type set to `text/css`.
func CSS(ctx rweb.Context, body string) error {
	ctx.Response().SetHeader("Content-Type", "text/css")
	return ctx.WriteString(body)
}

// CSV sends the body with the content type set to `text/csv`.
func CSV(ctx rweb.Context, body string) error {
	ctx.Response().SetHeader("Content-Type", "text/csv")
	return ctx.WriteString(body)
}

// HTML sends the body with the content type set to `text/html`.
func HTML(ctx rweb.Context, body string) error {
	ctx.Response().SetHeader("Content-Type", "text/html")
	return ctx.WriteString(body)
}

func File(ctx rweb.Context, filename string, body []byte) error {
	ctx.Response().SetHeader("Content-Type", "application/octet-stream")
	ctx.Response().SetHeader("Content-Disposition", "attachment; filename="+url.QueryEscape(filename))
	ctx.Response().SetHeader("x-filename", url.QueryEscape(filename))
	ctx.Response().SetHeader("Content-Description", "File Transfer")
	ctx.Response().SetHeader("Content-Transfer-Encoding", "binary")
	ctx.Response().SetHeader("Expires", "0")
	ctx.Response().SetHeader("Cache-Control", "must-revalidate")
	ctx.Response().SetHeader("Pragma", "public")
	ctx.Response().SetHeader("Access-Control-Expose-Headers", "x-filename")
	return ctx.Bytes(body)
}

// JS sends the body with the content type set to `text/javascript`.
func JS(ctx rweb.Context, body string) error {
	ctx.Response().SetHeader("Content-Type", "text/javascript")
	return ctx.WriteString(body)
}

// JSON encodes the object in JSON format and sends it with the content type set to `application/json`.
func JSON(ctx rweb.Context, object any) error {
	ctx.Response().SetHeader("Content-Type", "application/json")
	return json.NewEncoder(ctx.Response()).Encode(object)
}

// Text sends the body with the content type set to `text/plain`.
func Text(ctx rweb.Context, body string) error {
	ctx.Response().SetHeader("Content-Type", "text/plain")
	return ctx.WriteString(body)
}

// XML sends the body with the content type set to `text/xml`.
func XML(ctx rweb.Context, body string) error {
	ctx.Response().SetHeader("Content-Type", "text/xml")
	return ctx.WriteString(body)
}
