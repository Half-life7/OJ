package judge

import (
	"errors"
	"math"
	"regexp"
	"strings"
	"unicode/utf8"
)

const (
	MaxSourceBytes     = 64 << 10
	MaxRunBytes        = 64 << 10
	MaxCaseBytes       = 16 << 20
	MaxDatasetBytes    = 64 << 20
	MaxRequestBytes    = 96 << 20
	MaxArtifactBytes   = 16 << 20
	MaxArtifactEntries = 256
)

var ErrInvalidRequest = errors.New("invalid judge request")
var caseID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)

type Case struct {
	ID       string `json:"id"`
	Input    []byte `json:"input"`
	Expected []byte `json:"expected"`
	Score    int64  `json:"score"`
}

type Request struct {
	Task          TaskEnvelope `json:"task"`
	Kind          Kind         `json:"kind"`
	Language      string       `json:"language"`
	SourceCode    string       `json:"source_code"`
	TimeLimitMS   int64        `json:"time_limit_ms"`
	MemoryLimitMB int64        `json:"memory_limit_mb"`
	Cases         []Case       `json:"cases,omitempty"`
	Stdin         []byte       `json:"stdin,omitempty"`
	ProblemType   string       `json:"problem_type,omitempty"`
	CheckerMode   string       `json:"checker_mode,omitempty"`
	CheckerSource string       `json:"checker_source,omitempty"`
}

func (r Request) Validate() error {
	if ValidateJudging(r.ProblemType, r.CheckerMode, r.CheckerSource) != nil {
		return ErrInvalidRequest
	}
	if r.ProblemType == "output_only" {
		if r.Kind != Submit || r.Language != "text" {
			return ErrInvalidRequest
		}
		if _, e := ParseOutputs(r.SourceCode); e != nil {
			return e
		}
	} else if r.Language == "text" {
		return ErrInvalidRequest
	}
	if r.Task.Validate() != nil || !ValidKind(r.Kind) || !utf8.ValidString(r.SourceCode) || len(r.SourceCode) == 0 || len(r.SourceCode) > MaxSourceBytes || r.TimeLimitMS < 1 || r.TimeLimitMS > 60000 || r.MemoryLimitMB < 16 || r.MemoryLimitMB > 4096 {
		return ErrInvalidRequest
	}
	if _, ok := LookupLanguage(r.Language); !ok {
		return ErrInvalidRequest
	}
	if r.Kind == Run {
		if len(r.Cases) != 0 || len(r.Stdin) > MaxRunBytes {
			return ErrInvalidRequest
		}
		return nil
	}
	if len(r.Stdin) != 0 || len(r.Cases) == 0 || len(r.Cases) > 512 {
		return ErrInvalidRequest
	}
	seen := make(map[string]bool, len(r.Cases))
	exact := map[string]bool{}
	var totalBytes, totalScore int64
	for _, c := range r.Cases {
		if r.CheckerMode == "spj" && len(c.Input)+len(c.Expected) > 4<<20 {
			return ErrInvalidRequest
		}
		fold := strings.ToLower(c.ID)
		if !caseID.MatchString(c.ID) || seen[fold] || len(c.Input) > MaxCaseBytes || len(c.Expected) > MaxCaseBytes || c.Score < 0 || c.Score > math.MaxInt64-totalScore {
			return ErrInvalidRequest
		}
		seen[fold] = true
		exact[c.ID] = true
		totalBytes += int64(len(c.Input)) + int64(len(c.Expected))
		totalScore += c.Score
		if totalBytes > MaxDatasetBytes {
			return ErrInvalidRequest
		}
	}
	if totalScore == 0 {
		return ErrInvalidRequest
	}
	if r.ProblemType == "output_only" {
		outputs, e := ParseOutputs(r.SourceCode)
		if e != nil {
			return e
		}
		for id := range outputs {
			if !exact[id] {
				return ErrInvalidRequest
			}
		}
	}
	return nil
}

type CaseResult struct {
	ID              string `json:"id"`
	Status          Status `json:"status"`
	Score           int64  `json:"score"`
	ExecutionTimeMS int64  `json:"execution_time_ms"`
	MemoryUsageKB   int64  `json:"memory_usage_kb"`
	MemoryMeasured  bool   `json:"memory_measured"`
}

type Result struct {
	Task            TaskEnvelope `json:"task"`
	Status          Status       `json:"status"`
	Score           int64        `json:"score"`
	ExecutionTimeMS int64        `json:"execution_time_ms"`
	MemoryUsageKB   int64        `json:"memory_usage_kb"`
	MemoryMeasured  bool         `json:"memory_measured"`
	PassedCases     int          `json:"passed_cases"`
	TotalCases      int          `json:"total_cases"`
	Output          string       `json:"output,omitempty"`
	ErrorMessage    string       `json:"error_message,omitempty"`
	Cases           []CaseResult `json:"cases,omitempty"`
}

type Event struct {
	Phase  Status  `json:"phase,omitempty"`
	Result *Result `json:"result,omitempty"`
}
