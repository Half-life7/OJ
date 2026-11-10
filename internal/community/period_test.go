package community

import (
	"strings"
	"testing"
)

func TestLeaderboardPeriodsRemainWhitelisted(t *testing.T) {
	for _, kind := range []string{"practice", "contest", "solutions"} {
		for _, period := range []string{"all", "week", "month"} {
			q, e := boardQueryPeriod(kind, period)
			if e != nil {
				t.Fatal(e)
			}
			if period == "week" && !strings.Contains(q, "INTERVAL 7 DAY") || period == "month" && !strings.Contains(q, "INTERVAL 30 DAY") {
				t.Fatal(q)
			}
		}
	}
	for _, bad := range []string{"year", "week;DROP TABLE users", "-1"} {
		if _, e := boardQueryPeriod("practice", bad); e != ErrInvalid {
			t.Fatal(e)
		}
	}
}
