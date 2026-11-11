package judge

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/DATA-DOG/go-sqlmock"
	"testing"
	"time"
)

func beginRejudge(m sqlmock.Sqlmock) {
	m.ExpectBegin()
	m.ExpectQuery("SELECT id FROM judge_admission").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1))
	m.ExpectQuery("SELECT global_role,account_type,status FROM users").WithArgs(uint64(5)).WillReturnRows(sqlmock.NewRows([]string{"global_role", "account_type", "status"}).AddRow("super_admin", "permanent", "active"))
}
func inputRejudge() RejudgeInput {
	return RejudgeInput{Key: "batch_test_key", Reason: "regression", Items: []RejudgeItem{{ID: 9, Generation: 1}}}
}
func TestRejudgeAtomicSuccessAndAuditRollback(t *testing.T) {
	for _, auditFailure := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "rollback"}[auditFailure], func(t *testing.T) {
			s, m := leaseFixture(t)
			a, _ := NewJudgeAdmin(s.db, 100)
			in := inputRejudge()
			beginRejudge(m)
			m.ExpectQuery("SELECT request_digest,tasks FROM rejudge_requests").WillReturnError(sql.ErrNoRows)
			m.ExpectQuery("SELECT COUNT.*FROM judge_tasks").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
			m.ExpectQuery("SELECT problem_id,statement_version,data_version,generation,status,contest_id FROM submissions").WithArgs(uint64(9)).WillReturnRows(
				sqlmock.NewRows([]string{"problem_id", "statement_version", "data_version", "generation", "status", "contest_id"}).AddRow(7, 3, 4, 1, Accepted, nil))
			m.ExpectExec("INSERT INTO judge_tasks").WithArgs(uint64(9), uint64(2)).WillReturnResult(sqlmock.NewResult(10, 1))
			m.ExpectExec("UPDATE submissions").WithArgs(uint64(2), uint64(9), uint64(1)).WillReturnResult(sqlmock.NewResult(0, 1))
			m.ExpectExec("INSERT INTO judge_task_outbox").WithArgs(int64(10), uint64(9), uint64(2)).WillReturnResult(sqlmock.NewResult(1, 1))
			m.ExpectExec("INSERT INTO rejudge_requests").WillReturnResult(sqlmock.NewResult(1, 1))
			audit := m.ExpectExec("INSERT INTO audit_logs")
			if auditFailure {
				audit.WillReturnError(errors.New("audit failed"))
				m.ExpectRollback()
			} else {
				audit.WillReturnResult(sqlmock.NewResult(1, 1))
				m.ExpectExec("DELETE FROM global_board_cache").WillReturnResult(sqlmock.NewResult(0, 1))
				m.ExpectCommit()
			}
			tasks, e := a.Rejudge(context.Background(), 5, in)
			if auditFailure {
				if e != ErrExecutionStore || tasks != nil {
					t.Fatal(tasks, e)
				}
			} else if e != nil || len(tasks) != 1 || tasks[0].Generation != 2 {
				t.Fatal(tasks, e)
			}
			if e = m.ExpectationsWereMet(); e != nil {
				t.Fatal(e)
			}
		})
	}
}
func TestRejudgeReplayAndDigestConflict(t *testing.T) {
	for _, conflict := range []bool{false, true} {
		s, m := leaseFixture(t)
		a, _ := NewJudgeAdmin(s.db, 100)
		in := inputRejudge()
		raw, _ := json.Marshal(in)
		sum := sha256.Sum256(raw)
		digest := hex.EncodeToString(sum[:])
		if conflict {
			digest = "other"
		}
		beginRejudge(m)
		m.ExpectQuery("SELECT request_digest,tasks").WithArgs(uint64(5), in.Key).WillReturnRows(sqlmock.NewRows([]string{"request_digest", "tasks"}).AddRow(digest, `[{"task_id":10,"submission_id":9,"generation":2,"attempt":1}]`))
		m.ExpectRollback()
		tasks, e := a.Rejudge(context.Background(), 5, in)
		if conflict {
			if e != ErrAdminConflict {
				t.Fatal(e)
			}
		} else if e != nil || len(tasks) != 1 || tasks[0].TaskID != 10 {
			t.Fatal(tasks, e)
		}
		if e = m.ExpectationsWereMet(); e != nil {
			t.Fatal(e)
		}
	}
}

func TestScopedRejudgeCanMovePracticeSubmissionToLatestProblemVersions(t *testing.T) {
	s, m := leaseFixture(t)
	a, _ := NewJudgeAdmin(s.db, 100)
	in := inputRejudge()
	in.UseLatestProblem = true
	beginRejudge(m)
	m.ExpectQuery("SELECT request_digest,tasks FROM rejudge_requests").WillReturnError(sql.ErrNoRows)
	m.ExpectQuery("SELECT COUNT.*FROM judge_tasks").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	m.ExpectQuery("SELECT problem_id,statement_version,data_version,generation,status,contest_id FROM submissions").WithArgs(uint64(9)).WillReturnRows(
		sqlmock.NewRows([]string{"problem_id", "statement_version", "data_version", "generation", "status", "contest_id"}).AddRow(7, 1, 1, 1, Accepted, nil))
	m.ExpectQuery("SELECT statement_version,data_version FROM problems").WithArgs(uint64(7)).WillReturnRows(
		sqlmock.NewRows([]string{"statement_version", "data_version"}).AddRow(2, 2))
	m.ExpectExec("INSERT INTO judge_tasks").WithArgs(uint64(9), uint64(2)).WillReturnResult(sqlmock.NewResult(10, 1))
	m.ExpectExec("UPDATE submissions SET generation=\\?,statement_version=\\?,data_version=\\?").
		WithArgs(uint64(2), uint64(2), uint64(2), uint64(9), uint64(1)).WillReturnResult(sqlmock.NewResult(0, 1))
	m.ExpectExec("INSERT INTO judge_task_outbox").WithArgs(int64(10), uint64(9), uint64(2)).WillReturnResult(sqlmock.NewResult(1, 1))
	m.ExpectExec("INSERT INTO rejudge_requests").WillReturnResult(sqlmock.NewResult(1, 1))
	m.ExpectExec("INSERT INTO audit_logs").WillReturnResult(sqlmock.NewResult(1, 1))
	m.ExpectExec("DELETE FROM global_board_cache").WillReturnResult(sqlmock.NewResult(0, 1))
	m.ExpectCommit()
	if tasks, err := a.Rejudge(context.Background(), 5, in); err != nil || len(tasks) != 1 {
		t.Fatal(tasks, err)
	}
	if err := m.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestRejudgeSettledContestBlocked(t *testing.T) {
	s, m := leaseFixture(t)
	a, _ := NewJudgeAdmin(s.db, 100)
	beginRejudge(m)
	m.ExpectQuery("SELECT request_digest,tasks").WillReturnError(sql.ErrNoRows)
	m.ExpectQuery("SELECT COUNT.*FROM judge_tasks").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	m.ExpectQuery("SELECT problem_id,statement_version,data_version,generation,status,contest_id FROM submissions").WillReturnRows(
		sqlmock.NewRows([]string{"problem_id", "statement_version", "data_version", "generation", "status", "contest_id"}).AddRow(9, 3, 4, 1, Accepted, 7))
	m.ExpectQuery("SELECT settled_at FROM contests").WithArgs(uint64(7)).WillReturnRows(sqlmock.NewRows([]string{"settled_at"}).AddRow(time.Now()))
	m.ExpectRollback()
	if _, e := a.Rejudge(context.Background(), 5, inputRejudge()); e != ErrAdminConflict {
		t.Fatal(e)
	}
	if e := m.ExpectationsWereMet(); e != nil {
		t.Fatal(e)
	}
}
