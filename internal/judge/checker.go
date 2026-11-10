package judge

import (
	"encoding/json"
	"io"
	"strings"
	"unicode/utf8"
)

func ValidateJudging(kind, mode, source string) error {
	if kind != "" && kind != "programming" && kind != "output_only" || mode != "" && mode != "tokens" && mode != "spj" {
		return ErrInvalidRequest
	}
	if mode == "spj" {
		if !utf8.ValidString(source) || len(source) > MaxSourceBytes || strings.TrimSpace(source) == "" || strings.ContainsRune(source, 0) {
			return ErrInvalidRequest
		}
	} else if source != "" {
		return ErrInvalidRequest
	}
	return nil
}

// ParseOutputs rejects duplicate keys instead of letting a later value replace
// a previous output; missing cases are graded WA, unknown cases are rejected.
func ParseOutputs(source string) (map[string]string, error) {
	if len(source) > MaxSourceBytes || !utf8.ValidString(source) {
		return nil, ErrInvalidRequest
	}
	d := json.NewDecoder(strings.NewReader(source))
	t, e := d.Token()
	if e != nil || t != json.Delim('{') {
		return nil, ErrInvalidRequest
	}
	out := map[string]string{}
	for d.More() {
		t, e = d.Token()
		if e != nil {
			return nil, ErrInvalidRequest
		}
		key, ok := t.(string)
		if !ok || !caseID.MatchString(key) {
			return nil, ErrInvalidRequest
		}
		if _, ok = out[key]; ok {
			return nil, ErrInvalidRequest
		}
		valueToken, e := d.Token()
		value, validString := valueToken.(string)
		if e != nil || !validString || len(value) > MaxSourceBytes {
			return nil, ErrInvalidRequest
		}
		out[key] = value
	}
	if _, e = d.Token(); e != nil {
		return nil, ErrInvalidRequest
	}
	if _, e = d.Token(); e != io.EOF {
		return nil, ErrInvalidRequest
	}
	return out, nil
}
