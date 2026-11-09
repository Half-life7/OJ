package judge

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"
)

var (
	ErrRunnerUnavailable = errors.New("judge runner unavailable")
	ErrRunnerProtocol    = errors.New("invalid judge runner response")
	ErrRunnerBusy        = errors.New("judge runner capacity unavailable")
	ErrRequestData       = errors.New("judge request data unavailable")
)

type RunnerJob struct {
	Request    Request `json:"request"`
	LeaseToken string  `json:"lease_token"`
}

type Runner interface {
	Ready(context.Context) error
	Execute(context.Context, RunnerJob, func(Status) error) (Result, error)
}

// RunnerBudget includes compilation, per-case control overhead and cleanup.
func RunnerBudget(r Request) time.Duration {
	n := len(r.Cases)
	if r.Kind == Run {
		n = 1
	}
	return time.Minute + time.Duration(n)*(time.Duration(r.TimeLimitMS)*time.Millisecond+5*time.Second)
}

type RunnerClient struct {
	origin, token string
	client        *http.Client
}

func NewRunnerClient(origin, token string, tlsConfig *tls.Config) (*RunnerClient, error) {
	u, err := url.Parse(origin)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.ForceQuery || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" || u.RawPath != "" || (u.Path != "" && u.Path != "/") || len(token) < 32 || len(token) > 256 || strings.ContainsAny(token, " \t\r\n\x00") {
		return nil, ErrRunnerUnavailable
	}
	u.Path = ""
	transport := &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext, TLSClientConfig: tlsConfig, TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 15 * time.Second, IdleConnTimeout: time.Minute, MaxConnsPerHost: 8, MaxIdleConnsPerHost: 4, DisableCompression: true}
	return &RunnerClient{origin: u.String(), token: token, client: &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

func (c *RunnerClient) Close() { c.client.CloseIdleConnections() }

func (c *RunnerClient) Ready(ctx context.Context) error {
	return c.probe(ctx, "/ready")
}
func (c *RunnerClient) BackendReady(ctx context.Context) error { return c.probe(ctx, "/engine-ready") }
func (c *RunnerClient) probe(ctx context.Context, path string) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	r, _ := http.NewRequestWithContext(ctx, http.MethodGet, c.origin+path, nil)
	r.Header.Set("Authorization", "Bearer "+c.token)
	response, err := c.client.Do(r)
	if err != nil {
		return ErrRunnerUnavailable
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return ErrRunnerUnavailable
	}
	return nil
}

func (c *RunnerClient) Execute(ctx context.Context, job RunnerJob, phase func(Status) error) (Result, error) {
	if job.Request.Validate() != nil || !validLease(Lease{Task: job.Request.Task, Token: job.LeaseToken}) || phase == nil {
		return Result{}, ErrInvalidRequest
	}
	ctx, cancel := context.WithTimeout(ctx, RunnerBudget(job.Request))
	defer cancel()
	body, err := json.Marshal(job)
	if err != nil || len(body) > MaxRequestBytes {
		return Result{}, ErrInvalidRequest
	}
	r, _ := http.NewRequestWithContext(ctx, http.MethodPost, c.origin+"/v1/judge", bytes.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+c.token)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Accept", "application/x-ndjson")
	response, err := c.client.Do(r)
	if err != nil {
		return Result{}, ErrRunnerUnavailable
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusServiceUnavailable || response.StatusCode == http.StatusTooManyRequests {
		return Result{}, ErrRunnerBusy
	}
	if response.StatusCode == 400 || response.StatusCode == 409 || response.StatusCode == 422 {
		return Result{}, ErrRunnerProtocol
	}
	if response.StatusCode != http.StatusOK {
		return Result{}, ErrRunnerUnavailable
	}
	media, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || media != "application/x-ndjson" {
		return Result{}, ErrRunnerProtocol
	}
	scanner := bufio.NewScanner(io.LimitReader(response.Body, 2<<20))
	scanner.Buffer(make([]byte, 4096), 512<<10)
	state := Queued
	var result *Result
	count := 0
	for scanner.Scan() {
		if !utf8.Valid(scanner.Bytes()) {
			return Result{}, ErrRunnerProtocol
		}
		count++
		if count > 3 || result != nil {
			return Result{}, ErrRunnerProtocol
		}
		var event Event
		decoder := json.NewDecoder(bytes.NewReader(scanner.Bytes()))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&event) != nil {
			return Result{}, ErrRunnerProtocol
		}
		var extra any
		if decoder.Decode(&extra) != io.EOF {
			return Result{}, ErrRunnerProtocol
		}
		if event.Result != nil {
			if event.Phase != "" || ValidateResult(job.Request, *event.Result) != nil || !CanTransition(state, event.Result.Status) {
				return Result{}, ErrRunnerProtocol
			}
			result = event.Result
		} else {
			if event.Phase != Compiling && event.Phase != Running || !CanTransition(state, event.Phase) {
				return Result{}, ErrRunnerProtocol
			}
			if err := phase(event.Phase); err != nil {
				return Result{}, err
			}
			state = event.Phase
		}
	}
	if scanner.Err() != nil || result == nil {
		return Result{}, ErrRunnerProtocol
	}
	return *result, nil
}
