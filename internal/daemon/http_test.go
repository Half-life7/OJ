package daemon

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHealthAndGracefulCancellation(t *testing.T) {
	h := Health(func(context.Context) bool { return false })
	for path, status := range map[string]int{"/health": 200, "/ready": 503} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != status {
			t.Fatalf("%s=%d", path, w.Code)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- Serve(ctx, "127.0.0.1:0", h, "", "", func(ctx context.Context) error { close(started); <-ctx.Done(); return nil })
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("loop did not start")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("daemon did not stop")
	}
}
