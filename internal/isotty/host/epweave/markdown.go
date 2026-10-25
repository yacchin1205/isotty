package epweave

import (
	"errors"
	"fmt"
	"html"
	"regexp"
	"strings"
)

var (
	headingPattern = regexp.MustCompile(`^(#{1,3}) (.*)$`)
	bulletPattern  = regexp.MustCompile(`^[-*] `)
	// A trailing line made only of hashtags, as ep_weave renders it in getHTML output.
	hashtagLinePattern = regexp.MustCompile(`(?:#(?:[^\s<&]|&#\d+;|&[a-z]+;)+\s*)+<br>`)
)

// MarkdownToHTML converts a deliberately small subset: #/##/### headings,
// -/* bullets, paragraphs. URLs stay as text; Etherpad links them itself and
// setHTML drops <a> tags anyway.
func MarkdownToHTML(text string) string {
	var out strings.Builder
	var bullets []string
	flush := func() {
		if len(bullets) == 0 {
			return
		}
		out.WriteString("<ul>")
		for _, bullet := range bullets {
			out.WriteString("<li>" + bullet + "</li>")
		}
		out.WriteString("</ul>")
		bullets = nil
	}

	for _, line := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
		if m := headingPattern.FindStringSubmatch(line); m != nil {
			flush()
			level := len(m[1])
			fmt.Fprintf(&out, "<h%d>%s</h%d>", level, html.EscapeString(m[2]), level)
			continue
		}
		if bulletPattern.MatchString(line) {
			bullets = append(bullets, html.EscapeString(line[2:]))
			continue
		}
		flush()
		if strings.TrimSpace(line) != "" {
			out.WriteString("<p>" + html.EscapeString(line) + "</p>")
		}
	}
	flush()
	return out.String()
}

func htmlDocument(fragment string) string {
	return "<!DOCTYPE html><html><body>" + fragment + "</body></html>"
}

var (
	// Etherpad omits the last </li> before </ul>, so every block tag, opening
	// or closing, counts as a line break.
	lineBreakPattern = regexp.MustCompile(`<br\s*/?>|</?(?:h[1-6]|p|li|ul|ol|div)\b[^>]*>`)
	tagPattern       = regexp.MustCompile(`<[^>]+>`)
)

// htmlLines flattens getHTML output into text lines: one per block element,
// tags removed, entities decoded, blank lines dropped.
func htmlLines(doc string) []string {
	text := lineBreakPattern.ReplaceAllString(doc, "\n")
	text = html.UnescapeString(tagPattern.ReplaceAllString(text, ""))
	var lines []string
	for _, line := range strings.Split(text, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

// insertionPoint keeps the hashtag line last: insert before it when the pad
// has one, otherwise before </body>.
func insertionPoint(currentHTML string) (int, error) {
	if tags := hashtagLinePattern.FindAllStringIndex(currentHTML, -1); len(tags) > 0 {
		return tags[len(tags)-1][0], nil
	}
	at := strings.Index(currentHTML, "</body>")
	if at < 0 {
		return 0, errors.New("unexpected getHTML output: no </body>")
	}
	return at, nil
}
