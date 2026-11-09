package judge

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestManualRetryIsAuthorizedVersionedAndAtomic(t *testing.T) {
	for _, scenario := range []string{"success", "not administrator", "capacity", "already retried", "audit failed", "commit unknown"} {
		t.Run(scenario, func(t *testing.T) {
			s, mock := leaseFixture(t)
			admin, _ := NewJudgeAdmin(s.db, 1000)
			mock.ExpectBegin()
			mock.ExpectQuery("SELECT id FROM judge_admission").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1))
			role := "super_admin"
			if scenario == "not administrator" {
				role = "user"
			}
			mock.ExpectQuery("SELECT global_role, account_type, status FROM users").WithArgs(5).WillReturnRows(sqlmock.NewRows([]string{"global_role", "account_type", "status"}).AddRow(role, "permanent", "active"))
			want := error(nil)
			if scenario == "not administrator" {
				want = ErrAdminForbidden
				mock.ExpectRollback()
			} else {
				count := 0
				if scenario == "capacity" {
					count = 1000
				}
				mock.ExpectQuery("SELECT COUNT.* FROM judge_tasks").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(count))
				if scenario == "capacity" {
					want = ErrAdminCapacity
					mock.ExpectRollback()
				} else {
					mock.ExpectQuery("SELECT id, task_id, submission_id.*FROM judge_dead_letters WHERE id").WithArgs(7).WillReturnRows(sqlmock.NewRows([]string{"id", "task_id", "submission_id", "generation", "attempt", "reason", "created_at"}).AddRow(7, 8, 9, 1, 4, FailureRunnerSystem, time.Now()))
					generation := 1
					if scenario == "already retried" {
						generation = 2
					}
					mock.ExpectQuery("SELECT generation, status FROM submissions").WithArgs(9).WillReturnRows(sqlmock.NewRows([]string{"generation", "status"}).AddRow(generation, SystemError))
					if scenario == "already retried" {
						want = ErrAdminConflict
						mock.ExpectRollback()
					} else {
						mock.ExpectQuery("SELECT status FROM judge_tasks.*FOR UPDATE").WithArgs(8, 9, 1).WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow(SystemError))
						mock.ExpectExec("INSERT INTO judge_tasks").WithArgs(9, 2).WillReturnResult(sqlmock.NewResult(10, 1))
						mock.ExpectExec("UPDATE submissions SET generation = .*WHERE id = .*AND generation = .*AND status = 'SystemError'").WithArgs(2, 9, 1).WillReturnResult(sqlmock.NewResult(0, 1))
						mock.ExpectExec("INSERT INTO judge_task_outbox").WithArgs(int64(10), 9, 2).WillReturnResult(sqlmock.NewResult(0, 1))
						audit := mock.ExpectExec("INSERT INTO audit_logs").WithArgs(5, 9, `{"dead_letter_id":7,"previous_generation":1}`)
						if scenario == "audit failed" {
							audit.WillReturnError(errors.New("failed"))
							want = ErrExecutionStore
							mock.ExpectRollback()
						} else {
							audit.WillReturnResult(sqlmock.NewResult(0, 1))
							commit := mock.ExpectCommit()
							if scenario == "commit unknown" {
								commit.WillReturnError(errors.New("unknown"))
								want = ErrExecutionStore
							}
						}
					}
				}
			}
			task, err := admin.RetryDead(context.Background(), 5, 7, 1)
			if err != want {
				t.Fatalf("error=%v want=%v", err, want)
			}
			if want == nil {
				if task != (TaskEnvelope{TaskID: 10, SubmissionID: 9, Generation: 2, Attempt: 1}) {
					t.Fatal(task)
				}
			} else if task != (TaskEnvelope{}) {
				t.Fatal("uncommitted task exposed")
			}
		})
	}
}

func TestDeadListRejectsOrdinaryAccount(t *testing.T) {
	s, mock := leaseFixture(t)
	admin, _ := NewJudgeAdmin(s.db, 1000)
	mock.ExpectQuery("SELECT EXISTS").WithArgs(3).WillReturnRows(sqlmock.NewRows([]string{"allowed"}).AddRow(false))
	if _, err := admin.ListDead(context.Background(), 3, 20); err != ErrAdminForbidden {
		t.Fatal(err)
	}
}
