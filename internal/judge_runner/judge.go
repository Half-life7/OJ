package judge_runner

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"unicode/utf8"

	"biue/internal/judge"
	"biue/internal/sandbox"
)

func (d *Docker) Judge(ctx context.Context, r judge.Request, emit func(judge.Status) error) (judge.Result, error) {
	result := judge.Result{Task: r.Task, Status: judge.Accepted, TotalCases: len(r.Cases)}
	if r.Kind == judge.Run {
		result.TotalCases = 1
	}
	if r.Validate() != nil {
		return result, ErrSandbox
	}
	language, _ := judge.LookupLanguage(r.Language)
	files := map[string][]byte{language.Filename: []byte(r.SourceCode)}
	if len(language.Compile) > 0 {
		if err := emit(judge.Compiling); err != nil {
			return result, err
		}
		out, artifacts, err := d.runSandbox(ctx, r, true, files, nil)
		if errors.Is(err, ErrArtifact) {
			result.Status = judge.CompileError
			result.ErrorMessage = "Compiler artifacts are missing, unsafe, or exceed the artifact limit."
			return result, nil
		}
		if err != nil {
			return result, err
		}
		if out.ExitCode != 0 || out.TimedOut || out.OutputExceeded {
			result.Status = judge.CompileError
			result.ErrorMessage = safeText(out.Stderr, judge.MaxRunBytes)
			if result.ErrorMessage == "" {
				result.ErrorMessage = "Compilation failed or exceeded compiler limits."
			}
			return result, nil
		}
		files = artifacts
	}
	if err := emit(judge.Running); err != nil {
		return result, err
	}
	if r.Kind == judge.Run {
		out, _, err := d.runSandbox(ctx, r, false, files, r.Stdin)
		if err != nil {
			return result, err
		}
		result.Status = verdict(out)
		result.ExecutionTimeMS, result.MemoryUsageKB, result.MemoryMeasured = out.TimeMS, out.MemoryKB, out.MemoryMeasured
		result.Output = strings.ToValidUTF8(string(out.Stdout), "�")
		if len(result.Output) > judge.MaxRunBytes {
			result.Output = ""
			result.Status = judge.RuntimeError
		}
		if result.Status == judge.Accepted {
			result.PassedCases = 1
		}
		return result, nil
	}
	result.MemoryMeasured = true
	for _, c := range r.Cases {
		out, _, err := d.runSandbox(ctx, r, false, files, c.Input)
		if err != nil {
			return result, err
		}
		status := verdict(out)
		if status == judge.Accepted && !equalTokens(out.Stdout, c.Expected) {
			status = judge.WrongAnswer
		}
		point := judge.CaseResult{ID: c.ID, Status: status, ExecutionTimeMS: out.TimeMS, MemoryUsageKB: out.MemoryKB, MemoryMeasured: out.MemoryMeasured}
		if status == judge.Accepted {
			point.Score = c.Score
			result.Score += c.Score
			result.PassedCases++
		} else if result.Status == judge.Accepted {
			result.Status = status
		}
		result.ExecutionTimeMS = max(result.ExecutionTimeMS, out.TimeMS)
		result.MemoryUsageKB = max(result.MemoryUsageKB, out.MemoryKB)
		result.MemoryMeasured = result.MemoryMeasured && out.MemoryMeasured
		result.Cases = append(result.Cases, point)
	}
	if !result.MemoryMeasured {
		result.MemoryUsageKB = 0
		for i := range result.Cases {
			result.Cases[i].MemoryUsageKB = 0
			result.Cases[i].MemoryMeasured = false
		}
	}
	return result, nil
}

func safeText(raw []byte, limit int) string {
	s := strings.ToValidUTF8(string(raw), "�")
	if len(s) <= limit {
		return s
	}
	s = s[:limit]
	for !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}

func verdict(o sandbox.Outcome) judge.Status {
	if o.TimedOut {
		return judge.TimeLimitExceeded
	}
	if o.OutputExceeded {
		return judge.RuntimeError
	}
	if o.MemoryExceeded {
		return judge.MemoryLimitExceeded
	}
	if o.ExitCode != 0 || o.OutputExceeded {
		return judge.RuntimeError
	}
	return judge.Accepted
}

// Compare ASCII whitespace-delimited tokens without allocating a token array.
func equalTokens(a, b []byte) bool {
	space := func(c byte) bool { return c == ' ' || c == '\n' || c == '\r' || c == '\t' || c == '\v' || c == '\f' }
	for {
		for len(a) > 0 && space(a[0]) {
			a = a[1:]
		}
		for len(b) > 0 && space(b[0]) {
			b = b[1:]
		}
		if len(a) == 0 || len(b) == 0 {
			return len(a) == 0 && len(b) == 0
		}
		i, j := 0, 0
		for i < len(a) && !space(a[i]) {
			i++
		}
		for j < len(b) && !space(b[j]) {
			j++
		}
		if !bytes.Equal(a[:i], b[:j]) {
			return false
		}
		a, b = a[i:], b[j:]
	}
}
