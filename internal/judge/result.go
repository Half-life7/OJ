package judge

import (
	"errors"
	"unicode/utf8"
)

var ErrInvalidResult = errors.New("invalid judge result")

// ValidateResult binds a result to the authoritative request and checks complete
// per-case scoring. Compilation/infrastructure failures carry no partial score.
func ValidateResult(request Request, result Result) error {
	if request.Validate() != nil || result.Task != request.Task || !Terminal(result.Status) || result.Score < 0 || result.ExecutionTimeMS < 0 || result.MemoryUsageKB < 0 || !result.MemoryMeasured && result.MemoryUsageKB != 0 || len(result.Output) > MaxRunBytes || len(result.ErrorMessage) > MaxRunBytes || !utf8.ValidString(result.Output) || !utf8.ValidString(result.ErrorMessage) {
		return ErrInvalidResult
	}
	total := len(request.Cases)
	if request.Kind == Run {
		total = 1
	}
	if result.TotalCases != total || result.PassedCases < 0 || result.PassedCases > total {
		return ErrInvalidResult
	}
	if result.Status == CompileError || result.Status == SystemError || result.Status == Cancelled {
		if result.Score != 0 || result.PassedCases != 0 || len(result.Cases) != 0 {
			return ErrInvalidResult
		}
		return nil
	}
	if request.Kind == Run {
		passed := 0
		if result.Status == Accepted {
			passed = 1
		}
		if result.Status == WrongAnswer || result.Score != 0 || len(result.Cases) != 0 || result.PassedCases != passed {
			return ErrInvalidResult
		}
		return nil
	}
	if len(result.Cases) != total {
		return ErrInvalidResult
	}
	var score, maxTime, maxMemory int64
	passed := 0
	verdict := Accepted
	for i, c := range result.Cases {
		expected := request.Cases[i]
		if c.ID != expected.ID || c.ExecutionTimeMS < 0 || c.MemoryUsageKB < 0 || c.MemoryMeasured != result.MemoryMeasured || !c.MemoryMeasured && c.MemoryUsageKB != 0 {
			return ErrInvalidResult
		}
		switch c.Status {
		case Accepted:
			if c.Score != expected.Score {
				return ErrInvalidResult
			}
			passed++
		case WrongAnswer, TimeLimitExceeded, MemoryLimitExceeded, RuntimeError:
			if c.Score != 0 {
				return ErrInvalidResult
			}
			if verdict == Accepted {
				verdict = c.Status
			}
		default:
			return ErrInvalidResult
		}
		score += c.Score // Request.Validate bounds the sum by MaxInt64.
		maxTime = max(maxTime, c.ExecutionTimeMS)
		maxMemory = max(maxMemory, c.MemoryUsageKB)
	}
	if result.Status != verdict || result.Score != score || result.PassedCases != passed || result.ExecutionTimeMS != maxTime || result.MemoryUsageKB != maxMemory {
		return ErrInvalidResult
	}
	return nil
}
