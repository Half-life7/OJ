package competition

import (
	"biue/internal/auth"
	"context"
	"github.com/jmoiron/sqlx"
	"strings"
)

type Person struct {
	Accounts     []AccountRef `db:"-" json:"accounts,omitempty"`
	ID           uint64       `db:"id" json:"id"`
	Name         string       `db:"display_name" json:"display_name"`
	Student      *string      `db:"student_no" json:"student_no"`
	Status       string       `db:"student_no_status" json:"student_no_status"`
	Organization string       `db:"organization" json:"organization"`
	Revision     uint64       `db:"revision" json:"revision"`
}

type AccountRef struct {
	ID       uint64 `db:"id" json:"id"`
	PersonID uint64 `db:"person_id" json:"person_id"`
	Username string `db:"username" json:"username"`
	Type     string `db:"account_type" json:"account_type"`
	Status   string `db:"status" json:"status"`
}
type IdentityChange struct {
	Revision     uint64  `json:"revision"`
	Name         string  `json:"display_name"`
	Student      *string `json:"student_no"`
	Status       string  `json:"student_no_status"`
	Organization string  `json:"organization"`
	Reason       string  `json:"reason"`
}

func (s *Service) People(ctx context.Context, a auth.User, after uint64) ([]Person, error) {
	if a.GlobalRole != "super_admin" {
		return nil, ErrForbidden
	}
	items := []Person{}
	err := s.db.SelectContext(ctx, &items, `SELECT id,display_name,student_no,student_no_status,organization,revision FROM persons WHERE id>? ORDER BY id LIMIT 100`, after)
	if err == nil && len(items) > 0 {
		ids := make([]uint64, len(items))
		indices := map[uint64]int{}
		for i, p := range items {
			ids[i] = p.ID
			indices[p.ID] = i
		}
		query, args, e := sqlx.In(`SELECT u.id,pa.person_id,u.username,u.account_type,u.status FROM person_accounts pa JOIN users u ON u.id=pa.user_id WHERE pa.person_id IN (?) ORDER BY pa.person_id,u.id`, ids)
		if e != nil {
			return nil, ErrUnavailable
		}
		var accounts []AccountRef
		err = s.db.SelectContext(ctx, &accounts, query, args...)
		for _, a := range accounts {
			i := indices[a.PersonID]
			items[i].Accounts = append(items[i].Accounts, a)
		}
	}
	return items, storageError(err)
}
func (s *Service) Identity(ctx context.Context, a auth.User, id uint64, in IdentityChange) error {
	if !clean(in.Name, 100) || !clean(in.Reason, 255) || in.Organization != "" && !clean(in.Organization, 200) {
		return ErrInvalid
	}
	if in.Student != nil {
		v := strings.TrimSpace(*in.Student)
		if v == "" {
			in.Student = nil
		} else if !clean(v, 64) {
			return ErrInvalid
		} else {
			in.Student = &v
		}
	}
	if in.Student == nil && in.Status != "none" || in.Student != nil && (in.Status != "pending" && in.Status != "verified" && in.Status != "rejected") {
		return ErrInvalid
	}
	return s.write(ctx, a, func(tx *sqlx.Tx, a auth.User) error {
		if a.GlobalRole != "super_admin" {
			return ErrForbidden
		}
		var old Person
		if err := tx.GetContext(ctx, &old, `SELECT id,display_name,student_no,student_no_status,organization,revision FROM persons WHERE id=? FOR UPDATE`, id); err != nil {
			return err
		}
		if in.Revision != old.Revision {
			return ErrConflict
		}
		if _, err := tx.ExecContext(ctx, `UPDATE persons SET display_name=?,student_no=?,student_no_status=?,organization=?,revision=revision+1,updated_at=UTC_TIMESTAMP(6) WHERE id=?`, in.Name, in.Student, in.Status, in.Organization, id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO student_no_history(person_id,old_student_no,new_student_no,old_status,new_status,actor_user_id,reason,created_at) VALUES (?,?,?,?,?,?,?,UTC_TIMESTAMP(6))`, id, old.Student, in.Student, old.Status, in.Status, a.ID, in.Reason); err != nil {
			return err
		}
		return audit(ctx, tx, a.ID, "identity.update", "person", id, map[string]any{"before": old, "after": in})
	})
}

type Link struct {
	UserID uint64 `json:"user_id"`
	From   uint64 `json:"from_person_id"`
	To     uint64 `json:"to_person_id"`
	Reason string `json:"reason"`
}

func (s *Service) Link(ctx context.Context, a auth.User, in Link) error {
	if in.UserID == 0 || in.From == 0 || in.To == 0 || in.From == in.To || !clean(in.Reason, 255) {
		return ErrInvalid
	}
	return s.write(ctx, a, func(tx *sqlx.Tx, a auth.User) error {
		if a.GlobalRole != "super_admin" {
			return ErrForbidden
		}
		var target uint64
		if err := tx.GetContext(ctx, &target, `SELECT id FROM persons WHERE id=? FOR UPDATE`, in.To); err != nil {
			return err
		}
		r, err := tx.ExecContext(ctx, `UPDATE person_accounts SET person_id=? WHERE user_id=? AND person_id=?`, in.To, in.UserID, in.From)
		if err != nil {
			return err
		}
		n, err := r.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return ErrConflict
		}
		// Existing contest snapshots and point ownership intentionally remain fixed.
		if err := audit(ctx, tx, a.ID, "identity.link", "user", in.UserID, in); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `DELETE FROM global_board_cache`)
		return err
	})
}
