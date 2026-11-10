package competition

import (
	"biue/internal/scoring"
	"github.com/jmoiron/sqlx"
)

// PointsPolicy is injected when the service is constructed; settlement records
// its stable name in the audit and never reruns a policy for settled contests.
type PointsPolicy interface {
	Name() string
	Calculate(teams, rank int) (int, error)
}
type LinearPoints struct{}

func (LinearPoints) Name() string                           { return "linear100-v1" }
func (LinearPoints) Calculate(teams, rank int) (int, error) { return scoring.Points(teams, rank) }
func NewWithPointsPolicy(db *sqlx.DB, p PointsPolicy) (*Service, error) {
	if db == nil || p == nil || !clean(p.Name(), 64) {
		return nil, ErrInvalid
	}
	s := New(db)
	s.points = p
	return s, nil
}
