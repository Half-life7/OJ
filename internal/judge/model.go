// Package judge defines the states, languages and task identities shared by
// submission creation, queue dispatch, workers and the isolated judge runner.
package judge

import (
	"errors"
	"strconv"
)

type Status string

const (
	Pending             Status = "Pending"
	Queued              Status = "Queued"
	Compiling           Status = "Compiling"
	Running             Status = "Running"
	Accepted            Status = "Accepted"
	WrongAnswer         Status = "WrongAnswer"
	TimeLimitExceeded   Status = "TimeLimitExceeded"
	MemoryLimitExceeded Status = "MemoryLimitExceeded"
	RuntimeError        Status = "RuntimeError"
	CompileError        Status = "CompileError"
	SystemError         Status = "SystemError"
	Cancelled           Status = "Cancelled"
)

func ValidStatus(status Status) bool {
	switch status {
	case Pending, Queued, Compiling, Running, Accepted, WrongAnswer,
		TimeLimitExceeded, MemoryLimitExceeded, RuntimeError, CompileError, SystemError, Cancelled:
		return true
	default:
		return false
	}
}

// Terminal marks immutable outcomes. Rejudging creates a new generation; it
// must not use an ordinary transition to reopen or replace a terminal result.
func Terminal(status Status) bool {
	switch status {
	case Accepted, WrongAnswer, TimeLimitExceeded, MemoryLimitExceeded,
		RuntimeError, CompileError, SystemError, Cancelled:
		return true
	default:
		return false
	}
}

// CanTransition allows only an actual forward state change. A worker must also
// apply a database CAS for task, generation, attempt and lease identity. This
// function alone does not authorize an update or make a replay idempotent.
func CanTransition(from, to Status) bool {
	if !ValidStatus(from) || !ValidStatus(to) || Terminal(from) {
		return false
	}
	switch from {
	case Pending:
		return to == Queued || to == Cancelled || to == SystemError
	case Queued:
		return to == Compiling || to == Running || to == Cancelled || to == SystemError
	case Compiling:
		return to == Running || to == CompileError || to == SystemError || to == Cancelled
	case Running:
		return to == Accepted || to == WrongAnswer || to == TimeLimitExceeded ||
			to == MemoryLimitExceeded || to == RuntimeError || to == SystemError || to == Cancelled
	default:
		return false
	}
}

type Kind string

const (
	Run    Kind = "Run"
	Submit Kind = "Submit"
)

func ValidKind(kind Kind) bool { return kind == Run || kind == Submit }

// Language describes controlled commands executed inside the runner's selected
// language image. No shell or user-selected command is involved. HTTP language
// discovery should expose ID and Name rather than these internal commands.
type Language struct {
	ID         string
	Name       string
	Filename   string
	EditorMode string
	Compile    []string
	Execute    []string
}

var languageRegistry = [...]Language{
	{
		ID: "cpp17", Name: "GNU C++17", Filename: "main.cpp", EditorMode: "cpp",
		Compile: []string{"g++", "-std=c++17", "-O2", "-pipe", "-DONLINE_JUDGE", "-o", "main", "main.cpp"},
		Execute: []string{"./main"},
	},
	{
		ID: "go", Name: "Go", Filename: "main.go", EditorMode: "go",
		Compile: []string{"go", "build", "-trimpath", "-o", "main", "main.go"},
		Execute: []string{"./main"},
	},
	{
		ID: "python3", Name: "Python 3", Filename: "main.py", EditorMode: "python",
		Execute: []string{"python3", "-I", "-B", "main.py"},
	},
	{
		ID: "java", Name: "Java", Filename: "Main.java", EditorMode: "java",
		Compile: []string{"javac", "-encoding", "UTF-8", "Main.java"},
		Execute: []string{"java", "-XX:+UseSerialGC", "-XX:ActiveProcessorCount=1", "-XX:MaxRAMPercentage=60", "-Xss512k", "Main"},
	},
	{ID: "javascript", Name: "JavaScript (Node.js 22)", Filename: "main.js", EditorMode: "javascript", Execute: []string{"node", "--max-old-space-size=128", "main.js"}},
	{ID: "text", Name: "输出文件 JSON", Filename: "outputs.json", EditorMode: "json"},
}

// Languages returns the supported languages in a stable display order. Every
// command slice belongs to the caller; mutations cannot alter the registry.
func Languages() []Language {
	result := make([]Language, 0, len(languageRegistry))
	for _, language := range languageRegistry {
		result = append(result, cloneLanguage(language))
	}
	return result
}

func LookupLanguage(id string) (Language, bool) {
	for _, language := range languageRegistry {
		if language.ID == id {
			return cloneLanguage(language), true
		}
	}
	return Language{}, false
}

func cloneLanguage(language Language) Language {
	language.Compile = append([]string(nil), language.Compile...)
	language.Execute = append([]string(nil), language.Execute...)
	return language
}

// MaxAttempts includes the first execution and three automatic retries.
const MaxAttempts uint32 = 4

var ErrInvalidEnvelope = errors.New("invalid judge task envelope")

// TaskEnvelope identifies one execution attempt. It contains no source code,
// test data, storage paths or client-controlled runner commands. A future result
// write must additionally match the database lease token.
type TaskEnvelope struct {
	TaskID       uint64 `json:"task_id"`
	SubmissionID uint64 `json:"submission_id"`
	Generation   uint64 `json:"generation"`
	Attempt      uint32 `json:"attempt"`
}

func (e TaskEnvelope) Validate() error {
	if e.TaskID == 0 || e.SubmissionID == 0 || e.Generation == 0 || e.Attempt == 0 || e.Attempt > MaxAttempts {
		return ErrInvalidEnvelope
	}
	return nil
}

// StreamValues produces the fixed wire fields with canonical decimal strings.
// Dispatchers must call Validate before sending the values to a queue.
func (e TaskEnvelope) StreamValues() map[string]any {
	return map[string]any{
		"task_id":       strconv.FormatUint(e.TaskID, 10),
		"submission_id": strconv.FormatUint(e.SubmissionID, 10),
		"generation":    strconv.FormatUint(e.Generation, 10),
		"attempt":       strconv.FormatUint(uint64(e.Attempt), 10),
	}
}

// ParseEnvelope accepts exactly the four canonical string fields emitted by
// StreamValues. Missing, unknown, numeric, signed, padded and overflowing values
// are rejected, and failure never returns a partially usable identity.
func ParseEnvelope(values map[string]any) (TaskEnvelope, error) {
	if len(values) != 4 {
		return TaskEnvelope{}, ErrInvalidEnvelope
	}
	taskID, err := parseField(values, "task_id", 64)
	if err != nil {
		return TaskEnvelope{}, err
	}
	submissionID, err := parseField(values, "submission_id", 64)
	if err != nil {
		return TaskEnvelope{}, err
	}
	generation, err := parseField(values, "generation", 64)
	if err != nil {
		return TaskEnvelope{}, err
	}
	attempt, err := parseField(values, "attempt", 32)
	if err != nil {
		return TaskEnvelope{}, err
	}
	result := TaskEnvelope{TaskID: taskID, SubmissionID: submissionID, Generation: generation, Attempt: uint32(attempt)}
	if err := result.Validate(); err != nil {
		return TaskEnvelope{}, err
	}
	return result, nil
}

func parseField(values map[string]any, key string, bits int) (uint64, error) {
	raw, ok := values[key].(string)
	if !ok || len(raw) == 0 || len(raw) > 20 || raw[0] == '0' {
		return 0, ErrInvalidEnvelope
	}
	for i := range len(raw) {
		if raw[i] < '0' || raw[i] > '9' {
			return 0, ErrInvalidEnvelope
		}
	}
	value, err := strconv.ParseUint(raw, 10, bits)
	if err != nil {
		return 0, ErrInvalidEnvelope
	}
	return value, nil
}
