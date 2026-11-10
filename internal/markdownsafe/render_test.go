package markdownsafe

import (
	"errors"
	"strings"
	"testing"
)

func TestRenderSanitizesMarkdown(t *testing.T) {
	html, err := Render("# Title\n\n<script>alert(1)</script>\n\n[bad](javascript:alert(1))\n\n```go\nfmt.Println(1)\n```\n", 65536)
	if err != nil {
		t.Fatal(err)
	}
	for _, unsafe := range []string{"<script", "onerror", "javascript:"} {
		if strings.Contains(strings.ToLower(html), unsafe) {
			t.Fatalf("unsafe markdown survived: %s", html)
		}
	}
	if !strings.Contains(html, "<h1>Title</h1>") || !strings.Contains(html, `class="language-go"`) {
		t.Fatalf("safe markdown lost structure: %s", html)
	}
}

func TestRenderRejectsInvalidInput(t *testing.T) {
	for _, source := range []string{string([]byte{0xff}), "a\x00b", strings.Repeat("x", 17)} {
		max := 16
		if source == string([]byte{0xff}) || source == "a\x00b" {
			max = 16
		}
		if _, err := Render(source, max); !errors.Is(err, ErrInvalid) {
			t.Fatalf("source %q: %v", source, err)
		}
	}
}
