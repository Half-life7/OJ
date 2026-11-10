package submission

import (
	"context"
	"github.com/DATA-DOG/go-sqlmock"
	"testing"
	"time"
)

func TestContestPinsVersionsAndAllowsTemporaryAccount(t *testing.T) {
	f := newPersistenceFixture(t)
	input := persistenceInput()
	input.ContestID = 5
	a := Actor{ID: 7, AccountType: "contest", Role: "user"}
	expectAdmission(f.mock, 0)
	expectAccount(f.mock, "contest", "active")
	f.mock.ExpectQuery("SELECT starts_at,ends_at,settled_at FROM contests").WithArgs(uint64(5)).WillReturnRows(sqlmock.NewRows([]string{"starts_at", "ends_at", "settled_at"}).AddRow(persistenceNow.Add(-time.Hour), persistenceNow.Add(time.Hour), nil))
	f.mock.ExpectQuery("SELECT t.id FROM contest_team_members").WithArgs(uint64(5), uint64(7)).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(6))
	f.mock.ExpectQuery("SELECT cp.problem_id AS id.*FROM contest_problems").WithArgs(uint64(5), uint64(42)).WillReturnRows(pinnedRows(3, 2))
	f.mock.ExpectExec("INSERT INTO submissions").WithArgs(uint64(7), uint64(42), uint64(8), uint64(3), uint64(5), uint64(6), "Submit", input.Language, input.SourceCode, nil, 2, persistenceNow, persistenceNow).WillReturnResult(sqlmock.NewResult(101, 1))
	expectTaskAndOutbox(f.mock)
	expectCreationAudit(f.mock, "Submit").WillReturnResult(sqlmock.NewResult(1, 1))
	f.mock.ExpectCommit()
	got, err := f.service.Create(context.Background(), a, "Submit", input, "server-request-id")
	if err != nil || got.DataVersion != 3 || got.StatementVersion != 8 {
		t.Fatal(got, err)
	}
}
func TestContestWindowIsHalfOpen(t *testing.T) {
	for _, offset := range []time.Duration{-time.Nanosecond, 2 * time.Hour} {
		t.Run(offset.String(), func(t *testing.T) {
			f := newPersistenceFixture(t)
			input := persistenceInput()
			input.ContestID = 5
			expectAdmission(f.mock, 0)
			expectAccount(f.mock, "permanent", "active")
			start := persistenceNow.Add(-offset)
			f.mock.ExpectQuery("SELECT starts_at,ends_at,settled_at FROM contests").WillReturnRows(sqlmock.NewRows([]string{"starts_at", "ends_at", "settled_at"}).AddRow(start, start.Add(2*time.Hour), nil))
			f.mock.ExpectRollback()
			if _, err := f.service.Create(context.Background(), permanentActor, "Submit", input, ""); err != ErrForbidden {
				t.Fatal(err)
			}
		})
	}
}
