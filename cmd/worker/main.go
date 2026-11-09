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
	"biue/internal/problemdata"
	"biue/internal/storage"
	"github.com/gin-gonic/gin"
)

func main() {
	if err := run(); err != nil {
		slog.Error("worker stopped", "error", err)
		os.Exit(1)
	}
}
func run() error {
	if len(os.Args) == 2 && os.Args[1] == "--healthcheck" {
		addr := os.Getenv("WORKER_HEALTH_ADDR")
		if addr == "" {
			addr = "127.0.0.1:8081"
		}
		return daemon.Probe(addr, "http", "", &http.Client{})
	}
	base, err := config.Load()
	if err != nil {
		return err
	}
	cfg, err := config.WorkerFrom(os.Getenv, base)
	if err != nil {
		return err
	}
	gin.SetMode(gin.ReleaseMode)
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: base.LogLevel})))
	db, err := storage.OpenMySQL(base)
	if err != nil {
		return err
	}
	defer db.Close()
	redis := storage.OpenRedis(base)
	defer redis.Close()
	data, err := problemdata.New(base.ProblemDataDir)
	if err != nil {
		return err
	}
	defer data.Close()
	queue, err := judge.NewQueue(redis, cfg.Consumer, time.Minute)
	if err != nil {
		return err
	}
	leases, err := judge.NewLeaseStore(db, time.Minute)
	if err != nil {
		return err
	}
	store, err := judge.NewExecutionStore(leases, data)
	if err != nil {
		return err
	}
	consumer, err := judge.NewConsumer(queue, leases)
	if err != nil {
		return err
	}
	tlsConfig, err := config.ClientTLS(cfg.CAFile)
	if err != nil {
		return err
	}
	runner, err := judge.NewRunnerClient(cfg.URL, string(cfg.Token), tlsConfig)
	if err != nil {
		return err
	}
	defer runner.Close()
	worker, err := judge.NewWorker(consumer, store, runner)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ready := func(ctx context.Context) bool {
		return storage.Check(ctx, db, redis).Ready() && runner.BackendReady(ctx) == nil
	}
	slog.Info("worker started", "consumer", cfg.Consumer)
	return daemon.Serve(ctx, cfg.HealthAddr, daemon.Health(ready), "", "", func(ctx context.Context) error {
		for ctx.Err() == nil {
			probe, cancel := context.WithTimeout(ctx, 5*time.Second)
			available := storage.Check(probe, db, redis).Ready()
			cancel()
			if available {
				worked, err := worker.Step(ctx)
				if err != nil && ctx.Err() == nil {
					slog.Error("worker operation failed")
				}
				if worked && err == nil {
					continue
				}
			}
			if !daemon.Pause(ctx, time.Second) {
				break
			}
		}
		return nil
	})
}
