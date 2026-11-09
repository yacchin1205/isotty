package browser

import (
	"bytes"
	_ "embed"
	"fmt"
	"html"
	"io"
	"net/http"
	"os"
	"path"
	"strings"
	"unicode/utf8"
)

//go:embed assets/viewer.css
var viewerStyle []byte

//go:embed assets/viewer.js
var viewerScript []byte

// viewerPolicy is sent with the viewer page. The page is the server's own
// code, so its script runs and it talks back to the server; what the VM
// wrote is in it only as text, as an image or as rendered Markdown, and
// the page loads nothing from anywhere else.
const viewerPolicy = "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self'; connect-src 'self'; font-src 'self'; media-src 'self'"

// textLimit is the largest file put on a page as text or as a Markdown page.
const textLimit = 8 << 20

// kind is how the viewer shows a file, which decides what a comment can
// point at: lines of a text, a block of a Markdown page, a spot on an
// image, or the file as a whole.
type kind string

const (
	kindMarkdown kind = "markdown"
	kindText     kind = "text"
	kindImage    kind = "image"
	kindOther    kind = "other"
)

func isImage(rel string) bool {
	switch strings.ToLower(path.Ext(rel)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".svg", ".bmp", ".avif":
		return true
	}
	return false
}

// classify decides how the file is shown. A text is what is valid UTF-8
// without NUL bytes, judged on its first kilobytes; a text or a Markdown
// file too large to put on a page is shown as a whole.
func classify(rel string, file io.ReadSeeker, size int64) (kind, error) {
	switch {
	case isImage(rel):
		return kindImage, nil
	case size > textLimit:
		return kindOther, nil
	case isMarkdown(rel):
		return kindMarkdown, nil
	}
	head := make([]byte, 8192)
	n, err := io.ReadFull(file, head)
	if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
		return "", err
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	head = head[:n]
	if bytes.IndexByte(head, 0) >= 0 {
		return kindOther, nil
	}
	// A sample cut from a longer file may end inside a character.
	for i := 0; i < utf8.UTFMax && n == cap(head) && !utf8.Valid(head); i++ {
		head = head[:len(head)-1]
	}
	if !utf8.Valid(head) {
		return kindOther, nil
	}
	return kindText, nil
}

// view answers the browser with the page for a file: the file's content,
// shown as what it is, inside the server's own page that lets the human
// comment on it.
func (f *Files) view(w http.ResponseWriter, r *http.Request) {
	if r.Host != f.listener.Addr().String() {
		http.NotFound(w, r)
		return
	}
	hostPath, rel, err := f.locate(r.PathValue("path"))
	if err != nil || f.ignored(rel) {
		http.NotFound(w, r)
		return
	}
	file, err := os.Open(hostPath)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}
	shown, err := classify(rel, file, info.Size())
	if err != nil {
		http.Error(w, "reading the file failed", http.StatusInternalServerError)
		return
	}
	prefix := "/" + f.token + "/"
	var content []byte
	switch shown {
	case kindMarkdown:
		source, err := io.ReadAll(file)
		if err == nil {
			content, err = renderMarkdown(source, prefix, rel)
		}
		if err != nil {
			http.Error(w, "rendering the file failed", http.StatusInternalServerError)
			return
		}
	case kindText:
		source, err := io.ReadAll(file)
		if err != nil {
			http.Error(w, "reading the file failed", http.StatusInternalServerError)
			return
		}
		content = renderText(source)
	case kindImage:
		content = []byte(`<div class="canvas"><img id="image" src="` + prefix + "raw/" + escapePath(rel) + `" alt=""></div>`)
	default:
		content = []byte(`<p class="other">This file is not shown here. <a href="` + prefix + "raw/" + escapePath(rel) + `" target="_blank" rel="noopener">Open the file itself</a>, and comment on it as a whole.</p>`)
	}
	w.Header().Set("Content-Security-Policy", viewerPolicy)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<!doctype html>
<html><head><meta charset="utf-8"><title>%s</title><link rel="stylesheet" href="%sviewer.css"></head>
<body data-kind="%s" data-reviews="%sreviews/%s">
<header><span class="path">%s</span><span id="status"></span></header>
<main><article id="content" class="%s">
`, html.EscapeString(path.Base(rel)), prefix, shown, prefix, escapePath(rel), html.EscapeString(rel), shown)
	w.Write(content)
	fmt.Fprintf(w, `</article><aside id="comments"><h2>Comments</h2><ol id="list"></ol></aside></main>
<script src="%sviewer.js"></script></body></html>
`, prefix)
}

// renderText puts the file on the page line by line, each line numbered so
// that it can be pointed at.
func renderText(source []byte) []byte {
	var out bytes.Buffer
	out.WriteString(`<table class="text">`)
	lines := strings.Split(strings.TrimSuffix(string(source), "\n"), "\n")
	for i, line := range lines {
		fmt.Fprintf(&out, `<tr id="L%d"><td class="n" data-line="%d">%d</td><td class="c">%s</td></tr>`, i+1, i+1, i+1, html.EscapeString(line))
		out.WriteByte('\n')
	}
	out.WriteString("</table>")
	return out.Bytes()
}

func (f *Files) asset(contentType string, body []byte) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Host != f.listener.Addr().String() {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Write(body)
	}
}
