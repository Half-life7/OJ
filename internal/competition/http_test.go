package competition

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"biue/internal/auth"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/gin-gonic/gin"
)

type formAuthenticator struct{}

func (formAuthenticator) CurrentUser(context.Context, string) (auth.User, error) {
	return auth.User{ID: 1, AccountType: "permanent", GlobalRole: "super_admin", Status: "active"}, nil
}

func TestContestFormCreateAndFieldErrors(t *testing.T) {
	for _, mode := range []string{"OI", "ACM", "IOI"} {
		for _, invalid := range []bool{false, true} {
			name := mode
			if invalid {
				name += "/current-minute"
			}
			t.Run(name, func(t *testing.T) {
				service, mock := fixture(t)
				start := service.now().Add(time.Hour)
				if invalid {
					start = service.now()
				}
				input := Contest{Title: "Form contest", Mode: mode, Policy: "open", Start: start, End: start.Add(2 * time.Hour)}
				body, e := json.Marshal(input)
				if e != nil {
					t.Fatal(e)
				}
				begin(mock, "super_admin")
				if invalid {
					mock.ExpectRollback()
				} else {
					mock.ExpectExec("INSERT INTO contests").WithArgs(input.Title, mode, "open", false, false, input.Start.UTC(), input.End.UTC(), uint64(1), service.now()).WillReturnResult(sqlmock.NewResult(55, 1))
					mock.ExpectExec("INSERT INTO audit_logs").WillReturnResult(sqlmock.NewResult(1, 1))
					mock.ExpectCommit()
				}
				router := gin.New()
				NewHandler(service, formAuthenticator{}).RegisterRoutes(router.Group(""))
				request := httptest.NewRequest(http.MethodPost, "/api/contests", bytes.NewReader(body))
				request.Header.Set("Content-Type", "application/json")
				request.AddCookie(&http.Cookie{Name: auth.CookieName, Value: "test-session"})
				response := httptest.NewRecorder()
				router.ServeHTTP(response, request)
				var result struct {
					Data struct {
						ID     uint64            `json:"id"`
						Fields map[string]string `json:"fields"`
					} `json:"data"`
				}
				if e = json.Unmarshal(response.Body.Bytes(), &result); e != nil {
					t.Fatal(e)
				}
				if invalid {
					if response.Code != 400 || result.Data.Fields["starts_at"] == "" {
						t.Fatalf("missing field error: %d %s", response.Code, response.Body)
					}
				} else if response.Code != 200 || result.Data.ID != 55 {
					t.Fatalf("create failed: %d %s", response.Code, response.Body)
				}
			})
		}
	}
}

func TestContestDurationLimits(t *testing.T) {
	now := time.Date(2026, 11, 11, 0, 0, 0, 0, time.UTC)
	valid := Contest{Title: "Contest", Mode: "OI", Policy: "open", Start: now.Add(time.Hour), End: now.Add(time.Hour + 30*24*time.Hour)}
	if err := validateContest(valid, now); err != nil {
		t.Fatal(err)
	}
	for _, end := range []time.Time{valid.Start, valid.Start.Add(-time.Minute), valid.End.Add(time.Second)} {
		invalid := valid
		invalid.End = end
		err := validateContest(invalid, now)
		if err == nil {
			t.Fatal("invalid duration accepted")
		}
		fields, ok := err.(*FieldError)
		if !ok || fields.Fields["ends_at"] == "" {
			t.Fatal("missing ends_at error", err)
		}
	}
}
