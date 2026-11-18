package browser

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"regexp"
	"strconv"
	"strings"
)

// A Jupyter notebook is shown as its cells, with what they printed and
// drew: a notebook is read for its outputs as much as for its code. The
// page is the viewer's like any other: a Markdown cell is converted as a
// Markdown file is, a code cell is put on it line by line, and a text
// output as text. An image or an HTML output, a table of a data frame
// most often, is not put on the page but served on its own under out/,
// for an <img> and for a frame that runs no script, like a file under raw/.

// notebookLimit is the largest notebook shown as one. Its outputs are not
// put on the page, so a notebook with figures may be larger than a text.
const notebookLimit = 64 << 20

func isNotebook(rel string) bool {
	return strings.EqualFold(path.Ext(rel), ".ipynb")
}

// multiline is a notebook's text, which nbformat writes as one string or
// as a list of lines to join.
type multiline string

func (m *multiline) UnmarshalJSON(data []byte) error {
	var text string
	if err := json.Unmarshal(data, &text); err == nil {
		*m = multiline(text)
		return nil
	}
	var lines []string
	if err := json.Unmarshal(data, &lines); err != nil {
		return err
	}
	*m = multiline(strings.Join(lines, ""))
	return nil
}

type notebook struct {
	Format int    `json:"nbformat"`
	Cells  []cell `json:"cells"`
}

type cell struct {
	ID             string                                `json:"id"`
	Type           string                                `json:"cell_type"`
	Source         multiline                             `json:"source"`
	ExecutionCount *int                                  `json:"execution_count"`
	Outputs        []output                              `json:"outputs"`
	Attachments    map[string]map[string]json.RawMessage `json:"attachments"`
}

type output struct {
	Type           string                     `json:"output_type"`
	Name           string                     `json:"name"`
	Text           multiline                  `json:"text"`
	Data           map[string]json.RawMessage `json:"data"`
	ExecutionCount *int                       `json:"execution_count"`
	Ename          string                     `json:"ename"`
	Evalue         string                     `json:"evalue"`
	Traceback      []string                   `json:"traceback"`
}

func readNotebook(r io.Reader) (*notebook, error) {
	var nb notebook
	if err := json.NewDecoder(r).Decode(&nb); err != nil {
		return nil, fmt.Errorf("not a notebook: %w", err)
	}
	if nb.Format != 4 {
		return nil, fmt.Errorf("nbformat %d is not read; only 4 is", nb.Format)
	}
	return &nb, nil
}

// shownTypes are the media types an output is shown as, in the order
// Jupyter prefers them. JavaScript and widgets are not among them: they
// need a script, and their text/plain stands in for them.
var shownTypes = []string{"text/html", "image/svg+xml", "image/png", "image/jpeg", "image/gif", "text/markdown", "text/plain"}

// servedTypes are those served on their own under out/ rather than put on
// the page.
var servedTypes = map[string]bool{"text/html": true, "image/svg+xml": true, "image/png": true, "image/jpeg": true, "image/gif": true}

func shownType(data map[string]json.RawMessage) string {
	for _, t := range shownTypes {
		if _, ok := data[t]; ok {
			return t
		}
	}
	return ""
}

func dataText(raw json.RawMessage) (string, error) {
	var text multiline
	err := json.Unmarshal(raw, &text)
	return string(text), err
}

// dataBytes is the content of one representation: a bitmap is kept in
// base64, the rest as text.
func dataBytes(mediaType string, raw json.RawMessage) ([]byte, error) {
	text, err := dataText(raw)
	if err != nil {
		return nil, err
	}
	if strings.HasPrefix(mediaType, "image/") && mediaType != "image/svg+xml" {
		return base64.StdEncoding.DecodeString(strings.Join(strings.Fields(text), ""))
	}
	return []byte(text), nil
}

// ansi matches the terminal escapes a traceback or a progress bar carries.
var ansi = regexp.MustCompile(`\x1b\[[0-9;?]*[A-Za-z]`)

// terminalText is what a terminal would show of a stream: the escapes
// left out, and of a line redrawn with carriage returns its last drawing.
func terminalText(text string) string {
	lines := strings.Split(ansi.ReplaceAllString(text, ""), "\n")
	for i, line := range lines {
		if at := strings.LastIndex(strings.TrimRight(line, "\r"), "\r"); at >= 0 {
			lines[i] = line[at+1:]
		}
	}
	return strings.TrimSuffix(strings.Join(lines, "\n"), "\n")
}

// renderNotebook puts the notebook's cells on the page. Each cell and each
// output has a label to comment on it as a whole; a line of code and a
// block of Markdown take a comment as in a text and a Markdown file, with
// lines counted in the cell's source.
func renderNotebook(nb *notebook, prefix, rel string) ([]byte, error) {
	var out bytes.Buffer
	outURL := prefix + "out/" + escapePath(rel)
	for i, c := range nb.Cells {
		n := i + 1
		label := fmt.Sprintf("[%d] %s", n, c.Type)
		if c.Type == "code" && c.ExecutionCount != nil {
			label += fmt.Sprintf(" · In [%d]", *c.ExecutionCount)
		}
		fmt.Fprintf(&out, `<section class="cell %s" data-cell="%d" data-cell-id="%s"><div class="label">%s</div>`,
			html.EscapeString(c.Type), n, html.EscapeString(c.ID), html.EscapeString(label))
		switch c.Type {
		case "markdown":
			attachments := fmt.Sprintf("%s?cell=%d&attachment=", outURL, n)
			body, err := convertMarkdown([]byte(c.Source), prefix, rel, attachments)
			if err != nil {
				return nil, err
			}
			out.WriteString(`<div class="md">`)
			out.Write(body)
			out.WriteString(`</div>`)
		default:
			if c.Source != "" {
				out.Write(renderLines(string(c.Source), ""))
			}
		}
		for j, o := range c.Outputs {
			m := j + 1
			fmt.Fprintf(&out, `<div class="output" data-output="%d"><div class="label">output %d</div>`, m, m)
			if err := renderOutput(&out, o, fmt.Sprintf("%s?cell=%d&output=%d", outURL, n, m), prefix, rel); err != nil {
				return nil, fmt.Errorf("cell %d output %d: %w", n, m, err)
			}
			out.WriteString(`</div>`)
		}
		out.WriteString("</section>\n")
	}
	return out.Bytes(), nil
}

func renderOutput(out *bytes.Buffer, o output, served, prefix, rel string) error {
	switch o.Type {
	case "stream":
		fmt.Fprintf(out, `<pre class="stream %s">%s</pre>`, html.EscapeString(o.Name), html.EscapeString(terminalText(string(o.Text))))
	case "error":
		text := o.Ename + ": " + o.Evalue
		if len(o.Traceback) > 0 {
			text = strings.Join(o.Traceback, "\n")
		}
		fmt.Fprintf(out, `<pre class="error">%s</pre>`, html.EscapeString(terminalText(text)))
	case "display_data", "execute_result", "update_display_data":
		mediaType := shownType(o.Data)
		switch {
		case mediaType == "text/html":
			fmt.Fprintf(out, `<iframe class="html" sandbox="allow-same-origin" src="%s"></iframe>`, html.EscapeString(served))
		case servedTypes[mediaType]:
			fmt.Fprintf(out, `<img src="%s" alt="">`, html.EscapeString(served))
		case mediaType == "text/markdown":
			text, err := dataText(o.Data[mediaType])
			if err != nil {
				return err
			}
			body, err := convertMarkdown([]byte(text), prefix, rel, "")
			if err != nil {
				return err
			}
			out.WriteString(`<div class="md">`)
			out.Write(body)
			out.WriteString(`</div>`)
		case mediaType == "text/plain":
			text, err := dataText(o.Data[mediaType])
			if err != nil {
				return err
			}
			fmt.Fprintf(out, `<pre>%s</pre>`, html.EscapeString(terminalText(text)))
		default:
			var types []string
			for t := range o.Data {
				types = append(types, t)
			}
			fmt.Fprintf(out, `<p class="other">This output (%s) is not shown here.</p>`, html.EscapeString(strings.Join(types, ", ")))
		}
	default:
		fmt.Fprintf(out, `<p class="other">This output (%s) is not shown here.</p>`, html.EscapeString(o.Type))
	}
	return nil
}

// notebookOutputPolicy is sent with an HTML output. It runs no script, as
// a file under raw/ does not, but keeps the server's origin, so that the
// viewer can read how tall the table is and size its frame to it: with no
// script, the origin gives the output nothing to read with.
const notebookOutputPolicy = "sandbox allow-same-origin; default-src 'none'; img-src 'self'; style-src 'self' 'unsafe-inline'; font-src 'self'; media-src 'self'"

// output answers the browser with one output or attachment of a notebook,
// an image or an HTML fragment, located and read again from the file.
func (f *Files) output(w http.ResponseWriter, r *http.Request) {
	if r.Host != f.listener.Addr().String() {
		http.NotFound(w, r)
		return
	}
	hostPath, rel, err := f.locate(r.PathValue("path"))
	if err != nil || f.ignored(rel) || !isNotebook(rel) {
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
	if err != nil || !info.Mode().IsRegular() || info.Size() > notebookLimit {
		http.NotFound(w, r)
		return
	}
	nb, err := readNotebook(file)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	query := r.URL.Query()
	n, err := strconv.Atoi(query.Get("cell"))
	if err != nil || n < 1 || n > len(nb.Cells) {
		http.NotFound(w, r)
		return
	}
	c := nb.Cells[n-1]
	var data map[string]json.RawMessage
	if name := query.Get("attachment"); name != "" {
		data = c.Attachments[name]
	} else {
		m, err := strconv.Atoi(query.Get("output"))
		if err != nil || m < 1 || m > len(c.Outputs) {
			http.NotFound(w, r)
			return
		}
		data = c.Outputs[m-1].Data
	}
	mediaType := shownType(data)
	if !servedTypes[mediaType] {
		http.NotFound(w, r)
		return
	}
	body, err := dataBytes(mediaType, data[mediaType])
	if err != nil {
		http.Error(w, "reading the output failed", http.StatusInternalServerError)
		return
	}
	policy := contentSecurityPolicy
	switch mediaType {
	case "text/html":
		policy = notebookOutputPolicy
		body = append([]byte(`<!doctype html><meta charset="utf-8"><style>body{margin:0;font-family:system-ui,sans-serif;font-size:14px}table{border-collapse:collapse}th,td{border:1px solid #ddd;padding:.2em .5em}</style>`), body...)
		mediaType += "; charset=utf-8"
	case "image/svg+xml":
		mediaType += "; charset=utf-8"
	}
	w.Header().Set("Content-Security-Policy", policy)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Type", mediaType)
	w.Write(body)
}

// attachmentName is the name an image in a Markdown cell gives as
// attachment:<name>, or "" for any other destination.
func attachmentName(destination string) string {
	name, ok := strings.CutPrefix(destination, "attachment:")
	if !ok {
		return ""
	}
	if unescaped, err := url.PathUnescape(name); err == nil {
		return unescaped
	}
	return name
}
