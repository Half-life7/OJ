// Package config loads validated process configuration from environment variables.
// It deliberately does not load .env files: the shell or container must inject them.
package config

import (
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config contains application configuration. Do not log individual secret fields.
// Its formatting and slog representations intentionally omit credentials and keys.
type Config struct {
	Env                      string
	Port                     int
	MySQLHost                string
	MySQLPort                int
	MySQLDatabase            string
	MySQLUser                string
	MySQLPassword            string
	RedisAddr                string
	RedisPassword            string
	RedisDB                  int
	JWTSecret                []byte
	CSRFSecret               []byte
	EncryptionKey            []byte
	PublicURL                string
	Timezone                 string
	LogLevel                 slog.Level
	TrustedProxyCIDRs        []netip.Prefix
	SessionTTL               time.Duration
	BcryptCost               int
	ProblemDataDir           string
	ProblemSampleDir         string
	ProblemImportConcurrency int
	JudgeQueueCapacity       int
}

// Load reads process environment variables and fails without exposing their values.
func Load() (Config, error) { return LoadFrom(os.Getenv) }

// LoadFrom allows deterministic configuration tests without modifying process state.
func LoadFrom(getenv func(string) string) (Config, error) {
	if getenv == nil {
		return Config{}, fmt.Errorf("configuration environment reader is required")
	}
	value := func(key, fallback string) string {
		if raw := strings.TrimSpace(getenv(key)); raw != "" {
			return raw
		}
		return fallback
	}
	var cfg Config
	var err error
	cfg.Env = value("APP_ENV", "development")
	switch cfg.Env {
	case "development", "test", "production":
	default:
		return Config{}, invalid("APP_ENV", "must be development, test, or production")
	}
	if cfg.Port, err = integer("APP_PORT", value("APP_PORT", "8080"), 1, 65535); err != nil {
		return Config{}, err
	}
	cfg.MySQLHost = value("MYSQL_HOST", "127.0.0.1")
	if !validHost(cfg.MySQLHost) {
		return Config{}, invalid("MYSQL_HOST", "must contain only a host name or IP address")
	}
	if cfg.MySQLPort, err = integer("MYSQL_PORT", value("MYSQL_PORT", "3306"), 1, 65535); err != nil {
		return Config{}, err
	}
	cfg.MySQLDatabase = value("MYSQL_DATABASE", "biue")
	cfg.MySQLUser = value("MYSQL_USER", "biue")
	if strings.ContainsAny(cfg.MySQLDatabase, "\x00\r\n") {
		return Config{}, invalid("MYSQL_DATABASE", "contains invalid characters")
	}
	if strings.ContainsAny(cfg.MySQLUser, "\x00\r\n") {
		return Config{}, invalid("MYSQL_USER", "contains invalid characters")
	}
	cfg.MySQLPassword = getenv("MYSQL_PASSWORD")
	if err = password("MYSQL_PASSWORD", cfg.MySQLPassword, cfg.Env == "production"); err != nil {
		return Config{}, err
	}
	cfg.RedisAddr = value("REDIS_ADDR", "127.0.0.1:6379")
	host, port, splitErr := net.SplitHostPort(cfg.RedisAddr)
	if splitErr != nil || !validHost(host) {
		return Config{}, invalid("REDIS_ADDR", "must be a host:port address")
	}
	if _, err = integer("REDIS_ADDR", port, 1, 65535); err != nil {
		return Config{}, err
	}
	cfg.RedisPassword = getenv("REDIS_PASSWORD")
	if err = password("REDIS_PASSWORD", cfg.RedisPassword, cfg.Env == "production"); err != nil {
		return Config{}, err
	}
	if cfg.RedisDB, err = integer("REDIS_DB", value("REDIS_DB", "0"), 0, 15); err != nil {
		return Config{}, err
	}
	if cfg.JWTSecret, err = secret("JWT_SECRET", getenv("JWT_SECRET")); err != nil {
		return Config{}, err
	}
	if cfg.CSRFSecret, err = secret("CSRF_SECRET", getenv("CSRF_SECRET")); err != nil {
		return Config{}, err
	}
	if cfg.EncryptionKey, err = secret("APP_ENCRYPTION_KEY", getenv("APP_ENCRYPTION_KEY")); err != nil {
		return Config{}, err
	}
	if string(cfg.JWTSecret) == string(cfg.CSRFSecret) || string(cfg.JWTSecret) == string(cfg.EncryptionKey) || string(cfg.CSRFSecret) == string(cfg.EncryptionKey) {
		return Config{}, fmt.Errorf("JWT_SECRET, CSRF_SECRET, and APP_ENCRYPTION_KEY must be different")
	}
	cfg.PublicURL, err = publicURL(value("PUBLIC_URL", "http://localhost:8080"))
	if err != nil {
		return Config{}, err
	}
	cfg.Timezone = value("TIMEZONE", "Asia/Shanghai")
	if _, err = time.LoadLocation(cfg.Timezone); err != nil {
		return Config{}, invalid("TIMEZONE", "must name an available IANA time zone")
	}
	switch value("LOG_LEVEL", "info") {
	case "debug":
		cfg.LogLevel = slog.LevelDebug
	case "info":
		cfg.LogLevel = slog.LevelInfo
	case "warn":
		cfg.LogLevel = slog.LevelWarn
	case "error":
		cfg.LogLevel = slog.LevelError
	default:
		return Config{}, invalid("LOG_LEVEL", "must be debug, info, warn, or error")
	}
	if raw := strings.TrimSpace(getenv("TRUSTED_PROXY_CIDRS")); raw != "" {
		for _, item := range strings.Split(raw, ",") {
			prefix, parseErr := netip.ParsePrefix(strings.TrimSpace(item))
			if parseErr != nil {
				return Config{}, invalid("TRUSTED_PROXY_CIDRS", "must be a comma-separated list of CIDR prefixes")
			}
			cfg.TrustedProxyCIDRs = append(cfg.TrustedProxyCIDRs, prefix.Masked())
		}
	}
	cfg.SessionTTL, err = time.ParseDuration(value("AUTH_SESSION_TTL", "12h"))
	if err != nil || cfg.SessionTTL < time.Minute || cfg.SessionTTL > 7*24*time.Hour {
		return Config{}, invalid("AUTH_SESSION_TTL", "must be a duration between 1m and 168h")
	}
	if cfg.BcryptCost, err = integer("BCRYPT_COST", value("BCRYPT_COST", "12"), 10, 14); err != nil {
		return Config{}, err
	}
	cfg.ProblemDataDir = value("PROBLEM_DATA_DIR", "oj-data/problem-data")
	if strings.ContainsAny(cfg.ProblemDataDir, "\x00\r\n") {
		return Config{}, invalid("PROBLEM_DATA_DIR", "contains invalid characters")
	}
	cfg.ProblemSampleDir = value("PROBLEM_SAMPLE_DIR", "导入样例")
	if strings.ContainsAny(cfg.ProblemSampleDir, "\x00\r\n") {
		return Config{}, invalid("PROBLEM_SAMPLE_DIR", "contains invalid characters")
	}
	if cfg.ProblemImportConcurrency, err = integer("PROBLEM_IMPORT_CONCURRENCY", value("PROBLEM_IMPORT_CONCURRENCY", "2"), 1, 8); err != nil {
		return Config{}, err
	}
	if cfg.JudgeQueueCapacity, err = integer("JUDGE_QUEUE_CAPACITY", value("JUDGE_QUEUE_CAPACITY", "1000"), 1, 100000); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func invalid(name, reason string) error { return fmt.Errorf("%s %s", name, reason) }

func integer(name, raw string, min, max int) (int, error) {
	value, err := strconv.Atoi(raw)
	if err != nil || value < min || value > max {
		return 0, invalid(name, fmt.Sprintf("must be an integer between %d and %d", min, max))
	}
	return value, nil
}

func validHost(host string) bool {
	if host == "" || strings.ContainsAny(host, " \t\r\n/\\@\x00") {
		return false
	}
	if strings.ContainsAny(host, ":[]") {
		_, err := netip.ParseAddr(host)
		return err == nil
	}
	return true
}

func password(name, value string, production bool) error {
	if strings.TrimSpace(value) == "" {
		return invalid(name, "is required")
	}
	if strings.ContainsRune(value, '\x00') {
		return invalid(name, "contains invalid characters")
	}
	if production && (len(value) < 12 || placeholder(value) || commonPassword(value)) {
		return invalid(name, "must be at least 12 bytes and must not be a common or placeholder password in production")
	}
	return nil
}

func secret(name, value string) ([]byte, error) {
	if len(value) < 32 || strings.TrimSpace(value) == "" {
		return nil, invalid(name, "must contain at least 32 bytes")
	}
	distinct := make(map[byte]struct{})
	for i := range len(value) {
		distinct[value[i]] = struct{}{}
	}
	if placeholder(value) || len(distinct) < 8 || strings.ContainsAny(value, "\x00\r\n") {
		return nil, invalid(name, "must be an independently generated secret, not a weak or placeholder value")
	}
	return []byte(value), nil
}

func placeholder(value string) bool {
	lower := strings.ToLower(value)
	for _, marker := range []string{"changeme", "change-me", "change_me", "example", "replace-me", "replace_me", "replace-with", "replace_with", "your-secret", "your_secret", "your-password", "your_password"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func commonPassword(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "password", "password123", "password1234", "password12345", "123456789012", "1234567890123456", "qwertyuiop123", "administrator", "adminpassword", "redispassword", "mysqlpassword":
		return true
	}
	return false
}

func publicURL(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.Opaque != "" || u.ForceQuery || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || u.RawPath != "" {
		return "", invalid("PUBLIC_URL", "must be an absolute http(s) origin without credentials, path, query, or fragment")
	}
	if !validHost(u.Hostname()) {
		return "", invalid("PUBLIC_URL", "must contain a valid host")
	}
	if port := u.Port(); port != "" {
		if _, err := integer("PUBLIC_URL", port, 1, 65535); err != nil {
			return "", err
		}
	}
	u.Path = ""
	return u.String(), nil
}

// String prevents accidental credential disclosure in ordinary formatted logs.
func (c Config) String() string {
	return fmt.Sprintf("Config{Env:%s Port:%d PublicURL:%s Credentials:[REDACTED]}", c.Env, c.Port, c.PublicURL)
}

// GoString also redacts the %#v format.
func (c Config) GoString() string { return c.String() }

// LogValue ensures structured slog logging does not expose credentials.
func (c Config) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("environment", c.Env),
		slog.Int("port", c.Port),
		slog.String("public_url", c.PublicURL),
		slog.String("credentials", "[REDACTED]"),
	)
}
