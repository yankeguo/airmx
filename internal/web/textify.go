package web

import (
	"strings"
	"unicode"

	"golang.org/x/net/html"
)

// textifyBlockTags are elements that force a line break before and after
// their content when converting HTML to plain text.
var textifyBlockTags = map[string]bool{
	"p": true, "div": true, "blockquote": true, "pre": true, "hr": true,
	"h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true,
	"ul": true, "ol": true, "li": true,
	"table": true, "thead": true, "tbody": true, "tr": true,
	"section": true, "article": true, "header": true, "footer": true,
}

// htmlToText derives a plain-text approximation from an HTML body, for
// messages that ship no text/plain alternative. Scripts, styles and the
// head are dropped entirely; the result is inert text, so viewing it never
// fetches remote resources (no tracking pixels).
func htmlToText(s string) string {
	doc, err := html.Parse(strings.NewReader(s))
	if err != nil {
		return ""
	}
	var b strings.Builder
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			switch n.Data {
			case "script", "style", "head", "noscript", "template", "svg":
				return
			case "br":
				b.WriteByte('\n')
				return
			case "td", "th":
				b.WriteString("  ")
			}
			if textifyBlockTags[n.Data] {
				b.WriteByte('\n')
			}
			if n.Data == "li" {
				b.WriteString("- ")
			}
		}
		if n.Type == html.TextNode {
			b.WriteString(collapseSpace(n.Data))
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
		if n.Type == html.ElementNode && textifyBlockTags[n.Data] {
			b.WriteByte('\n')
		}
	}
	walk(doc)
	return normalizeText(b.String())
}

// collapseSpace reduces any run of whitespace (including newlines) inside a
// text node to a single space, matching how HTML renders inline text.
// Structural newlines come only from block elements.
func collapseSpace(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	space := false
	for _, r := range s {
		if unicode.IsSpace(r) {
			space = true
			continue
		}
		if space {
			b.WriteByte(' ')
			space = false
		}
		b.WriteRune(r)
	}
	if space {
		b.WriteByte(' ')
	}
	return b.String()
}

// normalizeText trims each line and drops empty lines, so the structural
// newlines emitted around block elements collapse into single line breaks.
func normalizeText(s string) string {
	lines := strings.Split(s, "\n")
	out := lines[:0]
	for _, ln := range lines {
		ln = strings.Join(strings.Fields(ln), " ")
		if ln != "" {
			out = append(out, ln)
		}
	}
	return strings.Join(out, "\n")
}
