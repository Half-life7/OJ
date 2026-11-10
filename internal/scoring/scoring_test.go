package scoring

import (
	"math"
	"reflect"
	"testing"
	"time"
)

func fixture() Input {
	start := time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)
	return Input{Start: start, End: start.Add(3 * time.Hour), Teams: []Team{{1, true, true}, {2, true, true}, {3, false, true}, {4, true, false}}, Problems: []Problem{{1, map[string]int64{"a": 30, "b": 70}}}}
}
func sub(in Input, id, team uint64, minute int, status string, a, b int64) Submission {
	cases := map[string]int64{"a": a, "b": b}
	if status == "CompileError" || status == "SystemError" || status == "Cancelled" {
		cases = nil
	}
	return Submission{ID: id, TeamID: team, ProblemID: 1, At: in.Start.Add(time.Duration(minute) * time.Minute), Kind: "Submit", Status: status, Score: a + b, Cases: cases}
}
func TestIndependentOIAndIOI(t *testing.T) {
	in := fixture()
	in.Submissions = []Submission{sub(in, 1, 1, 10, "WrongAnswer", 30, 0), sub(in, 2, 1, 20, "WrongAnswer", 0, 70), sub(in, 3, 1, 30, "WrongAnswer", 30, 0), sub(in, 4, 2, 25, "Accepted", 30, 70)}
	oi, err := (OI{}).Calculate(in)
	if err != nil {
		t.Fatal(err)
	}
	if oi[0].TeamID != 2 || oi[1].Score != 70 || !oi[1].ImprovedAt.Equal(in.Submissions[1].At) {
		t.Fatalf("OI: %+v", oi)
	}
	ioi, err := (IOI{}).Calculate(in)
	if err != nil {
		t.Fatal(err)
	}
	if ioi[0].TeamID != 1 || ioi[0].Score != 100 || ioi[0].Problems[1].Cases["a"] != 30 || !ioi[0].ImprovedAt.Equal(in.Submissions[1].At) {
		t.Fatalf("IOI: %+v", ioi)
	}
	in.Submissions[0], in.Submissions[3] = in.Submissions[3], in.Submissions[0]
	rebuilt, err := (IOI{}).Calculate(in)
	if err != nil || !reflect.DeepEqual(ioi, rebuilt) {
		t.Fatalf("out of order: %v", err)
	}
}
func TestACMPenalties(t *testing.T) {
	in := fixture()
	in.Problems = append(in.Problems, Problem{2, map[string]int64{"a": 30, "b": 70}})
	in.Submissions = []Submission{sub(in, 1, 1, 1, "CompileError", 0, 0), sub(in, 2, 1, 2, "SystemError", 0, 0), sub(in, 3, 1, 3, "WrongAnswer", 0, 0), sub(in, 4, 1, 4, "RuntimeError", 0, 0), sub(in, 5, 1, 30, "Accepted", 30, 70), sub(in, 6, 1, 35, "WrongAnswer", 0, 0), sub(in, 7, 1, 40, "Accepted", 30, 70), sub(in, 8, 1, 5, "TimeLimitExceeded", 0, 0)}
	in.Submissions[7].ProblemID = 2
	rows, err := (ACM{}).Calculate(in)
	if err != nil {
		t.Fatal(err)
	}
	if rows[0].Penalty != 70 || rows[0].Solved != 1 || rows[0].Problems[2].Wrong != 1 || rows[0].Problems[2].Penalty != 0 {
		t.Fatalf("ACM: %+v", rows[0])
	}
}
func TestTiesAndEligibility(t *testing.T) {
	in := fixture()
	in.Teams = append(in.Teams, Team{5, true, true})
	in.Submissions = []Submission{sub(in, 1, 1, 10, "Accepted", 30, 70), sub(in, 2, 2, 10, "Accepted", 30, 70), sub(in, 3, 3, 1, "Accepted", 30, 70), sub(in, 4, 4, 1, "Accepted", 30, 70)}
	for _, engine := range []Engine{OI{}, IOI{}, ACM{}} {
		rows, err := engine.Calculate(in)
		if err != nil {
			t.Fatal(err)
		}
		if rows[0].Rank != 1 || rows[1].Rank != 1 || rows[2].Rank != 3 || rows[3].Rank != 0 || rows[4].Rank != 0 {
			t.Fatalf("ranks: %+v", rows)
		}
	}
	if points, err := Points(3, 1); err != nil || points != 100 {
		t.Fatal(points, err)
	}
	if points, err := Points(3, 3); err != nil || points != 33 {
		t.Fatal(points, err)
	}
}
func TestInvalidResultsAndRunExclusion(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Input)
	}{
		{"duplicate", func(in *Input) { in.Submissions = append(in.Submissions, in.Submissions[0]) }},
		{"before", func(in *Input) { in.Submissions[0].At = in.Start.Add(-time.Second) }},
		{"at end", func(in *Input) { in.Submissions[0].At = in.End }},
		{"pending", func(in *Input) { in.Submissions[0].Status = "Pending" }},
		{"wrong sum", func(in *Input) { in.Submissions[0].Score = 99 }},
		{"missing case", func(in *Input) { delete(in.Submissions[0].Cases, "a") }},
		{"unknown team", func(in *Input) { in.Submissions[0].TeamID = 99 }},
		{"overflow", func(in *Input) { in.Problems = append(in.Problems, Problem{2, map[string]int64{"z": math.MaxInt64}}) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := fixture()
			in.Submissions = []Submission{sub(in, 1, 1, 10, "Accepted", 30, 70)}
			tt.mutate(&in)
			for _, engine := range []Engine{OI{}, IOI{}, ACM{}} {
				if _, err := engine.Calculate(in); err != ErrInvalid {
					t.Fatalf("expected invalid, got %v", err)
				}
			}
		})
	}
	in := fixture()
	s := sub(in, 1, 1, 10, "Accepted", 30, 70)
	s.Kind = "Run"
	in.Submissions = []Submission{s}
	for _, engine := range []Engine{OI{}, IOI{}, ACM{}} {
		rows, err := engine.Calculate(in)
		if err != nil || rows[0].Score != 0 || rows[0].Submissions != 0 {
			t.Fatalf("run counted: %v", err)
		}
	}
}

func TestIOIZeroWeightCaseNeedsPassingEvidence(t *testing.T) {
	in := fixture()
	in.Problems[0].Cases["zero"] = 0
	s := sub(in, 1, 1, 10, "WrongAnswer", 30, 70)
	s.Cases["zero"] = 0
	in.Submissions = []Submission{s}
	rows, e := (IOI{}).Calculate(in)
	if e != nil || rows[0].Score != 100 || rows[0].Solved != 0 {
		t.Fatal(rows, e)
	}
	in.Submissions[0].PassedCases = map[string]bool{"zero": true}
	rows, e = (IOI{}).Calculate(in)
	if e != nil || rows[0].Solved != 1 {
		t.Fatal(rows, e)
	}
}
