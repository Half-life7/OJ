package operations

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"biue/internal/auth"
	"biue/internal/problem"
	"biue/internal/testcase"

	"github.com/jmoiron/sqlx"
	"golang.org/x/crypto/bcrypt"
)

const simulatedUserCount = 36

type SimulationResult struct {
	ContestID          uint64   `json:"contest_id"`
	ProblemIDs         []uint64 `json:"problem_ids"`
	UsernamePrefix     string   `json:"username_prefix"`
	Usernames          []string `json:"usernames"`
	InitialPassword    string   `json:"initial_password"`
	Users              int      `json:"users"`
	PracticeSubmits    int      `json:"practice_submissions"`
	ContestSubmissions int      `json:"contest_submissions"`
	SimulationLabel    string   `json:"simulation_label"`
	Warnings           []string `json:"warnings"`
	Complete           bool     `json:"complete"`
}

type simulationProblem struct {
	input   problem.Input
	dataset testcase.Dataset
	correct string
}

// Simulate creates a self-contained, clearly labelled test fixture. Synthetic
// submissions are terminal rows without outbox entries; they never claim that
// an isolated runner executed.
func (s *Service) Simulate(ctx context.Context, actor auth.User) (SimulationResult, error) {
	if err := s.authorized(ctx, actor); err != nil {
		return SimulationResult{}, err
	}
	if s.db == nil || s.problems == nil || s.competition == nil {
		return SimulationResult{}, ErrUnavailable
	}
	suffix, err := randomHex(6)
	if err != nil {
		return SimulationResult{}, ErrUnavailable
	}
	passwordBytes := make([]byte, 18)
	if _, err := rand.Read(passwordBytes); err != nil {
		return SimulationResult{}, ErrUnavailable
	}
	password := "sim-" + base64.RawURLEncoding.EncodeToString(passwordBytes)
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return SimulationResult{}, ErrUnavailable
	}
	actorProblem := problem.Actor{ID: actor.ID, Role: actor.GlobalRole}
	definitions := simulationProblems(suffix)
	createdProblems := make([]problem.Problem, 0, len(definitions))
	for index, definition := range definitions {
		created, err := s.problems.ImportPackage(ctx, actorProblem, problem.ProblemPackage{
			Input: definition.input, Dataset: definition.dataset,
		}, fmt.Sprintf("simulate-%s-problem-%d", suffix, index+1))
		if err != nil {
			return SimulationResult{}, ErrUnavailable
		}
		createdProblems = append(createdProblems, created)
	}

	now := time.Now().UTC().Truncate(time.Microsecond)
	contestStart := now.Add(-3 * time.Hour)
	contestEnd := now.Add(-time.Hour)
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return SimulationResult{}, ErrUnavailable
	}
	defer tx.Rollback()
	contestResult, err := tx.ExecContext(ctx, `INSERT INTO contests(title,scoring_mode,enrollment_policy,approval_required,team_mode,starts_at,ends_at,revision,created_by,created_at) VALUES (?,?,?,?,?,?,?,1,?,?)`,
		"模拟赛 "+suffix, "OI", "admin_add", false, false, contestStart, contestEnd, actor.ID, now)
	if err != nil {
		return SimulationResult{}, ErrUnavailable
	}
	contestID, err := contestResult.LastInsertId()
	if err != nil || contestID < 1 {
		return SimulationResult{}, ErrUnavailable
	}
	for _, item := range createdProblems {
		if _, err := tx.ExecContext(ctx, `INSERT INTO contest_problems(contest_id,problem_id,statement_version,data_version) VALUES (?,?,?,?)`, contestID, item.ID, item.StatementVersion, item.DataVersion); err != nil {
			return SimulationResult{}, ErrUnavailable
		}
	}
	result := SimulationResult{
		ContestID: uint64(contestID), ProblemIDs: make([]uint64, 0, len(createdProblems)),
		UsernamePrefix: "sim" + suffix, InitialPassword: password, Users: simulatedUserCount,
		SimulationLabel: "测试中心模拟数据，不是真实 Runner 执行结果", Warnings: []string{}, Complete: true,
	}
	for _, item := range createdProblems {
		result.ProblemIDs = append(result.ProblemIDs, item.ID)
	}
	type simulatedAccount struct {
		UserID uint64
		TeamID uint64
	}
	for index := 0; index < simulatedUserCount; index++ {
		student := fmt.Sprintf("SIM%s%02d", strings.ToUpper(suffix), index+1)
		name := fmt.Sprintf("模拟选手 %02d", index+1)
		personResult, err := tx.ExecContext(ctx, `INSERT INTO persons(display_name,student_no,student_no_status,organization,revision,created_at,updated_at) VALUES (?,?,?,?,1,?,?)`,
			name, student, "verified", "Biue Test Center", now, now)
		if err != nil {
			return SimulationResult{}, ErrUnavailable
		}
		personID, err := personResult.LastInsertId()
		if err != nil || personID < 1 {
			return SimulationResult{}, ErrUnavailable
		}
		username := fmt.Sprintf("sim%s%02d", suffix, index+1)
		userResult, err := tx.ExecContext(ctx, `INSERT INTO users(username,password_hash,account_type,global_role,status,token_version,created_at,updated_at) VALUES (?,?,?,?,?,1,?,?)`,
			username, string(hash), "permanent", "user", "active", now, now)
		if err != nil {
			return SimulationResult{}, ErrUnavailable
		}
		userID, err := userResult.LastInsertId()
		if err != nil || userID < 1 {
			return SimulationResult{}, ErrUnavailable
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO person_accounts(user_id,person_id,created_at) VALUES (?,?,?)`, userID, personID, now); err != nil {
			return SimulationResult{}, ErrUnavailable
		}
		teamResult, err := tx.ExecContext(ctx, `INSERT INTO contest_teams(contest_id,name,participant_type,status,created_at) VALUES (?,?,?,?,?)`,
			contestID, fmt.Sprintf("模拟队 %02d", index+1), "official", "active", now)
		if err != nil {
			return SimulationResult{}, ErrUnavailable
		}
		teamID, err := teamResult.LastInsertId()
		if err != nil || teamID < 1 {
			return SimulationResult{}, ErrUnavailable
		}
		identity, _ := json.Marshal(map[string]any{
			"ID": userID, "Person": personID, "Username": username, "Name": name,
			"Student": student, "StudentStatus": "verified", "Organization": "Biue Test Center", "Kind": "permanent",
		})
		if _, err := tx.ExecContext(ctx, `INSERT INTO contest_team_members(contest_id,team_id,user_id,person_id,identity_snapshot) VALUES (?,?,?,?,?)`,
			contestID, teamID, userID, personID, string(identity)); err != nil {
			return SimulationResult{}, ErrUnavailable
		}
		account := simulatedAccount{UserID: uint64(userID), TeamID: uint64(teamID)}
		result.Usernames = append(result.Usernames, username)
		total := 24 + index%17
		practice := total / 2
		contestCount := total - practice
		for attempt := 0; attempt < total; attempt++ {
			problemIndex := (index + attempt) % len(createdProblems)
			creation := now.Add(-time.Duration((index+1)*100+attempt) * time.Minute)
			var contestValue any
			var teamValue any
			isContest := attempt >= practice && attempt < practice+contestCount
			if isContest {
				contestValue = contestID
				teamValue = teamID
				creation = contestStart.Add(time.Duration(index*40+attempt-practice) * time.Second)
			}
			status := "Accepted"
			score := int64(100)
			passed := len(definitions[problemIndex].dataset.Cases)
			if (index*7+attempt*11)%5 == 0 {
				status, score, passed = "WrongAnswer", 0, 0
			}
			code := fmt.Sprintf("// BIUE SIMULATION: %s\n%s", result.SimulationLabel, definitions[problemIndex].correct)
			if status != "Accepted" {
				code = fmt.Sprintf("// BIUE SIMULATION: %s\nint main() { return 0; }\n", result.SimulationLabel)
			}
			_, err = insertSimulatedSubmission(ctx, tx, simulatedSubmission{
				UserID: account.UserID, ProblemID: createdProblems[problemIndex].ID,
				StatementVersion: createdProblems[problemIndex].StatementVersion, DataVersion: createdProblems[problemIndex].DataVersion,
				ContestID: contestValue, TeamID: teamValue, Source: code, Status: status, Score: int64(score),
				Passed: passed, Total: len(definitions[problemIndex].dataset.Cases), Created: creation,
			})
			if err != nil {
				return SimulationResult{}, ErrUnavailable
			}
			if isContest {
				result.ContestSubmissions++
			} else {
				result.PracticeSubmits++
			}
		}
	}
	details, _ := json.Marshal(map[string]any{
		"contest_id": contestID, "problems": result.ProblemIDs, "users": simulatedUserCount,
		"practice_submissions": result.PracticeSubmits, "contest_submissions": result.ContestSubmissions,
		"label": result.SimulationLabel,
	})
	if _, err := tx.ExecContext(ctx, `INSERT INTO audit_logs(actor_user_id,action,object_type,object_id,request_id,details,created_at) VALUES (?,'operations.simulate','contest',?,?,?,?)`,
		actor.ID, contestID, "simulate-"+suffix, string(details), now); err != nil {
		return SimulationResult{}, ErrUnavailable
	}
	if err := tx.Commit(); err != nil {
		return SimulationResult{}, ErrUnavailable
	}
	if _, err := s.competition.Settle(ctx, actor, uint64(contestID)); err != nil {
		result.Complete = false
		result.Warnings = append(result.Warnings, "账号、题目和提交已写入，但比赛结算未完成；可在比赛工作区核对后重试结算")
	}
	return result, nil
}

type simulatedSubmission struct {
	UserID           uint64
	ProblemID        uint64
	StatementVersion uint64
	DataVersion      uint64
	ContestID        any
	TeamID           any
	Source           string
	Status           string
	Score            int64
	Passed           int
	Total            int
	Created          time.Time
}

func insertSimulatedSubmission(ctx context.Context, tx *sqlx.Tx, item simulatedSubmission) (uint64, error) {
	result, err := tx.ExecContext(ctx, `INSERT INTO submissions(user_id,problem_id,statement_version,data_version,contest_id,contest_team_id,kind,language,source_code,stdin,generation,status,score,execution_time_ms,memory_usage_kb,passed_cases,total_cases,output,error_message,created_at,updated_at) VALUES (?,?,?,?,?,?,?,?,?,NULL,1,?,?,?,?,?,?,?,?,?,?)`,
		item.UserID, item.ProblemID, item.StatementVersion, item.DataVersion, item.ContestID, item.TeamID,
		"Submit", "cpp17", item.Source, item.Status, item.Score, 12, 1024, item.Passed, item.Total, "", "", item.Created, item.Created)
	if err != nil {
		return 0, err
	}
	submissionID, err := result.LastInsertId()
	if err != nil || submissionID < 1 {
		return 0, fmt.Errorf("simulation submission id")
	}
	taskResult, err := tx.ExecContext(ctx, `INSERT INTO judge_tasks(submission_id,generation,attempt,status,error_message,created_at,updated_at) VALUES (?,1,1,?,'',?,?)`,
		submissionID, item.Status, item.Created, item.Created)
	if err != nil {
		return 0, err
	}
	if _, err := taskResult.LastInsertId(); err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO submission_results(submission_id,generation,attempt,status,score,execution_time_ms,memory_usage_kb,passed_cases,total_cases,output,error_message,created_at) VALUES (?,1,1,?,?,?,?,?,?,?,?,?)`,
		submissionID, item.Status, item.Score, 12, 1024, item.Passed, item.Total, "", "", item.Created); err != nil {
		return 0, err
	}
	for index := 1; index <= item.Total; index++ {
		caseStatus := item.Status
		caseScore := item.Score / int64(item.Total)
		if index == item.Total {
			caseScore += item.Score % int64(item.Total)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO submission_case_results(submission_id,generation,case_id,status,score,execution_time_ms,memory_usage_kb) VALUES (?,1,?,?,?,?,?)`,
			submissionID, fmt.Sprintf("%d", index), caseStatus, caseScore, 6, 512); err != nil {
			return 0, err
		}
	}
	return uint64(submissionID), nil
}

func simulationProblems(suffix string) []simulationProblem {
	return []simulationProblem{
		{
			correct: "#include <iostream>\nint main(){long long a,b;std::cin>>a>>b;std::cout<<a+b<<'\\n';}\n",
			input:   simulationInput(suffix, 1, "模拟 A+B", "输入两个整数。", "输出两数之和。", "2 3\n", "5\n"),
			dataset: simulationDataset([][2]string{{"1", "2 3\n|5\n"}, {"2", "-4 9\n|5\n"}}),
		},
		{
			correct: "#include <iostream>\nint main(){int n,x;long long s=0;std::cin>>n;while(n--){std::cin>>x;s+=x;}std::cout<<s<<'\\n';}\n",
			input:   simulationInput(suffix, 2, "模拟序列求和", "先输入长度 n，再输入 n 个整数。", "输出全部整数之和。", "3\n4 5 6\n", "15\n"),
			dataset: simulationDataset([][2]string{{"1", "3\n4 5 6\n|15\n"}, {"2", "4\n-1 2 3 10\n|14\n"}}),
		},
		{
			correct: "#include <algorithm>\n#include <iostream>\nint main(){int a,b,c;std::cin>>a>>b>>c;std::cout<<std::max({a,b,c})<<'\\n';}\n",
			input:   simulationInput(suffix, 3, "模拟三数最大值", "输入三个整数。", "输出其中的最大值。", "8 2 5\n", "8\n"),
			dataset: simulationDataset([][2]string{{"1", "8 2 5\n|8\n"}, {"2", "-3 -9 -1\n|-1\n"}}),
		},
	}
}

func simulationInput(suffix string, index int, title, input, output, sampleInput, sampleOutput string) problem.Input {
	return problem.Input{
		ProblemType: "programming", CheckerMode: "tokens", Title: fmt.Sprintf("%s [%s]", title, strings.ToUpper(suffix)),
		Description: fmt.Sprintf("## 题目描述\n\n这是测试中心生成的模拟题 %d。数据仅用于验证题库、比赛和排行榜界面。\n\n## 输入\n\n%s\n\n## 输出\n\n%s\n", index, input, output),
		InputFormat: input, OutputFormat: output, SampleInput: sampleInput, SampleOutput: sampleOutput,
		TimeLimitMS: 1000, MemoryLimitMB: 256, Difficulty: []string{"easy", "medium", "hard"}[(index-1)%3],
		Tags: []string{"simulation", "test-center"}, Status: "published", Visibility: "public",
	}
}

func simulationDataset(cases [][2]string) testcase.Dataset {
	result := testcase.Dataset{Cases: make([]testcase.Case, 0, len(cases))}
	for _, item := range cases {
		parts := strings.SplitN(item[1], "|", 2)
		input, output := []byte(parts[0]), []byte(parts[1])
		result.Cases = append(result.Cases, testcase.Case{ID: item[0], Input: input, Output: output, Score: 100 / int64(len(cases))})
		result.TotalBytes += int64(len(input) + len(output))
		result.TotalScore += result.Cases[len(result.Cases)-1].Score
	}
	if result.TotalScore < 100 {
		result.Cases[len(result.Cases)-1].Score += 100 - result.TotalScore
		result.TotalScore = 100
	}
	result.Digest = testcase.Fingerprint(result.Cases)
	return result
}

func randomHex(bytes int) (string, error) {
	buffer := make([]byte, bytes)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return hex.EncodeToString(buffer), nil
}
