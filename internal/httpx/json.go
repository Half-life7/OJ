// Package httpx provides the bounded JSON binding used by application APIs.
package httpx

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"unicode/utf8"
)

var ErrInvalidJSON = errors.New("invalid JSON request")

// JSONBinding keeps validation local to the route, without Gin global options.
type JSONBinding struct{ MaxBytes int64 }

func (JSONBinding) Name() string { return "strict-json" }

func (b JSONBinding) Bind(r *http.Request, target any) error {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" || r.Body == nil || b.MaxBytes <= 0 || b.MaxBytes > 1<<30 {
		return ErrInvalidJSON
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, b.MaxBytes+1))
	if err != nil || int64(len(raw)) > b.MaxBytes || !utf8.Valid(raw) {
		return ErrInvalidJSON
	}
	if trimmed := bytes.TrimSpace(raw); len(trimmed) == 0 || trimmed[0] != '{' {
		return ErrInvalidJSON
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(target) != nil {
		return ErrInvalidJSON
	}
	var extra any
	if !errors.Is(decoder.Decode(&extra), io.EOF) {
		return ErrInvalidJSON
	}
	return nil
}
