// Package scoring rebuilds contest standings from authoritative submission results.
package scoring

import (
	"errors"
	"math"
	"sort"
	"time"
)

var ErrInvalid = errors.New("invalid scoring input")

type Problem struct {
	ID    uint64
	Cases map[string]int64
}
type Team struct {
	ID               uint64
	Official, Active bool
}
type Submission struct {
	ID, TeamID, ProblemID uint64
	At                    time.Time
	Kind, Status          string
	Score                 int64
	Cases                 map[string]int64
	PassedCases           map[string]bool
}
type Input struct {
	Start, End  time.Time
	Teams       []Team
	Problems    []Problem
	Submissions []Submission
}
type ProblemResult struct {
	Score       int64            `json:"score"`
	Accepted    bool             `json:"accepted"`
	Attempts    int64            `json:"attempts"`
	Wrong       int64            `json:"wrong"`
	Penalty     int64            `json:"penalty"`
	Cases       map[string]int64 `json:"cases,omitempty"`
	PassedCases map[string]bool  `json:"passed_cases,omitempty"`
}
type Row struct {
	TeamID      uint64                    `json:"team_id"`
	Rank        int                       `json:"rank"`
	Score       int64                     `json:"score"`
	Solved      int                       `json:"solved"`
	Penalty     int64                     `json:"penalty"`
	ImprovedAt  time.Time                 `json:"improved_at"`
	Submissions int                       `json:"submissions"`
	Official    bool                      `json:"official"`
	Active      bool                      `json:"active"`
	Problems    map[uint64]*ProblemResult `json:"problems"`
}
type Engine interface{ Calculate(Input) ([]Row, error) }
type OI struct{}
type ACM struct{}
type IOI struct{}

func New(mode string) (Engine, error) {
	switch mode {
	case "OI":
		return OI{}, nil
	case "ACM":
		return ACM{}, nil
	case "IOI":
		return IOI{}, nil
	}
	return nil, ErrInvalid
}

func prepare(in Input) (map[uint64]*Row, []Submission, error) {
	if in.Start.IsZero() || !in.End.After(in.Start) {
		return nil, nil, ErrInvalid
	}
	rows := map[uint64]*Row{}
	problems := map[uint64]Problem{}
	var total int64
	for _, p := range in.Problems {
		if p.ID == 0 || len(p.Cases) == 0 {
			return nil, nil, ErrInvalid
		}
		if _, ok := problems[p.ID]; ok {
			return nil, nil, ErrInvalid
		}
		for id, score := range p.Cases {
			if id == "" || score < 0 || total > math.MaxInt64-score {
				return nil, nil, ErrInvalid
			}
			total += score
		}
		problems[p.ID] = p
	}
	for _, t := range in.Teams {
		if t.ID == 0 || rows[t.ID] != nil {
			return nil, nil, ErrInvalid
		}
		r := &Row{TeamID: t.ID, Official: t.Official, Active: t.Active, ImprovedAt: in.Start, Problems: map[uint64]*ProblemResult{}}
		for _, p := range in.Problems {
			r.Problems[p.ID] = &ProblemResult{Cases: map[string]int64{}}
		}
		rows[t.ID] = r
	}
	seen := map[uint64]bool{}
	submissions := make([]Submission, 0, len(in.Submissions))
	for _, s := range in.Submissions {
		if s.Kind == "Run" {
			continue
		}
		if s.Kind != "Submit" || s.ID == 0 || seen[s.ID] || rows[s.TeamID] == nil || s.At.Before(in.Start) || !s.At.Before(in.End) {
			return nil, nil, ErrInvalid
		}
		seen[s.ID] = true
		p, ok := problems[s.ProblemID]
		if !ok {
			return nil, nil, ErrInvalid
		}
		switch s.Status {
		case "CompileError", "SystemError", "Cancelled":
			if s.Score != 0 || len(s.Cases) != 0 {
				return nil, nil, ErrInvalid
			}
		case "Accepted", "WrongAnswer", "TimeLimitExceeded", "MemoryLimitExceeded", "RuntimeError":
			if len(s.Cases) != len(p.Cases) {
				return nil, nil, ErrInvalid
			}
			var sum int64
			for id, v := range s.Cases {
				limit, exists := p.Cases[id]
				if !exists || v < 0 || v > limit {
					return nil, nil, ErrInvalid
				}
				sum += v
				if s.Status == "Accepted" && v != limit {
					return nil, nil, ErrInvalid
				}
			}
			if sum != s.Score {
				return nil, nil, ErrInvalid
			}
		default:
			return nil, nil, ErrInvalid
		}
		submissions = append(submissions, s)
	}
	sort.Slice(submissions, func(i, j int) bool {
		if submissions[i].At.Equal(submissions[j].At) {
			return submissions[i].ID < submissions[j].ID
		}
		return submissions[i].At.Before(submissions[j].At)
	})
	return rows, submissions, nil
}

func (OI) Calculate(in Input) ([]Row, error) {
	rows, subs, err := prepare(in)
	if err != nil {
		return nil, err
	}
	for _, s := range subs {
		r := rows[s.TeamID]
		p := r.Problems[s.ProblemID]
		r.Submissions++
		p.Attempts++
		if s.Score > p.Score {
			r.Score += s.Score - p.Score
			p.Score = s.Score
			r.ImprovedAt = s.At
		}
		if s.Status == "Accepted" && !p.Accepted {
			p.Accepted = true
			r.Solved++
		}
	}
	return rank(rows, false), nil
}

func (IOI) Calculate(in Input) ([]Row, error) {
	rows, subs, err := prepare(in)
	if err != nil {
		return nil, err
	}
	for _, s := range subs {
		r := rows[s.TeamID]
		p := r.Problems[s.ProblemID]
		r.Submissions++
		p.Attempts++
		for id, v := range s.Cases {
			if p.PassedCases == nil {
				p.PassedCases = map[string]bool{}
			}
			if s.Status == "Accepted" || s.PassedCases[id] || v > 0 {
				p.PassedCases[id] = true
			}
			if v > p.Cases[id] {
				delta := v - p.Cases[id]
				p.Cases[id] = v
				p.Score += delta
				r.Score += delta
				r.ImprovedAt = s.At
			}
		}
	}
	for _, r := range rows {
		for _, problem := range in.Problems {
			p := r.Problems[problem.ID]
			complete := true
			for id, score := range problem.Cases {
				if p.Cases[id] != score || !p.PassedCases[id] {
					complete = false
				}
			}
			// A zero-weight case still requires evidence that a submission passed it;
			// aggregate scores alone cannot establish acceptance for an all-zero problem.
			var maximum int64
			for _, score := range problem.Cases {
				maximum += score
			}
			if complete && maximum > 0 {
				p.Accepted = true
				r.Solved++
			}
		}
	}
	return rank(rows, false), nil
}

func (ACM) Calculate(in Input) ([]Row, error) {
	rows, subs, err := prepare(in)
	if err != nil {
		return nil, err
	}
	for _, s := range subs {
		r := rows[s.TeamID]
		p := r.Problems[s.ProblemID]
		r.Submissions++
		p.Attempts++
		if p.Accepted {
			continue
		}
		switch s.Status {
		case "Accepted":
			// Floor elapsed minutes, independently of the wall-clock minute boundary.
			elapsed := int64(s.At.Sub(in.Start) / time.Minute)
			if p.Wrong > (math.MaxInt64-elapsed)/20 {
				return nil, ErrInvalid
			}
			penalty := elapsed + p.Wrong*20
			if r.Penalty > math.MaxInt64-penalty {
				return nil, ErrInvalid
			}
			p.Accepted = true
			p.Penalty = penalty
			r.Penalty += penalty
			r.Solved++
			p.Score = s.Score
			r.Score += s.Score
		case "WrongAnswer", "TimeLimitExceeded", "MemoryLimitExceeded", "RuntimeError":
			p.Wrong++
		}
	}
	return rank(rows, true), nil
}

func rank(rows map[uint64]*Row, acm bool) []Row {
	result := make([]Row, 0, len(rows))
	for _, r := range rows {
		result = append(result, *r)
	}
	better := func(a, b Row) bool {
		if acm {
			if a.Solved != b.Solved {
				return a.Solved > b.Solved
			}
			return a.Penalty < b.Penalty
		}
		if a.Score != b.Score {
			return a.Score > b.Score
		}
		return a.ImprovedAt.Before(b.ImprovedAt)
	}
	sort.Slice(result, func(i, j int) bool {
		a, b := result[i], result[j]
		ae, be := a.Official && a.Active, b.Official && b.Active
		if ae != be {
			return ae
		}
		if better(a, b) {
			return true
		}
		if better(b, a) {
			return false
		}
		return a.TeamID < b.TeamID
	})
	n, previous := 0, -1
	for i := range result {
		if !result[i].Official || !result[i].Active {
			continue
		}
		n++
		result[i].Rank = n
		if previous >= 0 && !better(result[previous], result[i]) && !better(result[i], result[previous]) {
			result[i].Rank = result[previous].Rank
		}
		previous = i
	}
	return result
}

// Points is the default, integer-only round-half-up competition points policy.
func Points(teams, rank int) (int, error) {
	if teams < 1 || rank < 1 || rank > teams || teams > 1000000 {
		return 0, ErrInvalid
	}
	return (200*(teams-rank+1) + teams) / (2 * teams), nil
}
