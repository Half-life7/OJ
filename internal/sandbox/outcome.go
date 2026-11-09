// Package sandbox defines the signed result produced by the immutable sandbox
// supervisor. Expected answers and database credentials never enter a sandbox.
package sandbox

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

type Outcome struct {
	ExitCode       int               `json:"exit_code"`
	TimeMS         int64             `json:"time_ms"`
	MemoryKB       int64             `json:"memory_kb"`
	MemoryMeasured bool              `json:"memory_measured"`
	MemoryExceeded bool              `json:"memory_exceeded"`
	TimedOut       bool              `json:"timed_out"`
	OutputExceeded bool              `json:"output_exceeded"`
	InternalError  bool              `json:"internal_error"`
	Stdout         []byte            `json:"stdout,omitempty"`
	Stderr         []byte            `json:"stderr,omitempty"`
	Artifacts      map[string][]byte `json:"artifacts,omitempty"`
}

type Input struct {
	Files map[string][]byte `json:"files"`
	Stdin []byte            `json:"stdin,omitempty"`
}

type Signed struct {
	Outcome Outcome `json:"outcome"`
	MAC     string  `json:"mac"`
}

func Sign(out Outcome, key []byte) Signed {
	mac := hmac.New(sha256.New, key)
	_ = json.NewEncoder(mac).Encode(out)
	return Signed{Outcome: out, MAC: hex.EncodeToString(mac.Sum(nil))}
}

func Verify(s Signed, key []byte) bool {
	expected := Sign(s.Outcome, key)
	return hmac.Equal([]byte(expected.MAC), []byte(s.MAC))
}
