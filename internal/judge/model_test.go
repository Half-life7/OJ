package judge

import (
	"errors"
	"math"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func TestStateMachineAllowsOnlyForwardLifecycleTransitions(t *testing.T) {
	states := []Status{Pending, Queued, Compiling, Running, Accepted, WrongAnswer, TimeLimitExceeded,
		MemoryLimitExceeded, RuntimeError, CompileError, SystemError, Cancelled}
	want := map[Status][]Status{
		Pending:   {Queued, Cancelled, SystemError},
		Queued:    {Compiling, Running, Cancelled, SystemError},
		Compiling: {Running, CompileError, SystemError, Cancelled},
		Running:   {Accepted, WrongAnswer, TimeLimitExceeded, MemoryLimitExceeded, RuntimeError, SystemError, Cancelled},
	}
	for i, from := range states {
		if !ValidStatus(from) {
			t.Fatalf("valid status rejected: %q", from)
		}
		if Terminal(from) != (i >= 4) {
			t.Fatalf("terminal classification incorrect: %q", from)
		}
		for _, to := range states {
			allowed := false
			for _, expected := range want[from] {
				if expected == to {
					allowed = true
				}
			}
			if got := CanTransition(from, to); got != allowed {
				t.Errorf("%s -> %s: got %v, want %v", from, to, got, allowed)
			}
		}
	}
	for _, invalid := range []Status{"", "accepted", "AC", "Unknown", "Pending "} {
		if ValidStatus(invalid) || Terminal(invalid) {
			t.Errorf("invalid status accepted: %q", invalid)
		}
		for _, valid := range states {
			if CanTransition(invalid, valid) || CanTransition(valid, invalid) {
				t.Errorf("transition with unknown status accepted: %q", invalid)
			}
		}
	}
}

func TestRunAndSubmitRemainDistinctKinds(t *testing.T) {
	if Run == Submit || !ValidKind(Run) || !ValidKind(Submit) {
		t.Fatal("Run and Submit must be distinct valid kinds")
	}
	for _, kind := range []Kind{"", "run", "submit", "Contest", "Run "} {
		if ValidKind(kind) {
			t.Errorf("invalid submission kind accepted: %q", kind)
		}
	}
}

func TestLanguageRegistryProvidesOnlyFixedSupportedCommands(t *testing.T) {
	languages := Languages()
	if len(languages) != 4 {
		t.Fatalf("expected four supported languages, got %d", len(languages))
	}
	for i, expected := range []struct{ id, filename, mode string }{
		{"cpp17", "main.cpp", "cpp"}, {"go", "main.go", "go"},
		{"python3", "main.py", "python"}, {"java", "Main.java", "java"},
	} {
		language := languages[i]
		if language.ID != expected.id || language.Filename != expected.filename || language.EditorMode != expected.mode || language.Name == "" || len(language.Execute) == 0 {
			t.Fatalf("incorrect language descriptor: %+v", language)
		}
		lookup, ok := LookupLanguage(expected.id)
		if !ok || !reflect.DeepEqual(lookup, language) {
			t.Fatalf("lookup differs from supported registry: %q", expected.id)
		}
		if expected.id == "python3" {
			if len(language.Compile) != 0 {
				t.Fatal("Python should run without a compilation stage")
			}
		} else if len(language.Compile) == 0 {
			t.Fatal("compiled language lacks a compile command")
		}
		for _, command := range [][]string{language.Compile, language.Execute} {
			if len(command) != 0 && (command[0] == "sh" || command[0] == "bash" || command[0] == "cmd" || command[0] == "powershell") {
				t.Fatal("language execution must not use a shell")
			}
		}
	}
	for _, id := range []string{"", "C++17", "python", "cpp17;rm", "CPP17", " go"} {
		if language, ok := LookupLanguage(id); ok || !reflect.DeepEqual(language, Language{}) {
			t.Errorf("unsupported language accepted: %q", id)
		}
	}
}

func TestLanguageDescriptorsCannotMutateGlobalCommands(t *testing.T) {
	baseline := Languages()
	listed := Languages()
	listed[0].ID = "changed"
	listed[0].Compile[0] = "uncontrolled"
	listed[0].Execute[0] = "uncontrolled"
	lookup, _ := LookupLanguage("java")
	lookup.Name = "changed"
	lookup.Compile[0] = "uncontrolled"
	lookup.Execute[0] = "uncontrolled"
	if !reflect.DeepEqual(Languages(), baseline) {
		t.Fatal("caller changed the global language registry")
	}
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			for range 20 {
				copy, _ := LookupLanguage("cpp17")
				copy.Compile[0] = "private-change"
				copy.Execute[0] = "private-change"
			}
		})
	}
	wg.Wait()
	if !reflect.DeepEqual(Languages(), baseline) {
		t.Fatal("concurrent callers changed the global registry")
	}
}

func TestEnvelopeRoundTripsCanonicalStringsWithoutAliasing(t *testing.T) {
	for _, envelope := range []TaskEnvelope{
		{TaskID: 1, SubmissionID: 2, Generation: 1, Attempt: 1},
		{TaskID: math.MaxUint64, SubmissionID: math.MaxUint64, Generation: math.MaxUint64, Attempt: 4},
	} {
		if err := envelope.Validate(); err != nil {
			t.Fatal(err)
		}
		values := envelope.StreamValues()
		if len(values) != 4 {
			t.Fatal("stream values must contain exactly four fields")
		}
		for key, value := range values {
			if _, ok := value.(string); !ok {
				t.Fatalf("field %s is not serialized as a string", key)
			}
		}
		parsed, err := ParseEnvelope(values)
		if err != nil || parsed != envelope {
			t.Fatalf("envelope changed: %+v %v", parsed, err)
		}
		values["task_id"] = "999"
		if envelope.StreamValues()["task_id"] == "999" {
			t.Fatal("stream maps share mutable state")
		}
	}
}

func TestEnvelopeValidationRejectsMissingIdentityAndExhaustedAttempts(t *testing.T) {
	for _, envelope := range []TaskEnvelope{
		{}, {TaskID: 0, SubmissionID: 2, Generation: 1, Attempt: 1},
		{TaskID: 1, SubmissionID: 0, Generation: 1, Attempt: 1},
		{TaskID: 1, SubmissionID: 2, Generation: 0, Attempt: 1},
		{TaskID: 1, SubmissionID: 2, Generation: 1, Attempt: 0},
		{TaskID: 1, SubmissionID: 2, Generation: 1, Attempt: 5},
		{TaskID: 1, SubmissionID: 2, Generation: 1, Attempt: math.MaxUint32},
	} {
		if !errors.Is(envelope.Validate(), ErrInvalidEnvelope) {
			t.Errorf("invalid identity accepted: %+v", envelope)
		}
	}
}

func TestParseEnvelopeRejectsUnknownMissingAndWrongTypes(t *testing.T) {
	valid := TaskEnvelope{TaskID: 1, SubmissionID: 2, Generation: 1, Attempt: 1}
	for _, field := range []string{"task_id", "submission_id", "generation", "attempt"} {
		for _, value := range []any{nil, 1, uint64(1), float64(1), []byte("1"), true, "", "0", "-1", "+1", "01", " 1", "1 ", "1.0", "1e1", "١", "18446744073709551616", strings.Repeat("9", 1024)} {
			values := valid.StreamValues()
			values[field] = value
			parsed, err := ParseEnvelope(values)
			if !errors.Is(err, ErrInvalidEnvelope) || parsed != (TaskEnvelope{}) {
				t.Fatalf("invalid %s value accepted or partial identity returned: %T %v", field, value, err)
			}
		}
		values := valid.StreamValues()
		delete(values, field)
		if parsed, err := ParseEnvelope(values); !errors.Is(err, ErrInvalidEnvelope) || parsed != (TaskEnvelope{}) {
			t.Fatalf("missing %s accepted", field)
		}
		values["unknown"] = "1"
		if parsed, err := ParseEnvelope(values); !errors.Is(err, ErrInvalidEnvelope) || parsed != (TaskEnvelope{}) {
			t.Fatalf("unknown field substituted for %s", field)
		}
	}
	values := valid.StreamValues()
	values["source_code"] = "private source"
	if parsed, err := ParseEnvelope(values); !errors.Is(err, ErrInvalidEnvelope) || parsed != (TaskEnvelope{}) || strings.Contains(err.Error(), "private source") {
		t.Fatal("unexpected fields or sensitive data accepted")
	}
	for _, value := range []string{"5", "4294967296", "4294967297"} {
		values := valid.StreamValues()
		values["attempt"] = value
		if parsed, err := ParseEnvelope(values); !errors.Is(err, ErrInvalidEnvelope) || parsed != (TaskEnvelope{}) {
			t.Fatalf("attempt exhaustion/overflow accepted: %s", value)
		}
	}
	if parsed, err := ParseEnvelope(nil); !errors.Is(err, ErrInvalidEnvelope) || parsed != (TaskEnvelope{}) {
		t.Fatal("nil message accepted")
	}
}
