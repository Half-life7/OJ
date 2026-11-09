// Command dispatcher forwards committed judge outbox intents to Redis.
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
	_ "time/tzdata"

	"biue/internal/config"
	"biue/internal/daemon"
	"biue/internal/judge"
	"biue/internal/storage"
	"github.com/gin-gonic/gin"
)

func main() {
	if err := run(); err != nil {
		slog.Error("dispatcher stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	addr := os.Getenv("DISPATCHER_HEALTH_ADDR")
	if addr == "" {
		addr = "127.0.0.1:8082"
	}
	if len(os.Args) == 2 && os.Args[1] == "--healthcheck" {
		return daemon.Probe(addr, "http", "", &http.Client{})
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	gin.SetMode(gin.ReleaseMode)
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel}))
	slog.SetDefault(logger)
	db, err := storage.OpenMySQL(cfg)
	if err != nil {
		return err
	}
	defer db.Close()
	client := storage.OpenRedis(cfg)
	defer client.Close()
	dispatcher, err := judge.NewDispatcher(db, client)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return daemon.Serve(ctx, addr, daemon.Health(func(ctx context.Context) bool { return storage.Check(ctx, db, client).Ready() }), "", "", func(ctx context.Context) error {
		lastReconcile := time.Time{}
		for ctx.Err() == nil {
			// Refuse dispatch against an outdated or dirty schema, and recover
			// automatically when dependencies return.
			probe, cancel := context.WithTimeout(ctx, 5*time.Second)
			ready := storage.Check(probe, db, client).Ready()
			cancel()
			if ready {
				if time.Since(lastReconcile) >= 30*time.Second {
					for i := 0; i < 32 && ctx.Err() == nil; i++ {
						normal, err := dispatcher.ReconcileOne(ctx, false)
						if err != nil {
							slog.Error("judge reconciliation failed")
							break
						}
						dead, err := dispatcher.ReconcileOne(ctx, true)
						if err != nil {
							slog.Error("dead-letter reconciliation failed")
							break
						}
						if !normal && !dead {
							break
						}
					}
					lastReconcile = time.Now()
				}
				sent, err := dispatcher.DispatchOne(ctx)
				if err != nil && ctx.Err() == nil {
					logger.Error("judge dispatch failed")
				}
				// Give dead letters a turn even while normal work keeps arriving.
				deadSent, deadErr := dispatcher.DispatchDeadOne(ctx)
				if deadErr != nil && ctx.Err() == nil {
					logger.Error("judge dead-letter dispatch failed")
				}
				if err == nil && deadErr == nil && (sent || deadSent) {
					continue
				}
			}
			timer := time.NewTimer(time.Second)
			select {
			case <-ctx.Done():
				timer.Stop()
			case <-timer.C:
			}
		}
		return nil
	})
}
