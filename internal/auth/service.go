// Package auth implements permanent-account authentication and durable sessions.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/go-sql-driver/mysql"
	"github.com/golang-jwt/jwt/v5"
	"github.com/jmoiron/sqlx"
	"github.com/redis/go-redis/v9"
	"golang.org/x/crypto/bcrypt"
)

const CookieName = "biue_session"

var (
	ErrInvalid      = errors.New("invalid request")
	ErrCredentials  = errors.New("invalid credentials")
	ErrConflict     = errors.New("account or identity unavailable")
	ErrUnavailable  = errors.New("service unavailable")
	ErrRateLimited  = errors.New("rate limited")
	usernamePattern = regexp.MustCompile(`^[a-z0-9_]{3,32}$`)
)

type Config struct {
	JWTSecret    []byte
	Issuer       string
	Audience     string
	SessionTTL   time.Duration
	BcryptCost   int
	CookieSecure bool
	Logger       *slog.Logger
}

type Service struct {
	db        *sqlx.DB
	redis     *redis.Client
	cfg       Config
	logger    *slog.Logger
	now       func() time.Time
	dummyHash []byte
}

type User struct {
	ID              uint64  `json:"id" db:"id"`
	Username        string  `json:"username" db:"username"`
	AccountType     string  `json:"account_type" db:"account_type"`
	GlobalRole      string  `json:"global_role" db:"global_role"`
	Status          string  `json:"status" db:"status"`
	DisplayName     string  `json:"display_name" db:"display_name"`
	StudentNo       *string `json:"student_no" db:"-"`
	StudentNoStatus string  `json:"student_no_status" db:"student_no_status"`
}

type account struct {
	User
	PasswordHash string         `db:"password_hash"`
	TokenVersion uint64         `db:"token_version"`
	Student      sql.NullString `db:"student_no"`
}

type claims struct {
	Version uint64 `json:"ver"`
	jwt.RegisteredClaims
}

const accountColumns = `u.id, u.username, u.password_hash, u.account_type, u.global_role, u.status, u.token_version, p.display_name, p.student_no, p.student_no_status`
const accountJoins = ` FROM users u JOIN person_accounts pa ON pa.user_id = u.id JOIN persons p ON p.id = pa.person_id`

func New(db *sqlx.DB, client *redis.Client, cfg Config) (*Service, error) {
	if db == nil || len(cfg.JWTSecret) < 32 || cfg.Issuer == "" || cfg.Audience == "" {
		return nil, errors.New("auth requires a database, 32-byte signing key, issuer and audience")
	}
	if cfg.SessionTTL == 0 {
		cfg.SessionTTL = 12 * time.Hour
	}
	if cfg.SessionTTL < time.Minute || cfg.SessionTTL > 7*24*time.Hour {
		return nil, errors.New("session TTL must be between one minute and seven days")
	}
	if cfg.BcryptCost == 0 {
		cfg.BcryptCost = 12
	}
	if cfg.BcryptCost < bcrypt.MinCost || cfg.BcryptCost > 16 {
		return nil, errors.New("bcrypt cost must be between 4 and 16")
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	dummy, err := bcrypt.GenerateFromPassword([]byte("dummy-password-never-used-for-auth"), cfg.BcryptCost)
	if err != nil {
		return nil, err
	}
	cfg.JWTSecret = append([]byte(nil), cfg.JWTSecret...)
	return &Service{db: db, redis: client, cfg: cfg, logger: cfg.Logger, now: time.Now, dummyHash: dummy}, nil
}

func normalizeUsername(value string, allowReserved bool) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if !usernamePattern.MatchString(value) {
		return "", ErrInvalid
	}
	if !allowReserved {
		switch value {
		case "admin", "administrator", "root", "system", "superadmin", "super_admin", "api", "support", "moderator", "null", "undefined":
			return "", ErrInvalid
		}
	}
	return value, nil
}

func normalizeStudent(value string) (*string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	if !utf8.ValidString(value) || utf8.RuneCountInString(value) > 64 || strings.IndexFunc(value, unicode.IsControl) >= 0 {
		return nil, ErrInvalid
	}
	return &value, nil
}

func validPassword(value string) bool {
	return utf8.ValidString(value) && len(value) >= 8 && len(value) <= 72
}

func duplicate(err error) bool {
	var me *mysql.MySQLError
	return errors.As(err, &me) && me.Number == 1062
}

func (s *Service) internal(op string, err error) error {
	// Do not print driver errors: they can include SQL values or credentials.
	s.logger.Error("authentication storage operation failed", "operation", op, "error_type", fmt.Sprintf("%T", err))
	return ErrUnavailable
}

func (s *Service) Register(ctx context.Context, username, password, confirmation, student string, requestID string) (User, error) {
	name, err := normalizeUsername(username, false)
	if err != nil || !validPassword(password) || password != confirmation {
		return User{}, ErrInvalid
	}
	studentNo, err := normalizeStudent(student)
	if err != nil {
		return User{}, err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), s.cfg.BcryptCost)
	if err != nil {
		return User{}, s.internal("hash_password", err)
	}
	u, err := s.createAccount(ctx, name, string(hash), studentNo, "user", requestID)
	if duplicate(err) {
		return User{}, ErrConflict
	}
	if err != nil {
		return User{}, s.internal("register", err)
	}
	return u, nil
}

func (s *Service) createAccount(ctx context.Context, name, hash string, student *string, role, requestID string) (User, error) {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return User{}, err
	}
	defer tx.Rollback()
	now := s.now().UTC()
	studentStatus := "none"
	if student != nil {
		studentStatus = "pending"
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO persons (display_name, student_no, student_no_status, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`, name, student, studentStatus, now, now)
	if err != nil {
		return User{}, err
	}
	personID, err := result.LastInsertId()
	if err != nil {
		return User{}, err
	}
	result, err = tx.ExecContext(ctx, `INSERT INTO users (username, password_hash, account_type, global_role, status, token_version, created_at, updated_at) VALUES (?, ?, 'permanent', ?, 'active', 1, ?, ?)`, name, hash, role, now, now)
	if err != nil {
		return User{}, err
	}
	userID, err := result.LastInsertId()
	if err != nil {
		return User{}, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO person_accounts (user_id, person_id, created_at) VALUES (?, ?, ?)`, userID, personID, now); err != nil {
		return User{}, err
	}
	if student != nil {
		if _, err = tx.ExecContext(ctx, `INSERT INTO student_no_history (person_id, old_student_no, new_student_no, old_status, new_status, actor_user_id, reason, created_at) VALUES (?, NULL, ?, 'none', 'pending', ?, 'registration', ?)`, personID, *student, userID, now); err != nil {
			return User{}, err
		}
	}
	action := "account.register"
	if role == "super_admin" {
		action = "account.admin_init"
	}
	if err = writeAudit(ctx, tx, uint64(userID), action, requestID, now); err != nil {
		return User{}, err
	}
	if err = tx.Commit(); err != nil {
		return User{}, err
	}
	return User{ID: uint64(userID), Username: name, AccountType: "permanent", GlobalRole: role, Status: "active", DisplayName: name, StudentNo: student, StudentNoStatus: studentStatus}, nil
}

func writeAudit(ctx context.Context, tx *sqlx.Tx, userID uint64, action, requestID string, now time.Time) error {
	if len(requestID) > 128 {
		requestID = requestID[:128]
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO audit_logs (actor_user_id, action, object_type, object_id, request_id, details, created_at) VALUES (?, ?, 'user', ?, ?, '{}', ?)`, userID, action, userID, requestID, now)
	return err
}

func (s *Service) InitAdmin(ctx context.Context, username, password string) (bool, error) {
	name, err := normalizeUsername(username, true)
	if err != nil || !validPassword(password) {
		return false, ErrInvalid
	}
	var existing struct {
		Role   string `db:"global_role"`
		Status string `db:"status"`
	}
	err = s.db.GetContext(ctx, &existing, `SELECT global_role, status FROM users WHERE username = ?`, name)
	if err == nil {
		if existing.Role == "super_admin" {
			return false, nil
		}
		return false, ErrConflict
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return false, s.internal("admin_lookup", err)
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), s.cfg.BcryptCost)
	if err != nil {
		return false, err
	}
	_, err = s.createAccount(ctx, name, string(hash), nil, "super_admin", "admin-init")
	if duplicate(err) {
		return false, ErrConflict
	}
	if err != nil {
		return false, s.internal("admin_create", err)
	}
	return true, nil
}

func populateUser(a *account) {
	if a.Student.Valid {
		v := a.Student.String
		a.StudentNo = &v
	}
}

func (s *Service) Login(ctx context.Context, username, password, requestID string) (string, User, error) {
	name, err := normalizeUsername(username, true)
	if err != nil || !validPassword(password) {
		return "", User{}, ErrCredentials
	}
	var a account
	err = s.db.GetContext(ctx, &a, `SELECT `+accountColumns+accountJoins+` WHERE u.username = ?`, name)
	if errors.Is(err, sql.ErrNoRows) {
		_ = bcrypt.CompareHashAndPassword(s.dummyHash, []byte(password))
		return "", User{}, ErrCredentials
	}
	if err != nil {
		return "", User{}, s.internal("login_lookup", err)
	}
	if bcrypt.CompareHashAndPassword([]byte(a.PasswordHash), []byte(password)) != nil || a.Status != "active" {
		return "", User{}, ErrCredentials
	}
	now := s.now().UTC()
	jtiBytes := make([]byte, 32)
	if _, err = rand.Read(jtiBytes); err != nil {
		return "", User{}, s.internal("session_random", err)
	}
	jti := base64.RawURLEncoding.EncodeToString(jtiBytes)
	expires := now.Add(s.cfg.SessionTTL)
	c := claims{Version: a.TokenVersion, RegisteredClaims: jwt.RegisteredClaims{Issuer: s.cfg.Issuer, Audience: jwt.ClaimStrings{s.cfg.Audience}, Subject: strconv.FormatUint(a.ID, 10), ID: jti, IssuedAt: jwt.NewNumericDate(now), NotBefore: jwt.NewNumericDate(now), ExpiresAt: jwt.NewNumericDate(expires)}}
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, c).SignedString(s.cfg.JWTSecret)
	if err != nil {
		return "", User{}, s.internal("session_sign", err)
	}
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return "", User{}, s.internal("session_begin", err)
	}
	defer tx.Rollback()
	// Lock the account so a concurrent password change cannot issue a usable old-version session.
	var current struct {
		Version uint64 `db:"token_version"`
		Status  string `db:"status"`
	}
	if err = tx.GetContext(ctx, &current, `SELECT token_version, status FROM users WHERE id = ? FOR UPDATE`, a.ID); err != nil {
		return "", User{}, s.internal("session_lock", err)
	}
	if current.Status != "active" || current.Version != a.TokenVersion {
		return "", User{}, ErrCredentials
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO auth_sessions (jti_hash, user_id, token_version, expires_at, created_at) VALUES (?, ?, ?, ?, ?)`, hashJTI(jti), a.ID, a.TokenVersion, expires, now); err != nil {
		return "", User{}, s.internal("session_insert", err)
	}
	if err = writeAudit(ctx, tx, a.ID, "account.login", requestID, now); err != nil {
		return "", User{}, s.internal("login_audit", err)
	}
	if err = tx.Commit(); err != nil {
		return "", User{}, s.internal("session_commit", err)
	}
	populateUser(&a)
	return token, a.User, nil
}

func hashJTI(jti string) string {
	digest := sha256.Sum256([]byte(jti))
	return hex.EncodeToString(digest[:])
}

func (s *Service) parseToken(raw string) (*claims, error) {
	c := &claims{}
	token, err := jwt.ParseWithClaims(raw, c, func(t *jwt.Token) (any, error) { return s.cfg.JWTSecret, nil }, jwt.WithValidMethods([]string{"HS256"}), jwt.WithIssuer(s.cfg.Issuer), jwt.WithAudience(s.cfg.Audience), jwt.WithExpirationRequired(), jwt.WithIssuedAt(), jwt.WithTimeFunc(s.now))
	if err != nil || !token.Valid || c.ID == "" || c.Version == 0 || c.IssuedAt == nil || c.NotBefore == nil {
		return nil, ErrCredentials
	}
	if id, err := strconv.ParseUint(c.Subject, 10, 64); err != nil || id == 0 {
		return nil, ErrCredentials
	}
	return c, nil
}

func (s *Service) authenticate(ctx context.Context, raw string) (account, *claims, error) {
	c, err := s.parseToken(raw)
	if err != nil {
		return account{}, nil, err
	}
	var a account
	query := `SELECT ` + accountColumns + accountJoins + ` JOIN auth_sessions sess ON sess.user_id = u.id WHERE u.id = ? AND sess.jti_hash = ? AND sess.revoked_at IS NULL AND sess.expires_at > ? AND sess.token_version = u.token_version`
	err = s.db.GetContext(ctx, &a, query, c.Subject, hashJTI(c.ID), s.now().UTC())
	if errors.Is(err, sql.ErrNoRows) {
		return account{}, nil, ErrCredentials
	}
	if err != nil {
		return account{}, nil, s.internal("authenticate", err)
	}
	if a.Status != "active" || a.TokenVersion != c.Version {
		return account{}, nil, ErrCredentials
	}
	populateUser(&a)
	return a, c, nil
}

func (s *Service) CurrentUser(ctx context.Context, raw string) (User, error) {
	a, _, err := s.authenticate(ctx, raw)
	return a.User, err
}

func (s *Service) Logout(ctx context.Context, raw, requestID string) error {
	c, err := s.parseToken(raw)
	if err != nil {
		return nil
	} // Missing, expired or malformed credentials are already logged out.
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return s.internal("logout_begin", err)
	}
	defer tx.Rollback()
	var userID uint64
	err = tx.GetContext(ctx, &userID, `SELECT id FROM users WHERE id = ? FOR UPDATE`, c.Subject)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return s.internal("logout_lock", err)
	}
	now := s.now().UTC()
	result, err := tx.ExecContext(ctx, `UPDATE auth_sessions SET revoked_at = ? WHERE jti_hash = ? AND user_id = ? AND revoked_at IS NULL`, now, hashJTI(c.ID), c.Subject)
	if err != nil {
		return s.internal("logout_revoke", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return s.internal("logout_count", err)
	}
	if count > 0 {
		id, _ := strconv.ParseUint(c.Subject, 10, 64)
		if err = writeAudit(ctx, tx, id, "account.logout", requestID, now); err != nil {
			return s.internal("logout_audit", err)
		}
	}
	if err = tx.Commit(); err != nil {
		return s.internal("logout_commit", err)
	}
	return nil
}

func (s *Service) ChangePassword(ctx context.Context, raw, current, password, confirmation, requestID string) error {
	if !validPassword(password) || password != confirmation || !validPassword(current) {
		return ErrInvalid
	}
	a, c, err := s.authenticate(ctx, raw)
	if err != nil {
		return err
	}
	if bcrypt.CompareHashAndPassword([]byte(a.PasswordHash), []byte(current)) != nil {
		return ErrCredentials
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), s.cfg.BcryptCost)
	if err != nil {
		return s.internal("password_hash", err)
	}
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return s.internal("password_begin", err)
	}
	defer tx.Rollback()
	// Lock ordering matches Login: user first, then sessions. Recheck the current session under lock.
	var locked struct {
		Version uint64 `db:"token_version"`
		Status  string `db:"status"`
	}
	if err = tx.GetContext(ctx, &locked, `SELECT token_version, status FROM users WHERE id = ? FOR UPDATE`, a.ID); err != nil {
		return s.internal("password_lock", err)
	}
	if locked.Status != "active" || locked.Version != c.Version {
		return ErrCredentials
	}
	var sessionID uint64
	err = tx.GetContext(ctx, &sessionID, `SELECT id FROM auth_sessions WHERE user_id = ? AND jti_hash = ? AND revoked_at IS NULL AND expires_at > ? FOR UPDATE`, a.ID, hashJTI(c.ID), s.now().UTC())
	if errors.Is(err, sql.ErrNoRows) {
		return ErrCredentials
	}
	if err != nil {
		return s.internal("password_session", err)
	}
	now := s.now().UTC()
	if _, err = tx.ExecContext(ctx, `UPDATE users SET password_hash = ?, token_version = token_version + 1, updated_at = ? WHERE id = ?`, string(hash), now, a.ID); err != nil {
		return s.internal("password_update", err)
	}
	if _, err = tx.ExecContext(ctx, `UPDATE auth_sessions SET revoked_at = ? WHERE user_id = ? AND revoked_at IS NULL`, now, a.ID); err != nil {
		return s.internal("password_revoke", err)
	}
	if err = writeAudit(ctx, tx, a.ID, "account.password_change", requestID, now); err != nil {
		return s.internal("password_audit", err)
	}
	if err = tx.Commit(); err != nil {
		return s.internal("password_commit", err)
	}
	return nil
}
