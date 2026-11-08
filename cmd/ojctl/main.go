package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"
	_ "time/tzdata"

	"biue/internal/auth"
	"biue/internal/config"
	"biue/internal/storage"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		slog.Error("command failed", "error", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: ojctl migrate up|version; ojctl admin-init --username NAME (OJ_ADMIN_PASSWORD required)")
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	switch args[0] {
	case "migrate":
		if len(args) != 2 || (args[1] != "up" && args[1] != "version") {
			return errors.New("usage: ojctl migrate up|version")
		}
		version, dirty, err := storage.Migrate(ctx, cfg, args[1])
		if err != nil {
			return err
		}
		fmt.Printf("schema_version=%d dirty=%t\n", version, dirty)
		if dirty {
			return errors.New("schema is dirty; manual inspection required")
		}
	case "admin-init":
		flags := flag.NewFlagSet("admin-init", flag.ContinueOnError)
		username := flags.String("username", "admin", "initial administrator account ID")
		if err := flags.Parse(args[1:]); err != nil {
			return errors.New("usage: ojctl admin-init --username NAME")
		}
		if flags.NArg() != 0 {
			return errors.New("unexpected admin-init arguments")
		}
		password := os.Getenv("OJ_ADMIN_PASSWORD")
		_ = os.Unsetenv("OJ_ADMIN_PASSWORD")
		if password == "" {
			return errors.New("OJ_ADMIN_PASSWORD must be supplied through the environment")
		}
		db, err := storage.OpenMySQL(cfg)
		if err != nil {
			return err
		}
		defer db.Close()
		svc, err := auth.New(db, nil, auth.Config{JWTSecret: cfg.JWTSecret, Issuer: "biue", Audience: "biue-web", SessionTTL: cfg.SessionTTL, BcryptCost: cfg.BcryptCost, Logger: slog.Default()})
		if err != nil {
			return errors.New("cannot initialize authentication")
		}
		created, err := svc.InitAdmin(ctx, *username, password)
		if err != nil {
			return err
		}
		if created {
			fmt.Println("administrator created")
		} else {
			fmt.Println("administrator already initialized; credentials unchanged")
		}
	default:
		return errors.New("unknown command; use migrate or admin-init")
	}
	return nil
}
