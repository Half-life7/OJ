package community

import (
	"bytes"
	"github.com/microcosm-cc/bluemonday"
	"github.com/yuin/goldmark/v2/parser"
	"github.com/yuin/goldmark/v2/renderer/html"
)

var markdownPolicy = func() *bluemonday.Policy {
	p := bluemonday.UGCPolicy()
	p.AllowAttrs("class").Matching(bluemonday.SpaceSeparatedTokens).OnElements("code")
	return p
}()

// Render disables raw HTML at the parser/renderer boundary, then sanitizes the
// generated HTML. Code fences retain language classes without executable HTML.
func Render(body string) (string, error) {
	if !text(body, 65536) {
		return "", ErrInvalid
	}
	source := []byte(body)
	doc := parser.New().Parse(source)
	var out bytes.Buffer
	if e := html.New().Render(&out, source, doc); e != nil {
		return "", e
	}
	return markdownPolicy.Sanitize(out.String()), nil
}
