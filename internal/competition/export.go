package competition

import (
	"biue/internal/auth"
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"strconv"
)

func (s *Service) ExportCSV(ctx context.Context, a auth.User, id uint64) ([]byte, error) {
	v, e := s.View(ctx, a, id)
	if e != nil {
		return nil, e
	}
	if !v.Manager {
		return nil, ErrForbidden
	}
	var b bytes.Buffer
	w := csv.NewWriter(&b)
	w.UseCRLF = true
	if e = w.Write([]string{"rank", "team_id", "team", "username", "display_name", "student_no", "participant_type", "status", "score", "solved", "penalty"}); e != nil {
		return nil, e
	}
	for _, team := range v.Teams {
		for _, member := range v.Members {
			if member.TeamID != team.ID {
				continue
			}
			var identity struct {
				Username, Name string
				Student        *string
			}
			if json.Unmarshal(member.Identity, &identity) != nil {
				return nil, ErrUnavailable
			}
			student := ""
			if identity.Student != nil {
				student = *identity.Student
			}
			rank, score, solved, penalty := "0", "0", "0", "0"
			for _, row := range v.Standings {
				if row.TeamID == team.ID {
					rank = strconv.Itoa(row.Rank)
					score = strconv.FormatInt(row.Score, 10)
					solved = strconv.Itoa(row.Solved)
					penalty = strconv.FormatInt(row.Penalty, 10)
				}
			}
			record := []string{rank, strconv.FormatUint(team.ID, 10), team.Name, identity.Username, identity.Name, student, team.Type, team.Status, score, solved, penalty}
			for i := range record {
				record[i] = spreadsheetCell(record[i])
			}
			if e = w.Write(record); e != nil {
				return nil, e
			}
		}
	}
	w.Flush()
	if w.Error() != nil {
		return nil, ErrUnavailable
	}
	return b.Bytes(), nil
}
