package judge

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
)

func leaseFixture(t *testing.T) (*LeaseStore, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	s, err := NewLeaseStore(sqlx.NewDb(db, "sqlmock"), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Error(err)
		}
	})
	return s, mock
}

func TestLeaseClaim(t *testing.T) {
	envelope := TaskEnvelope{TaskID: 8, SubmissionID: 9, Generation: 1, Attempt: 1}
	for _, scenario := range []string{"new", "expired", "busy", "old generation", "old attempt", "terminal", "missing", "mismatch", "task update fails", "submission update fails", "commit fails"} {
		t.Run(scenario, func(t *testing.T) {
			s, mock := leaseFixture(t)
			mock.ExpectBegin()
			generation, status := 1, Pending
			if scenario == "expired" {
				status = Running
			}
			if scenario == "old generation" {
				generation = 2
			}
			if scenario == "terminal" {
				status = Accepted
			}
			q := mock.ExpectQuery("SELECT generation, status FROM submissions").WithArgs(9)
			if scenario == "missing" {
				q.WillReturnError(sql.ErrNoRows)
			} else {
				q.WillReturnRows(sqlmock.NewRows([]string{"generation", "status"}).AddRow(generation, status))
			}
			wantState := ClaimAcquired
			wantError := false
			switch scenario {
			case "old generation", "terminal", "missing":
				wantState = ClaimObsolete
				mock.ExpectRollback()
			default:
				attempt, taskStatus := 1, status
				if scenario == "old attempt" {
					attempt = 2
				}
				if scenario == "mismatch" {
					taskStatus = Running
				}
				mock.ExpectQuery("SELECT attempt, status, .* FROM judge_tasks").WithArgs(8, 9, 1).WillReturnRows(sqlmock.NewRows([]string{"attempt", "status", "active"}).AddRow(attempt, taskStatus, scenario == "busy"))
				switch scenario {
				case "old attempt":
					wantState = ClaimObsolete
					mock.ExpectRollback()
				case "busy":
					wantState = ClaimBusy
					mock.ExpectRollback()
				case "mismatch":
					wantState, wantError = "", true
					mock.ExpectRollback()
				default:
					update := mock.ExpectExec("UPDATE judge_tasks SET lease_token").WithArgs(sqlmock.AnyArg(), int64(60000000), 8)
					if scenario == "task update fails" {
						update.WillReturnError(errors.New("failed"))
						wantState, wantError = "", true
						mock.ExpectRollback()
					} else {
						update.WillReturnResult(sqlmock.NewResult(0, 1))
						if status == Pending {
							u := mock.ExpectExec("UPDATE submissions SET status = 'Queued'").WithArgs(9, 1)
							if scenario == "submission update fails" {
								u.WillReturnError(errors.New("failed"))
							} else {
								u.WillReturnResult(sqlmock.NewResult(0, 1))
							}
						}
						if scenario == "submission update fails" {
							wantState, wantError = "", true
							mock.ExpectRollback()
						} else {
							c := mock.ExpectCommit()
							if scenario == "commit fails" {
								c.WillReturnError(errors.New("unknown"))
								wantState, wantError = "", true
							}
						}
					}
				}
			}
			lease, state, err := s.Claim(context.Background(), envelope)
			if state != wantState || (err != nil) != wantError {
				t.Fatalf("state=%s error=%v", state, err)
			}
			if state == ClaimAcquired {
				if lease.Task != envelope || !regexp.MustCompile(`^[0-9a-f]{32}$`).MatchString(lease.Token) {
					t.Fatalf("invalid lease identity")
				}
			} else if lease != (Lease{}) {
				t.Fatal("uncommitted lease returned")
			}
		})
	}
}

func TestLeaseRenewFencing(t *testing.T) {
	for _, scenario := range []string{"owned", "lost", "database error", "invalid token", "invalid envelope"} {
		t.Run(scenario, func(t *testing.T) {
			s, mock := leaseFixture(t)
			lease := Lease{Task: TaskEnvelope{TaskID: 8, SubmissionID: 9, Generation: 1, Attempt: 1}, Token: "0123456789abcdef0123456789abcdef"}
			want := error(nil)
			if scenario == "invalid token" {
				lease.Token = "invalid"
				want = ErrLeaseLost
			} else if scenario == "invalid envelope" {
				lease.Task.Attempt = 0
				want = ErrLeaseLost
			} else {
				// Match the complete fencing predicate, not merely the UPDATE prefix.
				query := `UPDATE judge_tasks SET lease_expires_at = TIMESTAMPADD(MICROSECOND, ?, UTC_TIMESTAMP(6)), updated_at = UTC_TIMESTAMP(6) WHERE id = ? AND submission_id = ? AND generation = ? AND attempt = ? AND lease_token = ? AND lease_expires_at > UTC_TIMESTAMP(6) AND status IN ('Queued','Compiling','Running')`
				u := mock.ExpectExec(regexp.QuoteMeta(query)).WithArgs(int64(60000000), 8, 9, 1, 1, lease.Token)
				switch scenario {
				case "owned":
					u.WillReturnResult(sqlmock.NewResult(0, 1))
				case "lost":
					u.WillReturnResult(sqlmock.NewResult(0, 0))
					want = ErrLeaseLost
				case "database error":
					u.WillReturnError(errors.New("private database detail"))
					want = ErrLeaseStore
				}
			}
			if err := s.Renew(context.Background(), lease); err != want {
				t.Fatalf("error=%v want=%v", err, want)
			}
		})
	}
}
