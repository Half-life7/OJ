package community

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"biue/internal/auth"
	"biue/internal/server"
	"github.com/go-sql-driver/mysql"
	"github.com/jmoiron/sqlx"
)

type Service struct {
	db  *sqlx.DB
	now func() time.Time
}

func New(db *sqlx.DB) *Service { return &Service{db: db, now: time.Now} }
func fail(err error) error {
	if err == nil {
		return nil
	}
	for _, e := range []error{ErrInvalid, ErrForbidden, ErrNotFound, ErrConflict, ErrRateLimited} {
		if errors.Is(err, e) {
			return e
		}
	}
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	var e *mysql.MySQLError
	if errors.As(err, &e) && e.Number == 1062 {
		return ErrConflict
	}
	return ErrUnavailable
}
func audit(ctx context.Context, tx *sqlx.Tx, a auth.User, action string, id uint64) error {
	_, e := tx.ExecContext(ctx, `INSERT INTO audit_logs(actor_user_id,action,object_type,object_id,request_id,details,created_at) VALUES (?,?,'community',?,?,'{}',UTC_TIMESTAMP(6))`, a.ID, action, id, server.RequestID(ctx))
	return e
}
func (s *Service) write(ctx context.Context, a auth.User, fn func(*sqlx.Tx, auth.User) error) error {
	tx, e := s.db.BeginTxx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if e != nil {
		return ErrUnavailable
	}
	defer tx.Rollback()
	var admission int
	if e = tx.GetContext(ctx, &admission, `SELECT id FROM judge_admission WHERE id=1 FOR UPDATE`); e != nil {
		return fail(e)
	}
	var actor auth.User
	if e = tx.GetContext(ctx, &actor, `SELECT id,username,account_type,global_role,status FROM users WHERE id=? FOR UPDATE`, a.ID); e != nil {
		return fail(e)
	}
	if actor.Status != "active" || actor.AccountType != "permanent" {
		return ErrForbidden
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO community_write_limits(user_id,window_start,writes) VALUES (?,UTC_TIMESTAMP(6),0) ON DUPLICATE KEY UPDATE user_id=user_id`, a.ID); e != nil {
		return fail(e)
	}
	var rate struct {
		Start time.Time `db:"window_start"`
		Count int       `db:"writes"`
	}
	if e = tx.GetContext(ctx, &rate, `SELECT window_start,writes FROM community_write_limits WHERE user_id=? FOR UPDATE`, a.ID); e != nil {
		return fail(e)
	}
	now := s.now().UTC()
	if now.Sub(rate.Start) >= time.Minute {
		rate.Start = now
		rate.Count = 0
	}
	if rate.Count >= 30 {
		return ErrRateLimited
	}
	if e = fn(tx, actor); e != nil {
		return fail(e)
	}
	if _, e = tx.ExecContext(ctx, `UPDATE community_write_limits SET window_start=?,writes=? WHERE user_id=?`, rate.Start, rate.Count+1, a.ID); e != nil {
		return fail(e)
	}
	if _, e = tx.ExecContext(ctx, `DELETE FROM global_board_cache`); e != nil {
		return fail(e)
	}
	return fail(tx.Commit())
}
func publicProblem(ctx context.Context, tx *sqlx.Tx, id uint64) error {
	var n uint64
	return fail(tx.GetContext(ctx, &n, `SELECT id FROM problems WHERE id=? AND status='published' AND visibility='public' FOR SHARE`, id))
}
func contestAccess(ctx context.Context, tx *sqlx.Tx, a auth.User, id uint64) error {
	var ok bool
	e := tx.GetContext(ctx, &ok, `SELECT EXISTS(SELECT 1 FROM contests c WHERE c.id=? AND (?='super_admin' OR EXISTS(SELECT 1 FROM contest_admins ca WHERE ca.contest_id=c.id AND ca.user_id=?) OR EXISTS(SELECT 1 FROM contest_team_members m WHERE m.contest_id=c.id AND m.user_id=?)))`, id, a.GlobalRole, a.ID, a.ID)
	if e != nil {
		return e
	}
	if !ok {
		return ErrNotFound
	}
	return nil
}
func contextAccess(ctx context.Context, tx *sqlx.Tx, a auth.User, problem, contest *uint64) error {
	if problem != nil {
		return publicProblem(ctx, tx, *problem)
	}
	if contest != nil {
		return contestAccess(ctx, tx, a, *contest)
	}
	return nil
}
func hasAC(ctx context.Context, tx *sqlx.Tx, user, problem uint64) error {
	var id uint64
	e := tx.GetContext(ctx, &id, `SELECT s.id FROM submissions s JOIN submission_results r ON r.submission_id=s.id AND r.generation=s.generation WHERE s.user_id=? AND s.problem_id=? AND s.kind='Submit' AND s.status='Accepted' AND r.status='Accepted' ORDER BY s.id LIMIT 1 FOR SHARE`, user, problem)
	if errors.Is(e, sql.ErrNoRows) {
		return ErrForbidden
	}
	return e
}

const docColumns = `d.id,d.owner_id,u.username,d.kind,d.title,d.body,d.visibility,d.review_status,d.problem_id,d.contest_id,d.parent_id,d.revision,d.created_at,d.updated_at,d.deleted_at,(SELECT COUNT(*) FROM discussion_likes l WHERE l.document_id=d.id) AS likes,(SELECT COUNT(*) FROM problem_list_favorites f WHERE f.document_id=d.id) AS favorites`

func load(ctx context.Context, tx *sqlx.Tx, id uint64, lock bool) (Document, error) {
	var d Document
	q := `SELECT ` + docColumns + ` FROM community_documents d JOIN users u ON u.id=d.owner_id WHERE d.id=?`
	if lock {
		q += ` FOR UPDATE`
	}
	e := tx.GetContext(ctx, &d, q, id)
	return d, fail(e)
}
func access(ctx context.Context, tx *sqlx.Tx, a auth.User, d Document) error {
	if d.Deleted != nil {
		return ErrNotFound
	}
	if d.Visibility == "private" && a.ID != d.OwnerID && a.GlobalRole != "super_admin" {
		return ErrNotFound
	}
	if e := contextAccess(ctx, tx, a, d.ProblemID, d.ContestID); e != nil {
		return e
	}
	if d.Kind == "solution" {
		if a.ID != d.OwnerID && a.GlobalRole != "super_admin" {
			if d.Review != "approved" {
				return ErrNotFound
			}
			if e := hasAC(ctx, tx, d.OwnerID, *d.ProblemID); e != nil {
				if errors.Is(e, ErrForbidden) {
					return ErrNotFound
				}
				return e
			}
		}
	}
	// Ancestors are checked in a bounded walk; soft deleting a parent hides replies.
	parent := d.ParentID
	for depth := 0; parent != nil; depth++ {
		if depth >= 16 {
			return ErrNotFound
		}
		p, e := load(ctx, tx, *parent, false)
		if e != nil {
			return e
		}
		if p.Deleted != nil || p.Visibility == "private" && p.OwnerID != a.ID && a.GlobalRole != "super_admin" {
			return ErrNotFound
		}
		parent = p.ParentID
	}
	return nil
}
func (s *Service) Get(ctx context.Context, a auth.User, id uint64) (Document, error) {
	tx, e := s.db.BeginTxx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return Document{}, ErrUnavailable
	}
	defer tx.Rollback()
	d, e := load(ctx, tx, id, false)
	if e != nil {
		return d, e
	}
	if e = access(ctx, tx, a, d); e != nil {
		return Document{}, fail(e)
	}
	d.Problems = []ListItem{}
	if d.Kind == "list" {
		e = tx.SelectContext(ctx, &d.Problems, `SELECT p.id,p.title,i.position FROM problem_list_items i JOIN problems p ON p.id=i.problem_id WHERE i.document_id=? AND p.status='published' AND p.visibility='public' ORDER BY i.position`, id)
		if e != nil {
			return Document{}, fail(e)
		}
	}
	d.HTML, e = Render(d.Body)
	if e != nil {
		return Document{}, ErrUnavailable
	}
	var capabilities struct {
		Liked     bool `db:"liked"`
		Favorited bool `db:"favorited"`
		CanDelete bool `db:"can_delete"`
	}
	if e = tx.GetContext(ctx, &capabilities, `SELECT
		EXISTS(SELECT 1 FROM discussion_likes l WHERE l.document_id=d.id AND l.user_id=? AND d.kind='discussion') AS liked,
		EXISTS(SELECT 1 FROM problem_list_favorites f WHERE f.document_id=d.id AND f.user_id=? AND d.kind='list') AS favorited,
		(d.owner_id=? OR ?='super_admin' OR EXISTS(SELECT 1 FROM contest_admins ca WHERE ca.contest_id=d.contest_id AND ca.user_id=?)) AS can_delete
		FROM community_documents d WHERE d.id=?`, a.ID, a.ID, a.ID, a.GlobalRole, a.ID, id); e != nil {
		return Document{}, fail(e)
	}
	d.Liked, d.Favorited, d.CanDelete = capabilities.Liked, capabilities.Favorited, capabilities.CanDelete
	d.CanEdit = a.ID != 0 && a.ID == d.OwnerID
	d.CanReview = a.GlobalRole == "super_admin" && d.Kind == "solution"
	return d, fail(tx.Commit())
}
func sameID(a, b *uint64) bool { return a == nil && b == nil || a != nil && b != nil && *a == *b }
func (s *Service) Save(ctx context.Context, a auth.User, id uint64, in Input) (uint64, error) {
	if e := valid(in); e != nil {
		return 0, e
	}
	result := id
	e := s.write(ctx, a, func(tx *sqlx.Tx, a auth.User) error {
		if e := contextAccess(ctx, tx, a, in.ProblemID, in.ContestID); e != nil {
			return e
		}
		if in.Kind == "solution" {
			if e := hasAC(ctx, tx, a.ID, *in.ProblemID); e != nil {
				return e
			}
		}
		if in.ParentID != nil {
			p, e := load(ctx, tx, *in.ParentID, true)
			if e != nil {
				return e
			}
			if e = access(ctx, tx, a, p); e != nil {
				return e
			}
			if p.Kind != "discussion" || !sameID(p.ProblemID, in.ProblemID) || !sameID(p.ContestID, in.ContestID) || p.Visibility != in.Visibility {
				return ErrInvalid
			}
			depth := 0
			for p.ParentID != nil {
				depth++
				if depth >= 15 {
					return ErrInvalid
				}
				p, e = load(ctx, tx, *p.ParentID, false)
				if e != nil {
					return e
				}
			}
		}
		review := "approved"
		if in.Kind == "solution" {
			review = "pending"
		}
		if id == 0 {
			r, e := tx.ExecContext(ctx, `INSERT INTO community_documents(owner_id,kind,title,body,visibility,review_status,problem_id,contest_id,parent_id,created_at,updated_at) VALUES (?,?,?,?,?,?,?,?,?,UTC_TIMESTAMP(6),UTC_TIMESTAMP(6))`, a.ID, in.Kind, in.Title, in.Body, in.Visibility, review, in.ProblemID, in.ContestID, in.ParentID)
			if e != nil {
				return e
			}
			n, e := r.LastInsertId()
			if e != nil {
				return e
			}
			result = uint64(n)
		} else {
			old, e := load(ctx, tx, id, true)
			if e != nil {
				return e
			}
			if old.OwnerID != a.ID {
				return ErrForbidden
			}
			if e = access(ctx, tx, a, old); e != nil {
				return e
			}
			if old.Revision != in.Revision {
				return ErrConflict
			}
			if old.Kind != in.Kind || !sameID(old.ProblemID, in.ProblemID) || !sameID(old.ContestID, in.ContestID) || !sameID(old.ParentID, in.ParentID) {
				return ErrInvalid
			}
			if old.Kind == "discussion" && old.ParentID != nil && old.Visibility != in.Visibility {
				return ErrInvalid
			}
			_, e = tx.ExecContext(ctx, `UPDATE community_documents SET title=?,body=?,visibility=?,review_status=?,published_at=NULL,revision=revision+1,updated_at=UTC_TIMESTAMP(6) WHERE id=?`, in.Title, in.Body, in.Visibility, review, id)
			if e != nil {
				return e
			}
		}
		if in.Kind == "list" {
			if _, e := tx.ExecContext(ctx, `DELETE FROM problem_list_items WHERE document_id=?`, result); e != nil {
				return e
			}
			for i, p := range in.Problems {
				if e := publicProblem(ctx, tx, p); e != nil {
					return e
				}
				if _, e := tx.ExecContext(ctx, `INSERT INTO problem_list_items(document_id,problem_id,position) VALUES (?,?,?)`, result, p, i); e != nil {
					return e
				}
			}
		}
		return audit(ctx, tx, a, "community.save", result)
	})
	if e != nil {
		return 0, e
	}
	return result, nil
}
func (s *Service) Action(ctx context.Context, a auth.User, id, revision uint64, action string) error {
	return s.write(ctx, a, func(tx *sqlx.Tx, a auth.User) error {
		d, e := load(ctx, tx, id, true)
		if e != nil {
			return e
		}
		if e = access(ctx, tx, a, d); e != nil {
			return e
		}
		switch action {
		case "delete":
			moderator := a.GlobalRole == "super_admin"
			if d.ContestID != nil {
				var ok bool
				if e = tx.GetContext(ctx, &ok, `SELECT EXISTS(SELECT 1 FROM contest_admins WHERE contest_id=? AND user_id=?)`, *d.ContestID, a.ID); e != nil {
					return e
				}
				moderator = moderator || ok
			}
			if d.OwnerID != a.ID && !moderator {
				return ErrForbidden
			}
			if revision != d.Revision {
				return ErrConflict
			}
			_, e = tx.ExecContext(ctx, `UPDATE community_documents SET deleted_at=UTC_TIMESTAMP(6),revision=revision+1 WHERE id=?`, id)
		case "approve", "reject":
			if a.GlobalRole != "super_admin" || d.Kind != "solution" {
				return ErrForbidden
			}
			if revision != d.Revision {
				return ErrConflict
			}
			if e = hasAC(ctx, tx, d.OwnerID, *d.ProblemID); e != nil {
				return e
			}
			status := "rejected"
			if action == "approve" {
				status = "approved"
			}
			_, e = tx.ExecContext(ctx, `UPDATE community_documents SET review_status=?,published_at=IF(?='approved',UTC_TIMESTAMP(6),NULL),revision=revision+1,updated_at=UTC_TIMESTAMP(6) WHERE id=?`, status, status, id)
		case "favorite", "unfavorite", "like", "unlike":
			table := "problem_list_favorites"
			kind := "list"
			if action == "like" || action == "unlike" {
				table = "discussion_likes"
				kind = "discussion"
			}
			if d.Kind != kind {
				return ErrInvalid
			}
			if action == "favorite" || action == "like" {
				_, e = tx.ExecContext(ctx, `INSERT INTO `+table+`(document_id,user_id,created_at) VALUES (?,?,UTC_TIMESTAMP(6)) ON DUPLICATE KEY UPDATE user_id=user_id`, id, a.ID)
			} else {
				_, e = tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE document_id=? AND user_id=?`, id, a.ID)
			}
		default:
			return ErrInvalid
		}
		if e != nil {
			return e
		}
		return audit(ctx, tx, a, "community."+action, id)
	})
}

// List applies coarse visibility in SQL, then the full context/ancestor policy.
// The cursor advances over scanned records, including newly hidden contexts.
type Page struct {
	Items []Document `json:"items"`
	Next  uint64     `json:"next"`
}

func (s *Service) List(ctx context.Context, a auth.User, f Filter) (Page, error) {
	if f.Kind != "list" && f.Kind != "discussion" && f.Kind != "solution" {
		return Page{}, ErrInvalid
	}
	if f.Mine && a.ID == 0 || f.Favorites && a.ID == 0 {
		return Page{}, ErrForbidden
	}
	if f.Review && a.GlobalRole != "super_admin" {
		return Page{}, ErrForbidden
	}
	tx, e := s.db.BeginTxx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return Page{}, ErrUnavailable
	}
	defer tx.Rollback()
	where := `d.kind=? AND d.deleted_at IS NULL AND d.id>?`
	args := []any{f.Kind, f.After}
	if f.Review {
		where += ` AND d.review_status='pending'`
	} else if f.Mine {
		where += ` AND d.owner_id=?`
		args = append(args, a.ID)
	} else {
		where += ` AND (d.visibility='public' OR d.owner_id=?)`
		args = append(args, a.ID)
	}
	if f.Favorites {
		where += ` AND EXISTS(SELECT 1 FROM problem_list_favorites f WHERE f.document_id=d.id AND f.user_id=?)`
		args = append(args, a.ID)
	}
	for _, v := range []struct {
		field string
		id    uint64
	}{{"problem_id", f.ProblemID}, {"contest_id", f.ContestID}, {"parent_id", f.ParentID}} {
		if v.id != 0 {
			where += ` AND d.` + v.field + `=?`
			args = append(args, v.id)
		}
	}
	var docs []Document
	if e = tx.SelectContext(ctx, &docs, `SELECT `+docColumns+` FROM community_documents d JOIN users u ON u.id=d.owner_id WHERE `+where+` ORDER BY d.id LIMIT 100`, args...); e != nil {
		return Page{}, fail(e)
	}
	page := Page{Items: []Document{}}
	for _, d := range docs {
		page.Next = d.ID
		if e = access(ctx, tx, a, d); e != nil {
			if errors.Is(e, ErrNotFound) || errors.Is(e, ErrForbidden) {
				continue
			}
			return Page{}, fail(e)
		}
		d.Body = ""
		page.Items = append(page.Items, d)
	}
	if len(docs) < 100 {
		page.Next = 0
	}
	return page, fail(tx.Commit())
}

func marshal(v any) string { b, _ := json.Marshal(v); return string(b) }
