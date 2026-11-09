package config

import (
	"bytes"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"log/slog"
	"net"
	"os"
	"strings"
)

type WorkerConfig struct {
	URL                          string
	Token                        []byte
	Consumer, HealthAddr, CAFile string
}

func (WorkerConfig) String() string         { return "WorkerConfig{credentials:[REDACTED]}" }
func (w WorkerConfig) GoString() string     { return w.String() }
func (w WorkerConfig) LogValue() slog.Value { return slog.StringValue(w.String()) }

type RunnerConfig struct {
	Token                                              []byte
	Addr, Directory, Socket, CertFile, KeyFile, CAFile string
	Concurrency                                        int
}

func (RunnerConfig) String() string         { return "RunnerConfig{credentials:[REDACTED]}" }
func (r RunnerConfig) GoString() string     { return r.String() }
func (r RunnerConfig) LogValue() slog.Value { return slog.StringValue(r.String()) }

func WorkerFrom(getenv func(string) string, base Config) (WorkerConfig, error) {
	var cfg WorkerConfig
	var err error
	if cfg.URL, err = publicURL(getenv("RUNNER_URL")); err != nil {
		return cfg, invalid("RUNNER_URL", "must be an http(s) origin")
	}
	if base.Env == "production" && !strings.HasPrefix(cfg.URL, "https://") {
		return cfg, invalid("RUNNER_URL", "requires HTTPS in production")
	}
	if cfg.Token, err = runnerToken(getenv("RUNNER_TOKEN")); err != nil {
		return cfg, err
	}
	for _, key := range [][]byte{base.JWTSecret, base.CSRFSecret, base.EncryptionKey} {
		if bytes.Equal(key, cfg.Token) {
			return cfg, invalid("RUNNER_TOKEN", "must be independent of application secrets")
		}
	}
	cfg.Consumer = strings.TrimSpace(getenv("WORKER_CONSUMER"))
	if cfg.Consumer == "" {
		var random [16]byte
		if _, err := rand.Read(random[:]); err != nil {
			return cfg, errors.New("cannot generate worker identity")
		}
		cfg.Consumer = "worker-" + hex.EncodeToString(random[:])
	}
	if len(cfg.Consumer) > 128 {
		return cfg, invalid("WORKER_CONSUMER", "is too long")
	}
	for _, c := range cfg.Consumer {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return cfg, invalid("WORKER_CONSUMER", "must use ASCII letters, digits, underscores or hyphens")
		}
	}
	cfg.HealthAddr = getenv("WORKER_HEALTH_ADDR")
	if cfg.HealthAddr == "" {
		cfg.HealthAddr = "127.0.0.1:8081"
	}
	if !listenAddress(cfg.HealthAddr) {
		return cfg, invalid("WORKER_HEALTH_ADDR", "must be a host:port address")
	}
	cfg.CAFile = getenv("RUNNER_CA_FILE")
	return cfg, nil
}
func RunnerFrom(getenv func(string) string) (RunnerConfig, error) {
	var cfg RunnerConfig
	var err error
	if env := getenv("APP_ENV"); env != "" && env != "development" && env != "test" && env != "production" {
		return cfg, invalid("APP_ENV", "must be development, test, or production")
	}
	if cfg.Token, err = runnerToken(getenv("RUNNER_TOKEN")); err != nil {
		return cfg, err
	}
	cfg.Addr = getenv("RUNNER_ADDR")
	if cfg.Addr == "" {
		cfg.Addr = "127.0.0.1:8083"
	}
	if !listenAddress(cfg.Addr) {
		return cfg, invalid("RUNNER_ADDR", "must be a host:port address")
	}
	cfg.Directory = getenv("RUNNER_DATA_DIR")
	if cfg.Directory == "" {
		cfg.Directory = "oj-data/runner"
	}
	cfg.Socket = getenv("DOCKER_SOCKET")
	if cfg.Socket == "" {
		cfg.Socket = "/var/run/docker.sock"
	}
	cfg.CertFile, cfg.KeyFile, cfg.CAFile = getenv("RUNNER_TLS_CERT"), getenv("RUNNER_TLS_KEY"), getenv("RUNNER_CA_FILE")
	if (cfg.CertFile == "") != (cfg.KeyFile == "") {
		return cfg, invalid("RUNNER_TLS_CERT/RUNNER_TLS_KEY", "must be supplied together")
	}
	if getenv("APP_ENV") == "production" && cfg.CertFile == "" {
		return cfg, invalid("RUNNER_TLS_CERT/RUNNER_TLS_KEY", "are required in production")
	}
	raw := getenv("JUDGE_CONCURRENCY")
	if raw == "" {
		raw = "2"
	}
	cfg.Concurrency, err = integer("JUDGE_CONCURRENCY", raw, 1, 8)
	return cfg, err
}
func runnerToken(raw string) ([]byte, error) {
	key, err := secret("RUNNER_TOKEN", raw)
	if err != nil {
		return nil, err
	}
	if len(key) > 256 || strings.ContainsAny(raw, " \t") {
		return nil, invalid("RUNNER_TOKEN", "must be a 32 to 256 byte token without whitespace")
	}
	return key, nil
}
func listenAddress(raw string) bool {
	host, port, err := net.SplitHostPort(raw)
	if err != nil || host != "" && !validHost(host) {
		return false
	}
	_, err = integer("port", port, 1, 65535)
	return err == nil
}
func ClientTLS(caFile string) (*tls.Config, error) {
	cfg := &tls.Config{MinVersion: tls.VersionTLS12}
	if caFile != "" {
		data, err := os.ReadFile(caFile)
		if err != nil || len(data) > 1<<20 {
			return nil, errors.New("cannot load runner CA")
		}
		pool, err := x509.SystemCertPool()
		if err != nil {
			pool = x509.NewCertPool()
		}
		if !pool.AppendCertsFromPEM(data) {
			return nil, errors.New("invalid runner CA")
		}
		cfg.RootCAs = pool
	}
	return cfg, nil
}
