package browser

import (
	"bytes"
	"fmt"
	"net/url"
	"path"
	"sort"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// A Markdown file is shown as a page, since that is what a report the
// agent writes most often is. It is converted on the host as it is served,
// so the VM hands over nothing but the file: raw HTML in it is left out of
// the page. Every top-level block and every list item is marked with the
// source lines it came from, so that a comment on what the human sees
// lands on lines the agent can find, and a path the file names leads to
// the neighbour under the same token: an image to the file itself, a link
// to its page.
var markdown = goldmark.New(
	goldmark.WithExtensions(extension.GFM),
	goldmark.WithParserOptions(parser.WithASTTransformers(util.Prioritized(&reviewable{}, 1000))),
	goldmark.WithRendererOptions(renderer.WithNodeRenderers(util.Prioritized(blockRenderer{}, 100))),
)

func isMarkdown(rel string) bool {
	switch strings.ToLower(path.Ext(rel)) {
	case ".md", ".markdown":
		return true
	}
	return false
}

// renderMarkdown converts the file at rel into the page's body.
func renderMarkdown(source []byte, prefix, rel string) ([]byte, error) {
	ctx := parser.NewContext()
	ctx.Set(prefixKey, prefix)
	ctx.Set(dirKey, path.Dir(rel))
	var body bytes.Buffer
	if err := markdown.Convert(source, &body, parser.WithContext(ctx)); err != nil {
		return nil, err
	}
	return body.Bytes(), nil
}

var (
	prefixKey = parser.NewContextKey()
	dirKey    = parser.NewContextKey()
)

// block wraps one top-level block of the document with the source lines
// it spans.
type block struct {
	ast.BaseBlock
	from, to int
}

var kindBlock = ast.NewNodeKind("ReviewBlock")

func (*block) Kind() ast.NodeKind { return kindBlock }

func (b *block) Dump(source []byte, level int) {
	ast.DumpHelper(b, source, level, map[string]string{"lines": fmt.Sprintf("%d-%d", b.from, b.to)}, nil)
}

type blockRenderer struct{}

func (blockRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(kindBlock, func(w util.BufWriter, _ []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
		if entering {
			b := node.(*block)
			fmt.Fprintf(w, `<div class="block" data-lines="%d-%d">`, b.from, b.to)
		} else {
			w.WriteString("</div>\n")
		}
		return ast.WalkContinue, nil
	})
}

// reviewable marks the blocks with their lines and points the paths the
// file names at the server.
type reviewable struct{}

func (reviewable) Transform(doc *ast.Document, reader text.Reader, pc parser.Context) {
	starts := lineStarts(reader.Source())
	for child := doc.FirstChild(); child != nil; {
		next := child.NextSibling()
		if from, to, ok := span(child, starts); ok {
			wrapper := &block{from: from, to: to}
			doc.ReplaceChild(doc, child, wrapper)
			wrapper.AppendChild(wrapper, child)
		}
		child = next
	}
	prefix, _ := pc.Get(prefixKey).(string)
	dir, _ := pc.Get(dirKey).(string)
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch n := n.(type) {
		case *ast.ListItem:
			if from, to, ok := span(n, starts); ok {
				n.SetAttributeString("data-lines", []byte(fmt.Sprintf("%d-%d", from, to)))
			}
		case *ast.Image:
			n.Destination = []byte(served(string(n.Destination), prefix+"raw/", dir))
		case *ast.Link:
			n.Destination = []byte(served(string(n.Destination), prefix+"view/", dir))
		}
		return ast.WalkContinue, nil
	})
}

// served turns a path the file names, relative to its own directory or to
// the project, into the server's URL for it under base. A URL with a
// scheme or a host, and a fragment of the page itself, are left alone.
func served(destination, base, dir string) string {
	parsed, err := url.Parse(destination)
	if err != nil || parsed.Scheme != "" || parsed.Host != "" || parsed.Path == "" || strings.HasPrefix(destination, "//") {
		return destination
	}
	target := parsed.Path
	if strings.HasPrefix(target, "/") {
		target = path.Clean(target)[1:]
	} else {
		target = path.Join(dir, target)
	}
	if target == ".." || strings.HasPrefix(target, "../") {
		return destination
	}
	out := base + escapePath(target)
	if parsed.RawQuery != "" {
		out += "?" + parsed.RawQuery
	}
	if parsed.Fragment != "" {
		out += "#" + parsed.Fragment
	}
	return out
}

func escapePath(rel string) string {
	segments := strings.Split(rel, "/")
	for i, segment := range segments {
		segments[i] = url.PathEscape(segment)
	}
	return strings.Join(segments, "/")
}

// lineStarts is the byte offset at which each line begins.
func lineStarts(source []byte) []int {
	starts := []int{0}
	for i, b := range source {
		if b == '\n' && i+1 < len(source) {
			starts = append(starts, i+1)
		}
	}
	return starts
}

// span is the first and last source line of a node: its own lines, or
// those of what it contains. Line numbers start at 1.
func span(n ast.Node, starts []int) (from, to int, ok bool) {
	lines := n.Lines()
	if lines != nil && lines.Len() > 0 {
		first, last := lines.At(0), lines.At(lines.Len()-1)
		return lineOf(first.Start, starts), lineOf(max(last.Stop-1, last.Start), starts), true
	}
	for child := n.FirstChild(); child != nil; child = child.NextSibling() {
		if childFrom, childTo, found := span(child, starts); found {
			if !ok || childFrom < from {
				from = childFrom
			}
			if !ok || childTo > to {
				to = childTo
			}
			ok = true
		}
	}
	return from, to, ok
}

func lineOf(offset int, starts []int) int {
	return sort.Search(len(starts), func(i int) bool { return starts[i] > offset })
}
