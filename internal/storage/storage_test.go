package storage

import (
	"context"
	"testing"
	"time"

	"biue/internal/config"
	"biue/migrations"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/alicebob/miniredis/v2"
	driver "github.com/go-sql-driver/mysql"
	"github.com/jmoiron/sqlx"
	"github.com/redis/go-redis/v9"
)

func TestDSNCredentialsAndMigrationIsolation(t *testing.T) {
	cfg := config.Config{MySQLHost: "127.0.0.1", MySQLPort: 3306, MySQLDatabase: "biue", MySQLUser: "oj", MySQLPassword: "a:b@c/?&password"}
	for _, migration := range []bool{false, true} {
		dsn, err := driver.ParseDSN(DSN(cfg, migration))
		if err != nil {
			t.Fatal(err)
		}
		if dsn.Passwd != cfg.MySQLPassword || dsn.MultiStatements != migration || !dsn.ParseTime || dsn.Loc != time.UTC {
			t.Fatal("DSN changed credentials or connection policy")
		}
	}
}

func TestDSNIPv6Host(t *testing.T) {
	cfg := config.Config{MySQLHost: "::1", MySQLPort: 3306, MySQLDatabase: "biue", MySQLUser: "oj", MySQLPassword: "not-logged"}
	dsn, err := driver.ParseDSN(DSN(cfg, false))
	if err != nil {
		t.Fatal(err)
	}
	if dsn.Addr != "[::1]:3306" {
		t.Fatalf("unexpected IPv6 address: %s", dsn.Addr)
	}
}

func TestReadinessSchemaAndRecovery(t *testing.T) {
	for _, tc := range []struct {
		name    string
		version uint
		dirty   bool
		ready   bool
	}{
		{"current", migrations.ExpectedVersion, false, true},
		{"dirty", migrations.ExpectedVersion, true, false},
		{"old", 0, false, false},
		{"future", migrations.ExpectedVersion + 1, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			mock.ExpectPing()
			mock.ExpectQuery("SELECT version, dirty FROM schema_migrations LIMIT 1").WillReturnRows(sqlmock.NewRows([]string{"version", "dirty"}).AddRow(tc.version, tc.dirty))
			redisServer := miniredis.RunT(t)
			client := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
			defer client.Close()
			got := Check(context.Background(), sqlx.NewDb(db, "mysql"), client)
			if got.Ready() != tc.ready || !got.MySQL || !got.Redis {
				t.Fatalf("unexpected readiness: %+v", got)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestReadinessRejectsMissingSchema(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mock.ExpectPing()
	mock.ExpectQuery("SELECT version, dirty FROM schema_migrations LIMIT 1").WillReturnRows(sqlmock.NewRows([]string{"version", "dirty"}))
	s := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: s.Addr()})
	defer client.Close()
	if got := Check(context.Background(), sqlx.NewDb(db, "mysql"), client); got.Ready() || got.Schema {
		t.Fatalf("empty database ready: %+v", got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
