package operations

import (
	"biue/internal/auth"
	"context"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"testing"
)

func TestOrdinaryAccountCannotReadAdministration(t *testing.T) {
	db, m, e := sqlmock.New()
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	s := New(sqlx.NewDb(db, "sqlmock"), nil, nil, nil, nil, 100)
	m.ExpectQuery("SELECT EXISTS").WithArgs(uint64(1)).WillReturnRows(sqlmock.NewRows([]string{"allowed"}).AddRow(false))
	if _, e = s.Users(context.Background(), auth.User{ID: 1, GlobalRole: "super_admin"}, 0, ""); e != ErrForbidden {
		t.Fatal(e)
	}
	if e = m.ExpectationsWereMet(); e != nil {
		t.Fatal(e)
	}
}
func TestCannotDisableSelf(t *testing.T) {
	s := &Service{}
	if e := s.SetStatus(context.Background(), auth.User{ID: 1}, 1, "disabled", "test"); e != ErrInvalid {
		t.Fatal(e)
	}
}

func TestResetRequiresExactConfirmationAndReason(t *testing.T) {
	s := &Service{}
	for _, input := range []ResetInput{
		{Confirmation: "初始化", Reason: "test"},
		{Confirmation: ResetConfirmation, Reason: ""},
		{Confirmation: ResetConfirmation, Reason: "line\nbreak"},
	} {
		if _, err := s.Reset(context.Background(), auth.User{}, input, "request"); err != ErrInvalid {
			t.Fatalf("invalid reset input accepted: %+v err=%v", input, err)
		}
	}
}
