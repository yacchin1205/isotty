package browser

import (
	"bytes"
	"html"
	"io"
	"net/http"
	"path"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
)

// A Markdown file is shown as a page, since that is what a report the
// agent writes most often is. It is converted on the host as it is served,
// so the VM hands over nothing but the file: raw HTML in it is left out of
// the page, and an image it names by a relative path becomes a request
// for the neighbour under the same token, checked like any other.
var markdown = goldmark.New(goldmark.WithExtensions(extension.GFM))

func isMarkdown(rel string) bool {
	switch strings.ToLower(path.Ext(rel)) {
	case ".md", ".markdown":
		return true
	}
	return false
}

func renderMarkdown(w http.ResponseWriter, name string, file io.Reader) {
	source, err := io.ReadAll(file)
	if err != nil {
		http.Error(w, "reading the file failed", http.StatusInternalServerError)
		return
	}
	var body bytes.Buffer
	if err := markdown.Convert(source, &body); err != nil {
		http.Error(w, "rendering the file failed", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	io.WriteString(w, "<!doctype html>\n<html><head><meta charset=\"utf-8\"><title>"+html.EscapeString(name)+"</title><style>"+markdownStyle+"</style></head><body>\n")
	w.Write(body.Bytes())
	io.WriteString(w, "</body></html>\n")
}

const markdownStyle = `body{max-width:52em;margin:2em auto;padding:0 1em;font-family:system-ui,sans-serif;line-height:1.5;color:#1b1b1b;background:#fff}` +
	`img{max-width:100%}pre{overflow:auto;padding:.75em;background:#f4f4f4}code{font-family:ui-monospace,monospace;font-size:.95em}` +
	`table{border-collapse:collapse}th,td{border:1px solid #ccc;padding:.25em .6em}blockquote{margin:0;padding-left:1em;border-left:3px solid #ccc;color:#555}`
