package competition

import (
	"strings"
	"testing"
)

func TestRosterStrictParsing(t *testing.T) {
	for _, raw := range []string{"", "name\nuser", "username,username\na,a", "username,unknown\na,b", "username,team\na", strings.Repeat("x", (256<<10)+1)} {
		if _, e := parseRoster(raw); e != ErrInvalid {
			t.Fatalf("accepted invalid CSV: %q %v", raw[:min(len(raw), 40)], e)
		}
	}
	rows, e := parseRoster("\ufeffusername,team,participant_type\r\nabc,\"team, one\",official\r\ndef,group,star\r\n")
	if e != nil || len(rows) != 2 || rows[0]["team"] != "team, one" {
		t.Fatal(rows, e)
	}
}
func TestSpreadsheetExportEscaping(t *testing.T) {
	for _, value := range []string{"=SUM(1,2)", "+1", "-2", "@name", "\tformula", "\rtest"} {
		if !strings.HasPrefix(spreadsheetCell(value), "'") {
			t.Fatal(value)
		}
	}
	if spreadsheetCell("normal") != "normal" {
		t.Fatal("ordinary value changed")
	}
}
