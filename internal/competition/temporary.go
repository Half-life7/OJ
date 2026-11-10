package competition

import (
	"biue/internal/auth"
	"context"
	"github.com/jmoiron/sqlx"
)

type Temporary struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Name     string `json:"display_name"`
}

func (s *Service) Temporary(ctx context.Context, a auth.User, id uint64, in Temporary) (uint64, error) {
	if !clean(in.Name, 100) {
		return 0, ErrInvalid
	}
	// Authorize before expensive hashing; authorization is rechecked in transaction.
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return 0, ErrUnavailable
	}
	err = allowed(ctx, tx, a, id)
	tx.Rollback()
	if err != nil {
		return 0, storageError(err)
	}
	name, hash, err := auth.TemporaryCredentials(in.Username, in.Password)
	if err != nil {
		return 0, ErrInvalid
	}
	var userID uint64
	err = s.write(ctx, a, func(tx *sqlx.Tx, a auth.User) error {
		c, e := contest(ctx, tx, id)
		if e != nil {
			return e
		}
		if e = allowed(ctx, tx, a, id); e != nil {
			return e
		}
		if !s.now().Before(c.Start) || c.Settled != nil {
			return ErrConflict
		}
		r, e := tx.ExecContext(ctx, `INSERT INTO persons(display_name,student_no_status,created_at,updated_at) VALUES (?,'none',UTC_TIMESTAMP(6),UTC_TIMESTAMP(6))`, in.Name)
		if e != nil {
			return e
		}
		person, e := r.LastInsertId()
		if e != nil {
			return e
		}
		r, e = tx.ExecContext(ctx, `INSERT INTO users(username,password_hash,account_type,global_role,status,token_version,created_at,updated_at) VALUES (?,?,'contest','user','active',1,UTC_TIMESTAMP(6),UTC_TIMESTAMP(6))`, name, hash)
		if e != nil {
			return e
		}
		u, e := r.LastInsertId()
		if e != nil {
			return e
		}
		userID = uint64(u)
		if _, e = tx.ExecContext(ctx, `INSERT INTO person_accounts(user_id,person_id,created_at) VALUES (?,?,UTC_TIMESTAMP(6))`, userID, person); e != nil {
			return e
		}
		if _, e = s.addTeam(ctx, tx, c, in.Name, []uint64{userID}, "active", "official"); e != nil {
			return e
		}
		return audit(ctx, tx, a.ID, "contest.temporary_account", "contest", id, map[string]any{"user_id": userID})
	})
	if err != nil {
		return 0, err
	}
	return userID, nil
}
