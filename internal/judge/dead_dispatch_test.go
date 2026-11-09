package judge

import (
	"context"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/alicebob/miniredis/v2"
	"github.com/jmoiron/sqlx"
	"github.com/redis/go-redis/v9"
)

func TestDeadLetterDispatchRecovery(t *testing.T) {
	for _, scenario := range []string{"success", "empty", "invalid reason", "invalid attempt", "Redis unavailable", "mark failed", "commit unknown"} {
		t.Run(scenario, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			r := miniredis.RunT(t)
			client := redis.NewClient(&redis.Options{Addr: r.Addr(), MaxRetries: -1})
			defer client.Close()
			d, _ := NewDispatcher(sqlx.NewDb(db, "sqlmock"), client)
			mock.ExpectBegin()
			rows := sqlmock.NewRows([]string{"id", "task_id", "submission_id", "generation", "attempt", "reason"})
			attempt, reason := MaxAttempts, FailureRunnerSystem
			if scenario == "invalid reason" {
				reason = "uncontrolled private detail"
			}
			if scenario == "invalid attempt" {
				attempt = 3
			}
			if scenario != "empty" {
				rows.AddRow(7, 8, 9, 1, attempt, reason)
			}
			mock.ExpectQuery("SELECT .* FROM judge_dead_letters .* FOR UPDATE SKIP LOCKED").WillReturnRows(rows)
			switch scenario {
			case "empty", "invalid reason", "invalid attempt":
				mock.ExpectRollback()
			default:
				mock.ExpectExec("UPDATE judge_dead_letters SET dispatch_attempts").WithArgs(7).WillReturnResult(sqlmock.NewResult(0, 1))
				if scenario == "Redis unavailable" {
					r.SetError("unavailable")
					mock.ExpectCommit()
				} else {
					u := mock.ExpectExec("UPDATE judge_dead_letters SET dispatched_at").WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), 7)
					if scenario == "mark failed" {
						u.WillReturnError(errors.New("failed"))
						mock.ExpectRollback()
					} else {
						u.WillReturnResult(sqlmock.NewResult(0, 1))
						c := mock.ExpectCommit()
						if scenario == "commit unknown" {
							c.WillReturnError(errors.New("unknown"))
						}
					}
				}
			}
			sent, err := d.DispatchDeadOne(context.Background())
			if sent != (scenario == "success") || (err != nil) != (scenario != "success" && scenario != "empty") {
				t.Fatalf("sent=%v error=%v", sent, err)
			}
			if scenario == "success" || scenario == "mark failed" || scenario == "commit unknown" {
				messages, err := client.XRange(context.Background(), DeadStream, "-", "+").Result()
				if err != nil || len(messages) != 1 {
					t.Fatalf("messages=%v error=%v", messages, err)
				}
				values := messages[0].Values
				if len(values) != 6 || values["reason"] != string(FailureRunnerSystem) || values["dead_letter_id"] != "7" {
					t.Fatalf("unexpected dead-letter payload fields")
				}
				delete(values, "reason")
				delete(values, "dead_letter_id")
				if envelope, err := ParseEnvelope(values); err != nil || envelope != (TaskEnvelope{TaskID: 8, SubmissionID: 9, Generation: 1, Attempt: 4}) {
					t.Fatalf("envelope=%+v error=%v", envelope, err)
				}
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestNormalDispatchRequiresDueOutbox(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	r := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: r.Addr()})
	defer client.Close()
	d, _ := NewDispatcher(sqlx.NewDb(db, "sqlmock"), client)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .* FROM judge_task_outbox WHERE dispatched_at IS NULL AND available_at <= UTC_TIMESTAMP\\(6\\)").WillReturnRows(sqlmock.NewRows([]string{"id", "task_id", "submission_id", "generation", "attempt"}))
	mock.ExpectRollback()
	if sent, err := d.DispatchOne(context.Background()); sent || err != nil {
		t.Fatalf("sent=%v error=%v", sent, err)
	}
	if n := client.XLen(context.Background(), Stream).Val(); n != 0 {
		t.Fatal("not-due retry was published")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
