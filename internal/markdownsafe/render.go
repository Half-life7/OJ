// Package markdownsafe renders bounded Markdown to sanitized HTML.
package markdownsafe

import (
	"bytes"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/microcosm-cc/bluemonday"
	"github.com/yuin/goldmark/v2/parser"
	"github.com/yuin/goldmark/v2/renderer/html"
)

var ErrInvalid = errors.New("invalid markdown")

var policy = func() *bluemonday.Policy {
	p := bluemonday.UGCPolicy()
	p.AllowAttrs("class").Matching(bluemonday.SpaceSeparatedTokens).OnElements("code")
	return p
}()

// Render disables raw HTML at the parser boundary and sanitizes the generated
// HTML. The returned text is safe for insertion into an element's innerHTML.
func Render(source string, maxBytes int) (string, error) {
	if maxBytes < 1 || len(source) > maxBytes || !utf8.ValidString(source) || strings.ContainsRune(source, 0) {
		return "", ErrInvalid
	}
	raw := []byte(source)
	doc := parser.New().Parse(raw)
	var out bytes.Buffer
	if err := html.New().Render(&out, raw, doc); err != nil {
		return "", err
	}
	return policy.Sanitize(out.String()), nil
}
