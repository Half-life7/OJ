// Package problem manages statements and immutable judge-data versions.
package problem

import (
	"context"
	"errors"
	"time"

	"biue/internal/testcase"
)

var (
	ErrInvalid     = errors.New("invalid problem request")
	ErrNotFound    = errors.New("problem not found")
	ErrForbidden   = errors.New("problem management forbidden")
	ErrConflict    = errors.New("problem revision conflict")
	ErrUnavailable = errors.New("problem storage unavailable")
)

// Actor is obtained from authenticated account data, never from request JSON.
// Its zero value grants only public, published read access.
type Actor struct {
	ID   uint64
	Role string
}

type Input struct {
	ProblemType   string   `json:"problem_type,omitempty"`
	CheckerMode   string   `json:"checker_mode,omitempty"`
	CheckerSource string   `json:"checker_source,omitempty"`
	Title         string   `json:"title"`
	Description   string   `json:"description"`
	InputFormat   string   `json:"input_format"`
	OutputFormat  string   `json:"output_format"`
	SampleInput   string   `json:"sample_input"`
	SampleOutput  string   `json:"sample_output"`
	TimeLimitMS   int64    `json:"time_limit_ms"`
	MemoryLimitMB int64    `json:"memory_limit_mb"`
	Difficulty    string   `json:"difficulty"`
	Tags          []string `json:"tags"`
	Status        string   `json:"status"`
	Visibility    string   `json:"visibility"`
	Revision      uint64   `json:"revision"`
}

type Problem struct {
	OutputCases     []string `json:"output_cases,omitempty"`
	DescriptionHTML string   `json:"description_html,omitempty"`
	InputHTML       string   `json:"input_html,omitempty"`
	OutputHTML      string   `json:"output_html,omitempty"`
	Input
	ID               uint64    `json:"id"`
	StatementVersion uint64    `json:"statement_version"`
	DataVersion      uint64    `json:"data_version"`
	CreatorID        uint64    `json:"creator_id"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

type ListOptions struct {
	Category                                   string
	Page, Limit                                int
	Query, Difficulty, Tag, Status, Visibility string
}

type Page struct {
	Items []Problem `json:"items"`
	Page  int       `json:"page"`
	Limit int       `json:"limit"`
	Total int64     `json:"total"`
}

type CaseInfo struct {
	ID          string `json:"id"`
	Score       int64  `json:"score"`
	InputBytes  int64  `json:"input_bytes"`
	OutputBytes int64  `json:"output_bytes"`
}

type DataVersion struct {
	Version    uint64     `json:"data_version"`
	Digest     string     `json:"digest"`
	CaseCount  int        `json:"case_count"`
	TotalBytes int64      `json:"total_bytes"`
	TotalScore int64      `json:"total_score"`
	CreatedBy  uint64     `json:"created_by"`
	CreatedAt  time.Time  `json:"created_at"`
	Cases      []CaseInfo `json:"cases,omitempty"`
	Revision   uint64     `json:"revision"`
}

type VersionPage struct {
	Items []DataVersion `json:"items"`
	Page  int           `json:"page"`
	Limit int           `json:"limit"`
	Total int64         `json:"total"`
}

// DataStore publishes complete immutable directories. A published directory
// is retained even if a database commit fails, because its outcome is unknown.
type DataStore interface {
	Write(context.Context, testcase.Dataset) (string, error)
	Read(context.Context, string) (testcase.Dataset, error)
}

const (
	ImportReplace = "replace"
	ImportMerge   = "merge"
)
