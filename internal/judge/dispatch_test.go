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

func TestDispatchRecovery(t *testing.T) {
	for _, scenario := range []string{"success", "redis failure", "mark failure", "commit failure", "empty", "invalid"} {
		t.Run(scenario, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			r := miniredis.RunT(t)
			client := redis.NewClient(&redis.Options{Addr: r.Addr(), MaxRetries: -1})
			defer client.Close()
			d, err := NewDispatcher(sqlx.NewDb(db, "sqlmock"), client)
			if err != nil {
				t.Fatal(err)
			}
			mock.ExpectBegin()
			rows := sqlmock.NewRows([]string{"id", "task_id", "submission_id", "generation", "attempt"})
			if scenario != "empty" {
				attempt := 1
				if scenario == "invalid" {
					attempt = 5
				}
				rows.AddRow(7, 8, 9, 1, attempt)
			}
			mock.ExpectQuery("SELECT .* FOR UPDATE SKIP LOCKED").WillReturnRows(rows)
			switch scenario {
			case "empty", "invalid":
				mock.ExpectRollback()
			default:
				mock.ExpectExec("UPDATE judge_task_outbox SET dispatch_attempts").WithArgs(7).WillReturnResult(sqlmock.NewResult(0, 1))
				if scenario == "redis failure" {
					r.SetError("unavailable")
					mock.ExpectCommit()
				} else {
					mark := mock.ExpectExec("UPDATE judge_task_outbox SET dispatched_at").WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), 7)
					if scenario == "mark failure" {
						mark.WillReturnError(errors.New("database unavailable"))
						mock.ExpectRollback()
					} else {
						mark.WillReturnResult(sqlmock.NewResult(0, 1))
						commit := mock.ExpectCommit()
						if scenario == "commit failure" {
							commit.WillReturnError(errors.New("commit unknown"))
						}
					}
				}
			}
			sent, err := d.DispatchOne(context.Background())
			if sent != (scenario == "success") {
				t.Fatalf("sent=%v error=%v", sent, err)
			}
			if (err != nil) != (scenario != "success" && scenario != "empty") {
				t.Fatalf("unexpected error: %v", err)
			}
			if scenario == "success" || scenario == "mark failure" || scenario == "commit failure" {
				messages, err := client.XRange(context.Background(), Stream, "-", "+").Result()
				if err != nil || len(messages) != 1 {
					t.Fatalf("messages=%v error=%v", messages, err)
				}
				envelope, err := ParseEnvelope(messages[0].Values)
				if err != nil || envelope != (TaskEnvelope{TaskID: 8, SubmissionID: 9, Generation: 1, Attempt: 1}) {
					t.Fatalf("envelope=%+v error=%v", envelope, err)
				}
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
