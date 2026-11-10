package submission

import (
	"biue/internal/judge"
	"strings"
	"testing"
	"time"
)

func TestTerminalSSEContainsOnlySummaryAndEnds(t *testing.T) {
	_, r, store, _ := handlerFixture(t, permanent())
	store.record = Submission{Summary: Summary{ID: 9, Generation: 1, Status: judge.Accepted, Kind: "Submit", UpdatedAt: time.Now()}, SourceCode: "PRIVATE_SOURCE", Output: "HIDDEN_OUTPUT", ErrorMessage: "HIDDEN_DIAGNOSTIC"}
	w := call(r, "GET", "/api/submissions/9/events", "", true)
	if w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/event-stream") || w.Header().Get("X-Accel-Buffering") != "no" || !strings.Contains(w.Body.String(), "event: end") {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, secret := range []string{"PRIVATE_SOURCE", "HIDDEN_OUTPUT", "HIDDEN_DIAGNOSTIC", "source_code", "stdin"} {
		if strings.Contains(w.Body.String(), secret) {
			t.Fatal("SSE leaked " + secret)
		}
	}
}
func TestSSEStreamCapacityIsBounded(t *testing.T) {
	const id = 999999
	defer func() {
		for i := 0; i < 2; i++ {
			releaseStream(id)
		}
	}()
	if !acquireStream(id) || !acquireStream(id) || acquireStream(id) {
		t.Fatal("per-user connection limit failed")
	}
}
