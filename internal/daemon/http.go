// Package daemon provides health probes and cooperative process shutdown.
package daemon

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

func Health(ready func(context.Context) bool) http.Handler {
	r := gin.New()
	r.SetTrustedProxies(nil)
	r.GET("/health", func(c *gin.Context) { c.Status(200) })
	r.GET("/ready", func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
		defer cancel()
		if ready(ctx) {
			c.Status(200)
		} else {
			c.Status(503)
		}
	})
	return r
}

func Serve(parent context.Context, addr string, handler http.Handler, cert, key string, loop func(context.Context) error) error {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return errors.New("cannot listen for service HTTP")
	}
	srv := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, IdleTimeout: time.Minute, MaxHeaderBytes: 16 << 10, BaseContext: func(net.Listener) context.Context { return ctx }, TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12}}
	webDone := make(chan error, 1)
	workDone := make(chan error, 1)
	go func() {
		if cert != "" {
			webDone <- srv.ServeTLS(listener, cert, key)
		} else {
			webDone <- srv.Serve(listener)
		}
	}()
	go func() { workDone <- loop(ctx) }()
	var result error
	webFinished, workFinished := false, false
	select {
	case <-ctx.Done():
	case err := <-webDone:
		webFinished = true
		if !errors.Is(err, http.ErrServerClosed) {
			result = errors.New("service HTTP failed")
		}
	case err := <-workDone:
		workFinished = true
		result = err
	}
	cancel()
	shutdown, stop := context.WithTimeout(context.Background(), 15*time.Second)
	defer stop()
	if srv.Shutdown(shutdown) != nil {
		_ = srv.Close()
		result = errors.New("service shutdown timed out")
	}
	if !workFinished {
		select {
		case err := <-workDone:
			if result == nil {
				result = err
			}
		case <-shutdown.Done():
			result = errors.New("service loop shutdown timed out")
		}
	}
	if !webFinished {
		select {
		case <-webDone:
		case <-shutdown.Done():
		}
	}
	return result
}

func Pause(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func Probe(addr, scheme, token string, client *http.Client) error {
	return ProbeAt(addr, scheme, token, "/ready", client)
}
func ProbeAt(addr, scheme, token, path string, client *http.Client) error {
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return errors.New("invalid health address")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	r, _ := http.NewRequestWithContext(ctx, "GET", scheme+"://127.0.0.1:"+port+path, nil)
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := client.Do(r)
	if err != nil {
		return errors.New("service is not ready")
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return errors.New("service is not ready")
	}
	return nil
}
