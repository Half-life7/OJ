package operations

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"biue/internal/auth"
	"biue/internal/judge"
	"biue/internal/problem"
	"biue/internal/submission"
	"biue/internal/testcase"

	"github.com/jmoiron/sqlx"
	"golang.org/x/crypto/bcrypt"
)

const (
	simulatedGroupSize = 10
	simulationWait     = 5 * time.Minute
)

var (
	simulationModes      = []string{"OI", "ACM", "IOI"}
	requiredSimulation   = []string{"Accepted", "WrongAnswer", "TimeLimitExceeded", "MemoryLimitExceeded", "RuntimeError", "CompileError"}
	simulationHTTPClient = &http.Client{Timeout: 2 * time.Second, Transport: &http.Transport{Proxy: nil}}
)

type SimulationResult struct {
	ID                  string            `json:"id"`
	Status              string            `json:"status"`
	StartedAt           time.Time         `json:"started_at"`
	DeadlineAt          time.Time         `json:"deadline_at"`
	FinishedAt          *time.Time        `json:"finished_at,omitempty"`
	Complete            bool              `json:"complete"`
	SimulationLabel     string            `json:"simulation_label"`
	Users               int               `json:"users"`
	Problems            int               `json:"problems"`
	Contests            map[string]uint64 `json:"contests"`
	Usernames           []string          `json:"usernames"`
	InitialPassword     string            `json:"initial_password,omitempty"`
	PracticeSubmissions int               `json:"practice_submissions"`
	ContestSubmissions  int               `json:"contest_submissions"`
	TotalSubmissions    int               `json:"total_submissions"`
	Terminal            int               `json:"terminal"`
	Pending             int               `json:"pending"`
	Progress            map[string]int    `json:"progress"`
	RequiredOutcomes    []string          `json:"required_outcomes"`
	MissingOutcomes     []string          `json:"missing_outcomes"`
	Warnings            []string          `json:"warnings"`
}

type simulationJob struct {
	result        SimulationResult
	submissionIDs []uint64
}

type simulationDefinition struct {
	problem  problem.Input
	dataset  testcase.Dataset
	source   string
	language string
	outcome  string
}

// StartSimulation provisions a bounded real judging run. Synthetic results are
// never inserted: submissions enter the ordinary outbox and are left Pending
// until Worker/Runner produce authoritative terminal rows.
func (s *Service) StartSimulation(ctx context.Context, actor auth.User) (SimulationResult, error) {
	if err := s.authorized(ctx, actor); err != nil {
		return SimulationResult{}, err
	}
	if s.db == nil || s.problems == nil || s.competition == nil || s.submissions == nil {
		return SimulationResult{}, ErrUnavailable
	}
	if !simulationRuntimeReady(ctx) {
		return SimulationResult{}, ErrRunnerUnavailable
	}
	s.simulationMu.Lock()
	if s.simulations == nil {
		s.simulations = make(map[string]*simulationJob)
	}
	for _, job := range s.simulations {
		if job.result.Status == "provisioning" || job.result.Status == "running" {
			s.simulationMu.Unlock()
			return SimulationResult{}, ErrConflict
		}
	}
	suffix, err := randomHex(6)
	if err != nil {
		s.simulationMu.Unlock()
		return SimulationResult{}, ErrUnavailable
	}
	jobID, err := randomHex(12)
	if err != nil {
		s.simulationMu.Unlock()
		return SimulationResult{}, ErrUnavailable
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	stub := &simulationJob{result: SimulationResult{
		ID: jobID, Status: "provisioning", StartedAt: now, DeadlineAt: now.Add(simulationWait),
		SimulationLabel: "真实 Worker/Runner 执行的小规模测试",
		Contests:        map[string]uint64{}, Usernames: []string{}, Progress: map[string]int{},
		RequiredOutcomes: append([]string(nil), requiredSimulation...), Warnings: []string{},
	}}
	s.simulations[jobID] = stub
	s.simulationMu.Unlock()

	provisioned, submissionIDs, err := s.provisionSimulation(ctx, actor, jobID, suffix, now)
	if err != nil {
		s.failSimulation(jobID, "创建真实评测任务失败："+simulationErrorText(err))
		return SimulationResult{}, err
	}
	s.simulationMu.Lock()
	job := s.simulations[jobID]
	response := copySimulationResult(provisioned)
	provisioned.Status = "running"
	provisioned.InitialPassword = ""
	job.result = provisioned
	job.submissionIDs = submissionIDs
	s.simulationMu.Unlock()
	go s.monitorSimulation(jobID, actor)
	return response, nil
}

func (s *Service) SimulationStatus(ctx context.Context, actor auth.User, id string) (SimulationResult, error) {
	if err := s.authorized(ctx, actor); err != nil {
		return SimulationResult{}, err
	}
	s.simulationMu.Lock()
	defer s.simulationMu.Unlock()
	job := s.simulations[id]
	if job == nil {
		return SimulationResult{}, ErrNotFound
	}
	return copySimulationResult(job.result), nil
}

func (s *Service) provisionSimulation(ctx context.Context, actor auth.User, jobID, suffix string, now time.Time) (SimulationResult, []uint64, error) {
	passwordBytes := make([]byte, 18)
	if _, err := rand.Read(passwordBytes); err != nil {
		return SimulationResult{}, nil, ErrUnavailable
	}
	password := "sim-" + base64.RawURLEncoding.EncodeToString(passwordBytes)
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return SimulationResult{}, nil, ErrUnavailable
	}
	definitions := simulationDefinitions(suffix)
	createdProblems := make([]problem.Problem, 0, len(definitions))
	for index, definition := range definitions {
		created, err := s.problems.ImportPackage(ctx, problem.Actor{ID: actor.ID, Role: actor.GlobalRole}, problem.ProblemPackage{
			Input: definition.problem, Dataset: definition.dataset,
		}, fmt.Sprintf("simulate-%s-problem-%d", suffix, index+1))
		if err != nil {
			return SimulationResult{}, nil, ErrUnavailable
		}
		createdProblems = append(createdProblems, created)
	}

	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return SimulationResult{}, nil, ErrUnavailable
	}
	defer tx.Rollback()
	contests := map[string]uint64{}
	for _, mode := range simulationModes {
		result, err := tx.ExecContext(ctx, `INSERT INTO contests(title,scoring_mode,enrollment_policy,approval_required,team_mode,starts_at,ends_at,revision,created_by,created_at) VALUES (?,?,?,?,?,?,?,1,?,?)`,
			fmt.Sprintf("真实小规模测试 %s [%s]", mode, suffix), mode, "admin_add", false, false,
			now.Add(-time.Minute), now.Add(4*time.Minute), actor.ID, now)
		if err != nil {
			return SimulationResult{}, nil, ErrUnavailable
		}
		id, err := result.LastInsertId()
		if err != nil || id < 1 {
			return SimulationResult{}, nil, ErrUnavailable
		}
		contests[mode] = uint64(id)
		for _, item := range createdProblems {
			if _, err := tx.ExecContext(ctx, `INSERT INTO contest_problems(contest_id,problem_id,statement_version,data_version) VALUES (?,?,?,?)`,
				id, item.ID, item.StatementVersion, item.DataVersion); err != nil {
				return SimulationResult{}, nil, ErrUnavailable
			}
		}
	}

	type simulatedAccount struct {
		UserID   uint64
		Username string
		Mode     string
		Index    int
	}
	accounts := make([]simulatedAccount, 0, simulatedGroupSize*len(simulationModes))
	usernames := make([]string, 0, cap(accounts))
	globalIndex := 0
	for _, mode := range simulationModes {
		for index := 0; index < simulatedGroupSize; index++ {
			globalIndex++
			student := fmt.Sprintf("SIM%s%02d", strings.ToUpper(suffix), globalIndex)
			name := fmt.Sprintf("真实测试选手 %s-%02d", mode, index+1)
			personResult, err := tx.ExecContext(ctx, `INSERT INTO persons(display_name,student_no,student_no_status,organization,revision,created_at,updated_at) VALUES (?,?,?,?,1,?,?)`,
				name, student, "verified", "Biue Real Test", now, now)
			if err != nil {
				return SimulationResult{}, nil, ErrUnavailable
			}
			personID, err := personResult.LastInsertId()
			if err != nil || personID < 1 {
				return SimulationResult{}, nil, ErrUnavailable
			}
			username := fmt.Sprintf("sim%s%02d", suffix, globalIndex)
			userResult, err := tx.ExecContext(ctx, `INSERT INTO users(username,password_hash,account_type,global_role,status,token_version,created_at,updated_at) VALUES (?,?,?,?,?,1,?,?)`,
				username, string(hash), "permanent", "user", "active", now, now)
			if err != nil {
				return SimulationResult{}, nil, ErrUnavailable
			}
			userID, err := userResult.LastInsertId()
			if err != nil || userID < 1 {
				return SimulationResult{}, nil, ErrUnavailable
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO person_accounts(user_id,person_id,created_at) VALUES (?,?,?)`, userID, personID, now); err != nil {
				return SimulationResult{}, nil, ErrUnavailable
			}
			teamResult, err := tx.ExecContext(ctx, `INSERT INTO contest_teams(contest_id,name,participant_type,status,created_at) VALUES (?,?,?,?,?)`,
				contests[mode], fmt.Sprintf("%s 真实测试队 %02d", mode, index+1), "official", "active", now)
			if err != nil {
				return SimulationResult{}, nil, ErrUnavailable
			}
			teamID, err := teamResult.LastInsertId()
			if err != nil || teamID < 1 {
				return SimulationResult{}, nil, ErrUnavailable
			}
			identity, _ := json.Marshal(map[string]any{
				"ID": userID, "Person": personID, "Username": username, "Name": name,
				"Student": student, "StudentStatus": "verified", "Organization": "Biue Real Test", "Kind": "permanent",
			})
			if _, err := tx.ExecContext(ctx, `INSERT INTO contest_team_members(contest_id,team_id,user_id,person_id,identity_snapshot) VALUES (?,?,?,?,?)`,
				contests[mode], teamID, userID, personID, string(identity)); err != nil {
				return SimulationResult{}, nil, ErrUnavailable
			}
			accounts = append(accounts, simulatedAccount{UserID: uint64(userID), Username: username, Mode: mode, Index: index})
			usernames = append(usernames, username)
		}
	}
	auditDetails, _ := json.Marshal(map[string]any{
		"job_id": jobID, "modes": simulationModes, "group_size": simulatedGroupSize,
		"problems": createdProblems, "real_runner": true, "deadline_seconds": int(simulationWait / time.Second),
	})
	if _, err := tx.ExecContext(ctx, `INSERT INTO audit_logs(actor_user_id,action,object_type,object_id,request_id,details,created_at) VALUES (?,'operations.simulate.start','system',0,?,?,?)`,
		actor.ID, "simulate-"+suffix, string(auditDetails), now); err != nil {
		return SimulationResult{}, nil, ErrUnavailable
	}
	if err := tx.Commit(); err != nil {
		return SimulationResult{}, nil, ErrUnavailable
	}

	result := SimulationResult{
		ID: jobID, Status: "running", StartedAt: now, DeadlineAt: now.Add(simulationWait),
		SimulationLabel: "真实 Worker/Runner 执行的小规模测试", Users: len(accounts), Problems: len(createdProblems),
		Contests: contests, Usernames: usernames, InitialPassword: password,
		RequiredOutcomes: append([]string(nil), requiredSimulation...), Progress: map[string]int{}, Warnings: []string{},
	}
	submissionIDs := []uint64{}
	for _, account := range accounts {
		practiceIndex := account.Index % len(definitions)
		contestA := (account.Index + len(simulationModes)*2) % len(definitions)
		contestB := (account.Index + simulatedGroupSize) % len(definitions)
		practice, err := s.submissions.Create(ctx, submission.Actor{ID: account.UserID, AccountType: "permanent", Role: "user"}, "Submit", submission.CreateInput{
			ProblemID: createdProblems[practiceIndex].ID, Language: definitions[practiceIndex].language, SourceCode: definitions[practiceIndex].source,
		}, fmt.Sprintf("sim-%s-%d-practice", jobID, account.UserID))
		if err != nil {
			return SimulationResult{}, nil, ErrUnavailable
		}
		submissionIDs = append(submissionIDs, practice.ID)
		result.PracticeSubmissions++
		for attempt, problemIndex := range []int{contestA, contestB} {
			item, err := s.submissions.Create(ctx, submission.Actor{ID: account.UserID, AccountType: "permanent", Role: "user"}, "Submit", submission.CreateInput{
				ContestID: contests[account.Mode], ProblemID: createdProblems[problemIndex].ID,
				Language: definitions[problemIndex].language, SourceCode: definitions[problemIndex].source,
			}, fmt.Sprintf("sim-%s-%d-contest-%d", jobID, account.UserID, attempt+1))
			if err != nil {
				return SimulationResult{}, nil, ErrUnavailable
			}
			submissionIDs = append(submissionIDs, item.ID)
			result.ContestSubmissions++
		}
	}
	result.TotalSubmissions = len(submissionIDs)
	result.Pending = len(submissionIDs)
	return result, submissionIDs, nil
}

func (s *Service) monitorSimulation(jobID string, actor auth.User) {
	deadline := time.Now().Add(simulationWait)
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		progress, terminal, pending, err := s.simulationProgress(ctx, jobID)
		if err != nil {
			s.failSimulation(jobID, "读取真实评测进度失败")
			return
		}
		s.updateSimulation(jobID, func(result *SimulationResult) {
			result.Progress = progress
			result.Terminal = terminal
			result.Pending = pending
		})
		if pending == 0 {
			missing := missingOutcomes(progress)
			s.updateSimulation(jobID, func(result *SimulationResult) { result.MissingOutcomes = missing })
			if len(missing) != 0 {
				s.failSimulation(jobID, "真实评测终态缺少指定结果："+strings.Join(missing, "、"))
				return
			}
			ids := make([]uint64, 0, len(simulationModes))
			s.simulationMu.Lock()
			if job := s.simulations[jobID]; job != nil {
				ids = sortedContestIDs(job.result.Contests)
			}
			s.simulationMu.Unlock()
			for _, contestID := range ids {
				_, _ = s.db.ExecContext(ctx, `UPDATE contests SET ends_at=UTC_TIMESTAMP(6)-INTERVAL 1 SECOND WHERE id=? AND settled_at IS NULL`, contestID)
				if _, err := s.competition.Settle(ctx, actor, contestID); err != nil {
					s.failSimulation(jobID, "真实评测完成，但比赛结算失败")
					return
				}
			}
			now := time.Now().UTC().Truncate(time.Microsecond)
			s.updateSimulation(jobID, func(result *SimulationResult) {
				result.Status, result.Complete, result.FinishedAt = "completed", true, &now
				result.Warnings = append(result.Warnings, "全部比赛均由真实 Worker/Runner 终态结果结算")
			})
			return
		}
		select {
		case <-ctx.Done():
			s.updateSimulation(jobID, func(result *SimulationResult) {
				result.Status, result.Complete = "failed", false
				result.Warnings = append(result.Warnings, fmt.Sprintf("超过 5 分钟仍有 %d 条真实评测任务未完成", pending))
			})
			return
		case <-ticker.C:
		}
	}
}

func (s *Service) simulationProgress(ctx context.Context, jobID string) (map[string]int, int, int, error) {
	s.simulationMu.Lock()
	job := s.simulations[jobID]
	if job == nil {
		s.simulationMu.Unlock()
		return nil, 0, 0, ErrNotFound
	}
	ids := append([]uint64(nil), job.submissionIDs...)
	s.simulationMu.Unlock()
	if len(ids) == 0 {
		return map[string]int{}, 0, 0, nil
	}
	query, args, err := sqlx.In(`SELECT status,COUNT(*) AS count FROM submissions WHERE id IN (?) GROUP BY status`, ids)
	if err != nil {
		return nil, 0, 0, ErrUnavailable
	}
	var rows []struct {
		Status string `db:"status"`
		Count  int    `db:"count"`
	}
	if err := s.db.SelectContext(ctx, &rows, s.db.Rebind(query), args...); err != nil {
		return nil, 0, 0, ErrUnavailable
	}
	progress := map[string]int{}
	terminal := 0
	for _, row := range rows {
		progress[row.Status] = row.Count
		if judge.Terminal(judge.Status(row.Status)) {
			terminal += row.Count
		}
	}
	return progress, terminal, len(ids) - terminal, nil
}

func (s *Service) updateSimulation(id string, fn func(*SimulationResult)) {
	s.simulationMu.Lock()
	defer s.simulationMu.Unlock()
	if job := s.simulations[id]; job != nil {
		fn(&job.result)
	}
}

func (s *Service) failSimulation(id, warning string) {
	now := time.Now().UTC().Truncate(time.Microsecond)
	s.updateSimulation(id, func(result *SimulationResult) {
		result.Status, result.Complete, result.FinishedAt = "failed", false, &now
		result.Warnings = append(result.Warnings, warning)
	})
}

func missingOutcomes(progress map[string]int) []string {
	missing := []string{}
	for _, outcome := range requiredSimulation {
		if progress[outcome] == 0 {
			missing = append(missing, outcome)
		}
	}
	return missing
}

func copySimulationResult(source SimulationResult) SimulationResult {
	result := source
	result.Contests = map[string]uint64{}
	for key, value := range source.Contests {
		result.Contests[key] = value
	}
	result.Progress = map[string]int{}
	for key, value := range source.Progress {
		result.Progress[key] = value
	}
	result.Usernames = append([]string(nil), source.Usernames...)
	result.RequiredOutcomes = append([]string(nil), source.RequiredOutcomes...)
	result.MissingOutcomes = append([]string(nil), source.MissingOutcomes...)
	result.Warnings = append([]string(nil), source.Warnings...)
	return result
}

func simulationRuntimeReady(ctx context.Context) bool {
	for _, target := range []string{"http://worker:8081/ready", "http://dispatcher:8082/ready"} {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
		if err != nil {
			return false
		}
		response, err := simulationHTTPClient.Do(request)
		if err != nil {
			return false
		}
		response.Body.Close()
		if response.StatusCode != http.StatusOK {
			return false
		}
	}
	return true
}

func simulationErrorText(err error) string {
	switch {
	case err == ErrRunnerUnavailable:
		return "Worker、dispatcher 或 Runner 尚未就绪"
	case err == ErrUnavailable:
		return "评测存储或题包服务暂时不可用"
	default:
		return "服务返回错误"
	}
}

func simulationDefinitions(suffix string) []simulationDefinition {
	tag := strings.ToUpper(suffix)
	return []simulationDefinition{
		simulationDefinitionFor(tag, "接受", "标准 A+B", "2 3\n", "5\n", 1000, 256,
			"#include <iostream>\nint main(){long long a,b;std::cin>>a>>b;std::cout<<a+b<<'\\n';}\n", "Accepted"),
		simulationDefinitionFor(tag, "错误答案", "固定错误输出", "2 3\n", "5\n", 1000, 256,
			"#include <iostream>\nint main(){std::cout<<0<<'\\n';}\n", "WrongAnswer"),
		simulationDefinitionFor(tag, "超时", "死循环", "2 3\n", "5\n", 200, 256,
			"#include <cstdint>\nint main(){volatile std::uint64_t x=0;for(;;){++x;}}\n", "TimeLimitExceeded"),
		simulationDefinitionFor(tag, "内存", "大内存分配", "2 3\n", "5\n", 1000, 32,
			"#include <iostream>\n#include <vector>\nint main(){std::vector<char> v(128*1024*1024,1);std::cout<<(int)v[0]<<'\\n';}\n", "MemoryLimitExceeded"),
		simulationDefinitionFor(tag, "运行错误", "空指针崩溃", "2 3\n", "5\n", 1000, 256,
			"int main(){volatile int *p=nullptr;*p=1;return 0;}\n", "RuntimeError"),
		simulationDefinitionFor(tag, "编译错误", "故意语法错误", "2 3\n", "5\n", 1000, 256,
			"int main( { this is not valid c++ }\n", "CompileError"),
	}
}

func simulationDefinitionFor(tag, label, title, input, output string, timeMS, memoryMB int64, source, outcome string) simulationDefinition {
	cases := []testcase.Case{{ID: "1", Input: []byte(input), Output: []byte(output), Score: 100}}
	dataset := testcase.Dataset{Cases: cases, TotalBytes: int64(len(input) + len(output)), TotalScore: 100}
	dataset.Digest = testcase.Fingerprint(cases)
	return simulationDefinition{
		problem: problem.Input{
			ProblemType: "programming", CheckerMode: "tokens", Title: fmt.Sprintf("真实测试 %s [%s]", title, tag),
			Description: fmt.Sprintf("## 题目描述\n\n该题用于验证真实评测结果 `%s`。\n\n## 输入\n\n两个整数。\n\n## 输出\n\n两数之和。", outcome),
			InputFormat: "两个整数。", OutputFormat: "输出两数之和。", SampleInput: input, SampleOutput: output,
			TimeLimitMS: timeMS, MemoryLimitMB: memoryMB, Difficulty: "easy",
			Tags: []string{"simulation", "real-runner", strings.ToLower(outcome)}, Status: "published", Visibility: "public",
		},
		dataset: dataset, source: source, language: "cpp17", outcome: outcome,
	}
}

func randomHex(length int) (string, error) {
	buffer := make([]byte, length)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return hex.EncodeToString(buffer), nil
}

// Keep map iteration deterministic where IDs are returned in diagnostics.
func sortedContestIDs(contests map[string]uint64) []uint64 {
	ids := make([]uint64, 0, len(contests))
	for _, id := range contests {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}
