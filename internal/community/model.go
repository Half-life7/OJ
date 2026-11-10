// Package community implements practice statistics, learning content and the three global boards.
package community

import (
	"errors"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

var (
	ErrInvalid     = errors.New("invalid community request")
	ErrForbidden   = errors.New("community access forbidden")
	ErrNotFound    = errors.New("community object not found")
	ErrConflict    = errors.New("community revision conflict")
	ErrUnavailable = errors.New("community storage unavailable")
	ErrRateLimited = errors.New("community write rate exceeded")
)

type Input struct {
	Kind       string   `json:"kind"`
	Title      string   `json:"title"`
	Body       string   `json:"body"`
	Visibility string   `json:"visibility"`
	ProblemID  *uint64  `json:"problem_id"`
	ContestID  *uint64  `json:"contest_id"`
	ParentID   *uint64  `json:"parent_id"`
	Revision   uint64   `json:"revision"`
	Problems   []uint64 `json:"problems"`
}
type Document struct {
	ID         uint64     `db:"id" json:"id"`
	OwnerID    uint64     `db:"owner_id" json:"owner_id"`
	Username   string     `db:"username" json:"username"`
	Kind       string     `db:"kind" json:"kind"`
	Title      string     `db:"title" json:"title"`
	Body       string     `db:"body" json:"body"`
	HTML       string     `db:"-" json:"html"`
	Visibility string     `db:"visibility" json:"visibility"`
	Review     string     `db:"review_status" json:"review_status"`
	ProblemID  *uint64    `db:"problem_id" json:"problem_id"`
	ContestID  *uint64    `db:"contest_id" json:"contest_id"`
	ParentID   *uint64    `db:"parent_id" json:"parent_id"`
	Revision   uint64     `db:"revision" json:"revision"`
	Created    time.Time  `db:"created_at" json:"created_at"`
	Updated    time.Time  `db:"updated_at" json:"updated_at"`
	Deleted    *time.Time `db:"deleted_at" json:"-"`
	Problems   []ListItem `db:"-" json:"problems"`
	Likes      int        `db:"likes" json:"likes"`
	Favorites  int        `db:"favorites" json:"favorites"`
	Liked      bool       `db:"-" json:"liked"`
	Favorited  bool       `db:"-" json:"favorited"`
	CanEdit    bool       `db:"-" json:"can_edit"`
	CanDelete  bool       `db:"-" json:"can_delete"`
	CanReview  bool       `db:"-" json:"can_review"`
}
type ListItem struct {
	ID       uint64 `db:"id" json:"id"`
	Title    string `db:"title" json:"title"`
	Position int    `db:"position" json:"position"`
}
type Filter struct {
	Kind                           string
	After                          uint64
	Mine, Favorites, Review        bool
	ProblemID, ContestID, ParentID uint64
}

func text(s string, max int) bool {
	return utf8.ValidString(s) && len(s) <= max && !strings.ContainsRune(s, 0)
}
func valid(in Input) error {
	if in.Kind != "list" && in.Kind != "discussion" && in.Kind != "solution" || in.Visibility != "public" && in.Visibility != "private" || !text(in.Title, 800) || utf8.RuneCountInString(in.Title) > 200 || strings.TrimSpace(in.Title) == "" || strings.ContainsFunc(in.Title, unicode.IsControl) || !text(in.Body, 65536) {
		return ErrInvalid
	}
	for _, id := range []*uint64{in.ProblemID, in.ContestID, in.ParentID} {
		if id != nil && *id == 0 {
			return ErrInvalid
		}
	}
	switch in.Kind {
	case "list":
		if in.ProblemID != nil || in.ContestID != nil || in.ParentID != nil || len(in.Problems) > 200 {
			return ErrInvalid
		}
	case "solution":
		if in.ProblemID == nil || in.ContestID != nil || in.ParentID != nil || len(in.Problems) > 0 || strings.TrimSpace(in.Body) == "" {
			return ErrInvalid
		}
	case "discussion":
		if (in.ProblemID == nil) == (in.ContestID == nil) || len(in.Problems) > 0 || strings.TrimSpace(in.Body) == "" {
			return ErrInvalid
		}
	}
	seen := map[uint64]bool{}
	for _, p := range in.Problems {
		if p == 0 || seen[p] {
			return ErrInvalid
		}
		seen[p] = true
	}
	return nil
}
