package judge

import (
	"testing"
)

func runRequest() Request {
	return Request{Task: TaskEnvelope{TaskID: 8, SubmissionID: 9, Generation: 1, Attempt: 1}, Kind: Run, Language: "python3", SourceCode: "print(1)", TimeLimitMS: 1000, MemoryLimitMB: 128, Stdin: []byte("saved input")}
}

func acceptedRun() Result {
	return Result{Task: runRequest().Task, Status: Accepted, TotalCases: 1, PassedCases: 1, Output: "1\n", ExecutionTimeMS: 2}
}

func submitRequest() Request {
	r := runRequest()
	r.Kind, r.Stdin = Submit, nil
	r.Cases = []Case{{ID: "a", Input: []byte("1"), Expected: []byte("1"), Score: 40}, {ID: "b", Score: 60}}
	return r
}

func acceptedSubmit() Result {
	r := acceptedRun()
	r.TotalCases, r.PassedCases, r.Score, r.Output = 2, 2, 100, ""
	r.Cases = []CaseResult{{ID: "a", Status: Accepted, Score: 40, ExecutionTimeMS: 1}, {ID: "b", Status: Accepted, Score: 60, ExecutionTimeMS: 2}}
	return r
}

func TestValidateResult(t *testing.T) {
	for _, test := range []struct {
		name string
		edit func(*Result)
	}{
		{"wrong task", func(r *Result) { r.Task.Attempt++ }},
		{"nonterminal", func(r *Result) { r.Status = Running }},
		{"negative resource", func(r *Result) { r.ExecutionTimeMS = -1 }},
		{"unmeasured memory", func(r *Result) { r.MemoryUsageKB = 1 }},
		{"wrong count", func(r *Result) { r.TotalCases = 1 }},
		{"missing case", func(r *Result) { r.Cases = r.Cases[:1] }},
		{"unknown case", func(r *Result) { r.Cases[0].ID = "unknown" }},
		{"reordered", func(r *Result) { r.Cases[0], r.Cases[1] = r.Cases[1], r.Cases[0] }},
		{"wrong score", func(r *Result) { r.Cases[0].Score++ }},
		{"score sum", func(r *Result) { r.Score = 40 }},
		{"passed count", func(r *Result) { r.PassedCases = 1 }},
		{"wrong verdict", func(r *Result) { r.Status = WrongAnswer }},
		{"resource aggregate", func(r *Result) { r.ExecutionTimeMS = 3 }},
		{"case nonterminal", func(r *Result) { r.Cases[0].Status = Compiling }},
		{"partial infrastructure result", func(r *Result) { r.Status = SystemError }},
		{"huge output", func(r *Result) { r.Output = string(make([]byte, MaxRunBytes+1)) }},
		{"invalid utf8", func(r *Result) { r.ErrorMessage = string([]byte{255}) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			r := acceptedSubmit()
			test.edit(&r)
			if ValidateResult(submitRequest(), r) != ErrInvalidResult {
				t.Fatal("invalid result accepted")
			}
		})
	}
	if err := ValidateResult(submitRequest(), acceptedSubmit()); err != nil {
		t.Fatal(err)
	}
	partial := acceptedSubmit()
	partial.Status, partial.Score, partial.PassedCases = WrongAnswer, 60, 1
	partial.Cases[0].Status, partial.Cases[0].Score = WrongAnswer, 0
	if err := ValidateResult(submitRequest(), partial); err != nil {
		t.Fatal(err)
	}
	for _, status := range []Status{CompileError, SystemError, Cancelled} {
		r := Result{Task: runRequest().Task, Status: status, TotalCases: 2}
		if err := ValidateResult(submitRequest(), r); err != nil {
			t.Fatal(err)
		}
	}
	if err := ValidateResult(runRequest(), acceptedRun()); err != nil {
		t.Fatal(err)
	}
	r := acceptedRun()
	r.Score = 1
	if ValidateResult(runRequest(), r) != ErrInvalidResult {
		t.Fatal("scored Run accepted")
	}
	r = acceptedRun()
	r.Status = WrongAnswer
	if ValidateResult(runRequest(), r) != ErrInvalidResult {
		t.Fatal("Run compared against hidden answer")
	}
}
