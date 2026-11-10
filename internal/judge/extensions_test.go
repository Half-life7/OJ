package judge

import (
	"strings"
	"testing"
)

func TestJudgingConfigurationAndOutputPayloads(t *testing.T) {
	for _, source := range []string{`[]`, `null`, `{"a":"x","a":"y"}`, `{"a":1}`, `{"a":null}`, `{"a":[]}`, `{"../a":"x"}`, `{} {}`, strings.Repeat("x", MaxSourceBytes+1)} {
		if _, e := ParseOutputs(source); e == nil {
			t.Fatalf("invalid output accepted: %q", source[:min(30, len(source))])
		}
	}
	values, e := ParseOutputs(`{"a":"line\n","zero":""}`)
	if e != nil || values["a"] != "line\n" {
		t.Fatal(values, e)
	}
	for _, args := range [][3]string{{"bad", "tokens", ""}, {"programming", "shell", ""}, {"", "spj", ""}, {"", "tokens", "secret"}} {
		if ValidateJudging(args[0], args[1], args[2]) == nil {
			t.Fatal(args)
		}
	}
	if ValidateJudging("output_only", "spj", "print('AC')") != nil {
		t.Fatal("valid checker rejected")
	}
}
func TestOutputOnlyRequestAndUnknownCaseRejected(t *testing.T) {
	r := Request{Task: TaskEnvelope{TaskID: 1, SubmissionID: 2, Generation: 1, Attempt: 1}, Kind: Submit, Language: "text", SourceCode: `{"a":"42"}`, ProblemType: "output_only", TimeLimitMS: 1000, MemoryLimitMB: 128, Cases: []Case{{ID: "a", Expected: []byte("42"), Score: 100}}}
	if e := r.Validate(); e != nil {
		t.Fatal(e)
	}
	r.SourceCode = `{"unknown":"42"}`
	if r.Validate() == nil {
		t.Fatal("unknown case accepted")
	}
	r.SourceCode = `{}`
	if r.Validate() != nil {
		t.Fatal("missing cases should grade WA")
	}
	r.Language = "python3"
	if r.Validate() == nil {
		t.Fatal("code language accepted for output-only")
	}
}
func TestAdditionalLanguagesAreFixedAndVirtualTextIsNotExecutable(t *testing.T) {
	js, ok := LookupLanguage("javascript")
	if !ok || js.Filename != "main.js" || len(js.Compile) != 0 || len(js.Execute) < 2 || js.Execute[0] != "node" {
		t.Fatal(js)
	}
	text, ok := LookupLanguage("text")
	if !ok || len(text.Execute) != 0 || len(text.Compile) != 0 {
		t.Fatal(text)
	}
}
