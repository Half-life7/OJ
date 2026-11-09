// Package submission persists version-pinned runs and submissions with an outbox.
package submission

import (
	"errors"
	"time"

	"biue/internal/judge"
)

var (
	ErrInvalid     = errors.New("invalid submission request")
	ErrForbidden   = errors.New("submission access forbidden")
	ErrNotFound    = errors.New("submission not found")
	ErrUnavailable = errors.New("submission storage unavailable")
	ErrQueueFull   = errors.New("judge queue capacity reached")
	ErrNoData      = errors.New("problem has no judge data")
)

type Actor struct {
	ID          uint64
	AccountType string
	Role        string
}

type CreateInput struct {
	ProblemID  uint64  `json:"problem_id"`
	Language   string  `json:"language"`
	SourceCode string  `json:"source_code"`
	Stdin      *string `json:"stdin,omitempty"`
}

type ListOptions struct {
	Page, Limit int
	ProblemID   uint64
	Kind        string
}

type Summary struct {
	ID               uint64       `json:"id" db:"id"`
	ProblemID        uint64       `json:"problem_id" db:"problem_id"`
	UserID           uint64       `json:"user_id" db:"user_id"`
	StatementVersion uint64       `json:"statement_version" db:"statement_version"`
	DataVersion      uint64       `json:"data_version" db:"data_version"`
	Generation       uint64       `json:"generation" db:"generation"`
	Kind             string       `json:"kind" db:"kind"`
	Language         string       `json:"language" db:"language"`
	Status           judge.Status `json:"status" db:"status"`
	Score            int64        `json:"score" db:"score"`
	ExecutionTimeMS  int64        `json:"execution_time_ms" db:"execution_time_ms"`
	MemoryUsageKB    int64        `json:"memory_usage_kb" db:"memory_usage_kb"`
	PassedCases      int          `json:"passed_cases" db:"passed_cases"`
	TotalCases       int          `json:"total_cases" db:"total_cases"`
	CreatedAt        time.Time    `json:"created_at" db:"created_at"`
	UpdatedAt        time.Time    `json:"updated_at" db:"updated_at"`
}

type Submission struct {
	Summary
	SourceCode   string  `json:"source_code" db:"source_code"`
	Stdin        *string `json:"stdin,omitempty" db:"stdin"`
	Output       string  `json:"output" db:"output"`
	ErrorMessage string  `json:"error_message" db:"error_message"`
}

type Page struct {
	Items []Summary `json:"items"`
	Page  int       `json:"page"`
	Limit int       `json:"limit"`
	Total int64     `json:"total"`
}
