package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"time"

	"biue/internal/config"
	"biue/migrations"

	driver "github.com/go-sql-driver/mysql"
	"github.com/golang-migrate/migrate/v4"
	migratemysql "github.com/golang-migrate/migrate/v4/database/mysql"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jmoiron/sqlx"
	"github.com/redis/go-redis/v9"
)

// DSN uses the driver's encoder so punctuation in credentials cannot alter options.
func DSN(cfg config.Config, migration bool) string {
	d := driver.NewConfig()
	d.User, d.Passwd = cfg.MySQLUser, cfg.MySQLPassword
	d.Net = "tcp"
	d.Addr = net.JoinHostPort(cfg.MySQLHost, fmt.Sprint(cfg.MySQLPort))
	d.DBName = cfg.MySQLDatabase
	d.ParseTime, d.Loc = true, time.UTC
	d.Timeout, d.ReadTimeout, d.WriteTimeout = 3*time.Second, 5*time.Second, 5*time.Second
	d.MultiStatements = migration
	d.Collation = "utf8mb4_unicode_ci"
	d.Params = map[string]string{"time_zone": "'+00:00'"}
	return d.FormatDSN()
}

// OpenMySQL deliberately does not ping: the Web must serve health endpoints
// while dependencies are unavailable, and reconnect when they return.
func OpenMySQL(cfg config.Config) (*sqlx.DB, error) {
	db, err := sqlx.Open("mysql", DSN(cfg, false))
	if err != nil {
		return nil, errors.New("cannot configure MySQL client")
	}
	db.SetMaxOpenConns(20)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(3 * time.Minute)
	db.SetConnMaxIdleTime(time.Minute)
	return db, nil
}

func OpenRedis(cfg config.Config) *redis.Client {
	return redis.NewClient(&redis.Options{
		Addr: cfg.RedisAddr, Password: cfg.RedisPassword, DB: cfg.RedisDB,
		DialTimeout: 2 * time.Second, ReadTimeout: 2 * time.Second,
		WriteTimeout: 2 * time.Second, PoolTimeout: 2 * time.Second,
		ContextTimeoutEnabled: true, MaxRetries: -1,
	})
}

type Readiness struct {
	MySQL  bool `json:"mysql"`
	Redis  bool `json:"redis"`
	Schema bool `json:"schema"`
}

func (r Readiness) Ready() bool { return r.MySQL && r.Redis && r.Schema }

func Check(ctx context.Context, db *sqlx.DB, client *redis.Client) Readiness {
	var result Readiness
	if err := db.PingContext(ctx); err == nil {
		result.MySQL = true
		var row struct {
			Version uint `db:"version"`
			Dirty   bool `db:"dirty"`
		}
		if err := db.GetContext(ctx, &row, "SELECT version, dirty FROM schema_migrations LIMIT 1"); err == nil {
			result.Schema = !row.Dirty && row.Version == migrations.ExpectedVersion
		}
	}
	result.Redis = client.Ping(ctx).Err() == nil
	return result
}

// Migrate owns a separate connection; closing it never closes the Web's pool.
// No automatic Force or down action is exposed.
func Migrate(ctx context.Context, cfg config.Config, action string) (uint, bool, error) {
	db, err := sql.Open("mysql", DSN(cfg, true))
	if err != nil {
		return 0, false, errors.New("cannot configure migration connection")
	}
	defer db.Close()
	if err := db.PingContext(ctx); err != nil {
		return 0, false, errors.New("migration database unavailable")
	}
	drv, err := migratemysql.WithInstance(db, &migratemysql.Config{
		DatabaseName: cfg.MySQLDatabase, StatementTimeout: 30 * time.Second,
	})
	if err != nil {
		return 0, false, errors.New("cannot initialize migration driver")
	}
	source, err := iofs.New(migrations.Files, ".")
	if err != nil {
		_ = drv.Close()
		return 0, false, errors.New("cannot open embedded migrations")
	}
	m, err := migrate.NewWithInstance("iofs", source, cfg.MySQLDatabase, drv)
	if err != nil {
		_ = source.Close()
		_ = drv.Close()
		return 0, false, errors.New("cannot initialize migrations")
	}
	defer m.Close()
	m.LockTimeout = 10 * time.Second
	if action == "up" {
		if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
			// Do not include driver errors: they may contain SQL parameters.
			return 0, false, errors.New("migration failed; inspect schema_migrations dirty state before retrying")
		}
	} else if action != "version" {
		return 0, false, errors.New("migration action must be up or version")
	}
	v, dirty, err := m.Version()
	if errors.Is(err, migrate.ErrNilVersion) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, errors.New("cannot read migration version")
	}
	return v, dirty, nil
}
