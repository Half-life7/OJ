package community

import (
	"errors"

	"biue/internal/markdownsafe"
)

// Render disables raw HTML at the parser/renderer boundary, then sanitizes the
// generated HTML. Code fences retain language classes without executable HTML.
func Render(body string) (string, error) {
	rendered, err := markdownsafe.Render(body, 65536)
	if errors.Is(err, markdownsafe.ErrInvalid) {
		return "", ErrInvalid
	}
	return rendered, err
}
