package sandbox

import "testing"

func TestSignedOutcomeFencesUntrustedOutput(t *testing.T) {
	key := []byte("per-container-independent-key-1234")
	s := Sign(Outcome{ExitCode: 0, Stdout: []byte("answer"), MemoryKB: 42, MemoryMeasured: true}, key)
	if !Verify(s, key) {
		t.Fatal("valid MAC")
	}
	s.Outcome.MemoryKB = 0
	if Verify(s, key) {
		t.Fatal("forged telemetry")
	}
	s = Sign(Outcome{}, key)
	if Verify(s, []byte("other key")) {
		t.Fatal("different container key")
	}
}
