package judge_runner

import (
	"biue/internal/judge"
	"context"
	"encoding/json"
	"testing"
)

func TestOutputOnlyUsesNoDockerAndGradesAllCases(t *testing.T) {
	r := judge.Request{Task: judge.TaskEnvelope{TaskID: 1, SubmissionID: 2, Generation: 1, Attempt: 1}, Kind: judge.Submit, Language: "text", ProblemType: "output_only", SourceCode: `{"a":"42"}`, TimeLimitMS: 1000, MemoryLimitMB: 128, Cases: []judge.Case{{ID: "a", Expected: []byte("42"), Score: 50}, {ID: "b", Expected: []byte("7"), Score: 50}}}
	var d *Docker
	result, e := d.Judge(context.Background(), r, func(judge.Status) error { return nil })
	if e != nil || result.Score != 50 || result.PassedCases != 1 || result.Status != judge.WrongAnswer || len(result.Cases) != 2 {
		t.Fatal(result, e)
	}
	if e = judge.ValidateResult(r, result); e != nil {
		t.Fatal(e)
	}
}

func TestCheckerPacketEmptyFilesRemainBase64Strings(t *testing.T) {
	raw, e := checkerPacket(judge.Case{}, nil)
	if e != nil {
		t.Fatal(e)
	}
	var fields map[string]any
	if json.Unmarshal(raw, &fields) != nil {
		t.Fatal("invalid JSON")
	}
	for _, name := range []string{"input", "expected", "output"} {
		if v, ok := fields[name].(string); !ok || v != "" {
			t.Fatalf("empty %s is not an empty base64 string", name)
		}
	}
}
