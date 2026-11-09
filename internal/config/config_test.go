package config

import (
	"bytes"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"
	_ "time/tzdata"
)

func validEnvironment() map[string]string {
	return map[string]string{
		"MYSQL_PASSWORD":     "strong-db-password!7284",
		"REDIS_PASSWORD":     "strong-cache-password!8529",
		"JWT_SECRET":         "aX7!qJ9mN2zB4vH6tR8wE0sY3uI5oP1d",
		"CSRF_SECRET":        "bY8@rK0nO3aC5wI7uS9xF1tZ4vJ6pQ2e",
		"APP_ENCRYPTION_KEY": "cZ9#sL1oP4bD6xJ8vT0yG2uA5wK7qR3f",
	}
}

func fromMap(env map[string]string) (Config, error) {
	return LoadFrom(func(key string) string { return env[key] })
}

func TestDefaults(t *testing.T) {
	cfg, err := fromMap(validEnvironment())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Env != "development" || cfg.Port != 8080 || cfg.MySQLHost != "127.0.0.1" || cfg.MySQLPort != 3306 || cfg.MySQLDatabase != "biue" || cfg.MySQLUser != "biue" {
		t.Fatalf("incorrect application/database defaults: %v", cfg)
	}
	if cfg.RedisAddr != "127.0.0.1:6379" || cfg.RedisDB != 0 || cfg.PublicURL != "http://localhost:8080" || cfg.Timezone != "Asia/Shanghai" {
		t.Fatalf("incorrect cache/public defaults: %v", cfg)
	}
	if cfg.LogLevel != slog.LevelInfo || cfg.SessionTTL != 12*time.Hour || cfg.BcryptCost != 12 || len(cfg.TrustedProxyCIDRs) != 0 {
		t.Fatal("incorrect security defaults")
	}
	if cfg.ProblemDataDir != "oj-data/problem-data" || cfg.ProblemImportConcurrency != 2 || cfg.JudgeQueueCapacity != 1000 {
		t.Fatal("incorrect problem storage defaults")
	}
}

func TestOverridesAndCredentialsArePreserved(t *testing.T) {
	env := validEnvironment()
	for key, value := range map[string]string{
		"APP_ENV": "production", "APP_PORT": "9000", "MYSQL_HOST": "mysql", "MYSQL_PORT": "3307", "MYSQL_DATABASE": "oj_test", "MYSQL_USER": "oj_user",
		"MYSQL_PASSWORD": "  valid-password-with-spaces!  ", "REDIS_ADDR": "[::1]:6380", "REDIS_DB": "3", "PUBLIC_URL": "https://judge.school.edu/", "TIMEZONE": "UTC",
		"LOG_LEVEL": "warn", "TRUSTED_PROXY_CIDRS": "10.2.3.4/8, ::1/128", "AUTH_SESSION_TTL": "30m", "BCRYPT_COST": "10",
	} {
		env[key] = value
	}
	cfg, err := fromMap(env)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Env != "production" || cfg.Port != 9000 || cfg.MySQLHost != "mysql" || cfg.MySQLPort != 3307 || cfg.MySQLDatabase != "oj_test" || cfg.MySQLUser != "oj_user" {
		t.Fatal("configuration overrides were not applied")
	}
	if cfg.MySQLPassword != env["MYSQL_PASSWORD"] || string(cfg.JWTSecret) != env["JWT_SECRET"] || string(cfg.CSRFSecret) != env["CSRF_SECRET"] || string(cfg.EncryptionKey) != env["APP_ENCRYPTION_KEY"] {
		t.Fatal("credentials must not be normalized")
	}
	if cfg.RedisAddr != "[::1]:6380" || cfg.RedisDB != 3 || cfg.PublicURL != "https://judge.school.edu" || cfg.Timezone != "UTC" || cfg.LogLevel != slog.LevelWarn || cfg.SessionTTL != 30*time.Minute || cfg.BcryptCost != 10 {
		t.Fatal("service/security overrides were not applied")
	}
	if len(cfg.TrustedProxyCIDRs) != 2 || cfg.TrustedProxyCIDRs[0].String() != "10.0.0.0/8" || cfg.TrustedProxyCIDRs[1].String() != "::1/128" {
		t.Fatal("trusted proxy ranges were not parsed and masked")
	}
}

func TestInvalidConfigurationDoesNotExposeValues(t *testing.T) {
	tests := []struct{ key, value string }{
		{"APP_ENV", "staging-private-value"},
		{"APP_PORT", "0"}, {"APP_PORT", "65536"}, {"APP_PORT", "not-a-number"},
		{"MYSQL_HOST", "mysql/private-host"}, {"MYSQL_PORT", "-1"}, {"MYSQL_DATABASE", "database\nprivate"}, {"MYSQL_USER", "user\x00private"},
		{"MYSQL_PASSWORD", ""}, {"MYSQL_PASSWORD", " \t "}, {"MYSQL_PASSWORD", "sensitive-password\x00"},
		{"REDIS_ADDR", "redis-private-host"}, {"REDIS_ADDR", "redis:0"}, {"REDIS_ADDR", "redis:99999"}, {"REDIS_ADDR", ":6379"}, {"REDIS_ADDR", "redis:private-port"},
		{"REDIS_PASSWORD", ""}, {"REDIS_DB", "-1"}, {"REDIS_DB", "16"},
		{"JWT_SECRET", "very-private-short-key"}, {"CSRF_SECRET", "example-secret-should-never-be-used-here"}, {"APP_ENCRYPTION_KEY", strings.Repeat("a", 64)},
		{"JWT_SECRET", "long-sensitive-key-1234567890123456\nmore"},
		{"PUBLIC_URL", "https://private-user:private-password@example.test"}, {"PUBLIC_URL", "javascript:private-value"}, {"PUBLIC_URL", "https://example.test/private-path"},
		{"PUBLIC_URL", "https://example.test?private=secret"}, {"PUBLIC_URL", "https://example.test?"}, {"PUBLIC_URL", "https://example.test#private-fragment"},
		{"PUBLIC_URL", "https://example.test:99999"}, {"PUBLIC_URL", "//example.test"}, {"PUBLIC_URL", "https://"},
		{"TIMEZONE", "Private/NonexistentZone"}, {"LOG_LEVEL", "private-level"},
		{"TRUSTED_PROXY_CIDRS", "private-proxy-value"}, {"TRUSTED_PROXY_CIDRS", "127.0.0.1/32,"},
		{"AUTH_SESSION_TTL", "0s"}, {"AUTH_SESSION_TTL", "59s"}, {"AUTH_SESSION_TTL", "-1h"}, {"AUTH_SESSION_TTL", "169h"}, {"AUTH_SESSION_TTL", "private-duration"},
		{"BCRYPT_COST", "9"}, {"BCRYPT_COST", "15"},
		{"PROBLEM_IMPORT_CONCURRENCY", "0"}, {"PROBLEM_IMPORT_CONCURRENCY", "9"}, {"PROBLEM_IMPORT_CONCURRENCY", "private-value"},
		{"PROBLEM_DATA_DIR", "private-directory\x00suffix"}, {"PROBLEM_DATA_DIR", "private-directory\nvalue"},
		{"JUDGE_QUEUE_CAPACITY", "0"}, {"JUDGE_QUEUE_CAPACITY", "100001"},
	}
	for _, tc := range tests {
		t.Run(tc.key+"/"+fmt.Sprint(len(tc.value)), func(t *testing.T) {
			env := validEnvironment()
			env[tc.key] = tc.value
			cfg, err := fromMap(env)
			if err == nil {
				t.Fatalf("expected %s validation failure", tc.key)
			}
			if !strings.Contains(err.Error(), tc.key) {
				t.Fatalf("error must identify the field: %v", err)
			}
			if len(tc.value) >= 8 && strings.Contains(err.Error(), tc.value) {
				t.Fatal("error exposed a configuration value")
			}
			if cfg.Env != "" || len(cfg.JWTSecret) != 0 || cfg.MySQLPassword != "" {
				t.Fatal("failure must return a zero configuration")
			}
		})
	}
}

func TestIndependentSecrets(t *testing.T) {
	for _, pair := range [][2]string{{"JWT_SECRET", "CSRF_SECRET"}, {"JWT_SECRET", "APP_ENCRYPTION_KEY"}, {"CSRF_SECRET", "APP_ENCRYPTION_KEY"}} {
		t.Run(pair[0]+"/"+pair[1], func(t *testing.T) {
			env := validEnvironment()
			env[pair[1]] = env[pair[0]]
			if _, err := fromMap(env); err == nil {
				t.Fatal("shared secrets must be rejected")
			}
		})
	}
}

func TestProductionPasswordChecks(t *testing.T) {
	for _, key := range []string{"MYSQL_PASSWORD", "REDIS_PASSWORD"} {
		for _, value := range []string{"short-key", "password12345", "administrator", "change-me-with-a-new-password"} {
			env := validEnvironment()
			env["APP_ENV"] = "production"
			env[key] = value
			if _, err := fromMap(env); err == nil {
				t.Fatalf("weak %s must be rejected in production", key)
			}
		}
	}
	env := validEnvironment()
	env["APP_ENV"] = "production"
	if _, err := fromMap(env); err != nil {
		t.Fatalf("production HTTP origin is explicitly supported by the requirements: %v", err)
	}
}

func TestKnownEnvironmentsAndLevels(t *testing.T) {
	for _, environment := range []string{"development", "test", "production"} {
		for _, level := range []string{"debug", "info", "warn", "error"} {
			env := validEnvironment()
			env["APP_ENV"] = environment
			env["LOG_LEVEL"] = level
			if _, err := fromMap(env); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestFormattingAndStructuredLogsAreRedacted(t *testing.T) {
	env := validEnvironment()
	cfg, err := fromMap(env)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&out, nil))
	logger.Info("configuration loaded", "config", cfg)
	for _, output := range []string{fmt.Sprintf("%v", cfg), fmt.Sprintf("%+v", cfg), fmt.Sprintf("%#v", cfg), out.String()} {
		if !strings.Contains(output, "REDACTED") {
			t.Fatal("configuration must identify redacted credentials")
		}
		for _, key := range []string{"MYSQL_PASSWORD", "REDIS_PASSWORD", "JWT_SECRET", "CSRF_SECRET", "APP_ENCRYPTION_KEY"} {
			if strings.Contains(output, env[key]) {
				t.Fatalf("formatting exposed %s", key)
			}
		}
	}
}

func TestNilEnvironmentReader(t *testing.T) {
	if _, err := LoadFrom(nil); err == nil {
		t.Fatal("nil environment reader must return a validation error")
	}
}
