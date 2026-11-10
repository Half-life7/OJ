package community

import (
	"biue/internal/auth"
	"context"
	"errors"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestMarkdownSanitizationAndCodeLanguage(t *testing.T) {
	cases := []string{`<script>alert(1)</script>`, `<img src=x onerror=alert(1)>`, `[x](javascript:alert(1))`, `[x](data:text/html;base64,PHNjcmlwdD4=)`, `<svg onload=alert(1)>x</svg>`}
	for _, body := range cases {
		html, e := Render(body)
		if e != nil {
			t.Fatal(e)
		}
		for _, bad := range []string{"<script", "onerror=", "javascript:", "data:text", "<svg"} {
			if strings.Contains(strings.ToLower(html), bad) {
				t.Fatalf("unsafe output %q", html)
			}
		}
	}
	html, e := Render("# Title\n\n**bold**\n\n```go\nfmt.Println(\"<script>\")\n```\n")
	if e != nil || !strings.Contains(html, "<h1>Title</h1>") || !strings.Contains(html, `class="language-go"`) || !strings.Contains(html, "&lt;script&gt;") {
		t.Fatal(html, e)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, e := Render("**shared policy**"); e != nil {
				t.Error(e)
			}
		}()
	}
	wg.Wait()
}
func TestInputContextAndOrdering(t *testing.T) {
	p, c := uint64(1), uint64(2)
	for _, in := range []Input{
		{Kind: "list", Title: "L", Visibility: "public", Problems: []uint64{1, 1}},
		{Kind: "discussion", Title: "D", Visibility: "public", Body: "Hi", ProblemID: &p, ContestID: &c},
		{Kind: "solution", Title: "S", Visibility: "public", Body: "Hi"},
		{Kind: "list", Title: "L", Visibility: "public", Body: strings.Repeat("x", 65537)},
	} {
		if valid(in) != ErrInvalid {
			t.Fatalf("accepted invalid input: %+v", in)
		}
	}
	if e := valid(Input{Kind: "list", Title: "L", Visibility: "private", Problems: []uint64{3, 1, 2}}); e != nil {
		t.Fatal(e)
	}
}
func TestThreeBoardsAndStableTies(t *testing.T) {
	for _, kind := range []string{"practice", "contest", "solutions"} {
		q, e := boardQuery(kind)
		if e != nil || !strings.Contains(q, "person_id") || !strings.Contains(q, "permanent") {
			t.Fatal(kind, e)
		}
	}
	if _, e := boardQuery("other"); e != ErrInvalid {
		t.Fatal(e)
	}
	now := time.Now()
	rows := []BoardRow{{PersonID: 1, Value: 4, Reached: now}, {PersonID: 2, Value: 4, Reached: now}, {PersonID: 3, Value: 3, Reached: now}}
	assignRanks(rows)
	if rows[0].Rank != 1 || rows[1].Rank != 1 || rows[2].Rank != 3 {
		t.Fatal(rows)
	}
}
func TestWriteRechecksAccountAndRollsBack(t *testing.T) {
	for _, kind := range []string{"contest", "permanent"} {
		t.Run(kind, func(t *testing.T) {
			db, m, e := sqlmock.New()
			if e != nil {
				t.Fatal(e)
			}
			defer db.Close()
			s := New(sqlx.NewDb(db, "sqlmock"))
			m.ExpectBegin()
			m.ExpectQuery("SELECT id FROM judge_admission").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1))
			m.ExpectQuery("SELECT id,username,account_type,global_role,status FROM users").WithArgs(uint64(1)).WillReturnRows(sqlmock.NewRows([]string{"id", "username", "account_type", "global_role", "status"}).AddRow(1, "test", kind, "user", "active"))
			want := ErrForbidden
			if kind == "permanent" {
				m.ExpectExec("INSERT INTO community_write_limits").WillReturnResult(sqlmock.NewResult(0, 1))
				m.ExpectQuery("SELECT window_start,writes").WillReturnRows(sqlmock.NewRows([]string{"window_start", "writes"}).AddRow(time.Now(), 0))
				want = ErrUnavailable
			}
			m.ExpectRollback()
			called := false
			e = s.write(context.Background(), auth.User{ID: 1, AccountType: "permanent"}, func(*sqlx.Tx, auth.User) error { called = true; return errors.New("write failure") })
			if e != want || called != (kind == "permanent") {
				t.Fatal(e, called)
			}
			if e = m.ExpectationsWereMet(); e != nil {
				t.Fatal(e)
			}
		})
	}
}
