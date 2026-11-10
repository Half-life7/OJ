package competition

import (
	"biue/internal/auth"
	"context"
	"errors"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"testing"
	"time"
)

func fixture(t *testing.T) (*Service, sqlmock.Sqlmock) {
	t.Helper()
	db, m, e := sqlmock.New()
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		db.Close()
		if e := m.ExpectationsWereMet(); e != nil {
			t.Error(e)
		}
	})
	s := New(sqlx.NewDb(db, "sqlmock"))
	s.now = func() time.Time { return time.Date(2026, 11, 10, 1, 0, 0, 0, time.UTC) }
	return s, m
}
func begin(m sqlmock.Sqlmock, role string) {
	m.ExpectBegin()
	m.ExpectQuery("SELECT id FROM judge_admission").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1))
	m.ExpectQuery("SELECT id, username, account_type, global_role, status FROM users").WithArgs(uint64(1)).WillReturnRows(sqlmock.NewRows([]string{"id", "username", "account_type", "global_role", "status"}).AddRow(1, "actor", "permanent", role, "active"))
}
func TestContestAuthorizationRechecked(t *testing.T) {
	s, m := fixture(t)
	begin(m, "user")
	m.ExpectRollback()
	_, err := s.Save(context.Background(), auth.User{ID: 1, GlobalRole: "super_admin"}, Contest{Title: "Contest", Mode: "OI", Policy: "open", Start: s.now().Add(time.Hour), End: s.now().Add(2 * time.Hour)})
	if err != ErrForbidden {
		t.Fatal(err)
	}
}
func TestIdentityAuditFailureRollsBack(t *testing.T) {
	s, m := fixture(t)
	begin(m, "super_admin")
	m.ExpectQuery("SELECT id,display_name.*FROM persons").WithArgs(uint64(2)).WillReturnRows(sqlmock.NewRows([]string{"id", "display_name", "student_no", "student_no_status", "organization", "revision"}).AddRow(2, "Ada", "S001", "pending", "", 3))
	m.ExpectExec("UPDATE persons").WillReturnResult(sqlmock.NewResult(0, 1))
	m.ExpectExec("INSERT INTO student_no_history").WillReturnResult(sqlmock.NewResult(1, 1))
	m.ExpectExec("INSERT INTO audit_logs").WillReturnError(errors.New("disk failure"))
	m.ExpectRollback()
	n := "S001"
	err := s.Identity(context.Background(), auth.User{ID: 1}, 2, IdentityChange{Revision: 3, Name: "Ada", Student: &n, Status: "verified", Reason: "verified in person"})
	if err != ErrUnavailable {
		t.Fatal(err)
	}
}
func TestStaleIdentityRejected(t *testing.T) {
	s, m := fixture(t)
	begin(m, "super_admin")
	m.ExpectQuery("SELECT id,display_name.*FROM persons").WillReturnRows(sqlmock.NewRows([]string{"id", "display_name", "student_no", "student_no_status", "organization", "revision"}).AddRow(2, "Ada", nil, "none", "", 3))
	m.ExpectRollback()
	if err := s.Identity(context.Background(), auth.User{ID: 1}, 2, IdentityChange{Revision: 2, Name: "Ada", Status: "none", Reason: "correction"}); err != ErrConflict {
		t.Fatal(err)
	}
}
func TestSettledContestReturnsSnapshotWithoutWrites(t *testing.T) {
	s, m := fixture(t)
	begin(m, "super_admin")
	now := s.now()
	m.ExpectQuery("SELECT .*FROM contests WHERE id=.*FOR UPDATE").WithArgs(uint64(5)).WillReturnRows(sqlmock.NewRows([]string{"id", "title", "scoring_mode", "enrollment_policy", "approval_required", "team_mode", "starts_at", "ends_at", "revision", "settled_at"}).AddRow(5, "Final", "IOI", "open", false, false, now.Add(-2*time.Hour), now.Add(-time.Hour), 1, now))
	m.ExpectQuery("SELECT standings FROM contest_snapshots").WithArgs(uint64(5)).WillReturnRows(sqlmock.NewRows([]string{"standings"}).AddRow(`[{"team_id":9,"rank":1,"score":100}]`))
	m.ExpectCommit()
	rows, err := s.Settle(context.Background(), auth.User{ID: 1}, 5)
	if err != nil || len(rows) != 1 || rows[0].Score != 100 {
		t.Fatal(rows, err)
	}
}
func TestInvalidIdentityDoesNotTouchStorage(t *testing.T) {
	s, _ := fixture(t)
	if err := s.Identity(context.Background(), auth.User{ID: 1}, 2, IdentityChange{Name: "Ada", Status: "verified", Reason: "check"}); err != ErrInvalid {
		t.Fatal(err)
	}
}

func TestRevokeContestAdminAfterSettlement(t *testing.T) {
	s, m := fixture(t)
	begin(m, "super_admin")
	now := s.now()
	m.ExpectQuery("SELECT .*FROM contests WHERE id=.*FOR UPDATE").WithArgs(uint64(5)).WillReturnRows(sqlmock.NewRows([]string{"id", "title", "scoring_mode", "enrollment_policy", "approval_required", "team_mode", "starts_at", "ends_at", "revision", "settled_at"}).AddRow(5, "Final", "OI", "open", false, false, now.Add(-2*time.Hour), now.Add(-time.Hour), 1, now))
	m.ExpectExec("DELETE FROM contest_admins").WithArgs(uint64(5), uint64(9)).WillReturnResult(sqlmock.NewResult(0, 1))
	m.ExpectExec("INSERT INTO audit_logs").WillReturnResult(sqlmock.NewResult(1, 1))
	m.ExpectCommit()
	if e := s.Manage(context.Background(), auth.User{ID: 1}, 5, Manage{Action: "revoke_admin", UserID: 9}); e != nil {
		t.Fatal(e)
	}
}
