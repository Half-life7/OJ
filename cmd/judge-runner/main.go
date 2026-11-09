package main

import (
	"context"
	"crypto/tls"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"biue/internal/config"
	"biue/internal/daemon"
	runner "biue/internal/judge_runner"
	"github.com/gin-gonic/gin"
)

func main() {
	if err := run(); err != nil {
		slog.Error("judge runner stopped", "error", err)
		os.Exit(1)
	}
}
func run() error {
	cfg, err := config.RunnerFrom(os.Getenv)
	if err != nil {
		return err
	}
	if len(os.Args) == 2 && os.Args[1] == "--healthcheck" {
		scheme := "http"
		if cfg.CertFile != "" {
			scheme = "https"
		}
		tlsConfig, err := config.ClientTLS(cfg.CAFile)
		if err != nil {
			return err
		}
		return daemon.ProbeAt(cfg.Addr, scheme, string(cfg.Token), "/engine-ready", &http.Client{Transport: &http.Transport{TLSClientConfig: tlsConfig}})
	}
	gin.SetMode(gin.ReleaseMode)
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	if cfg.CertFile != "" {
		if _, err := tls.LoadX509KeyPair(cfg.CertFile, cfg.KeyFile); err != nil {
			return err
		}
	}
	journal, err := runner.NewJournal(cfg.Directory)
	if err != nil {
		return err
	}
	defer journal.Close()
	engine, err := runner.NewDocker(cfg.Socket, journal.Owner)
	if err != nil {
		return err
	}
	defer engine.Close()
	manager, err := runner.NewManager(journal, engine, cfg.Concurrency)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	slog.Info("judge runner started")
	return daemon.Serve(ctx, cfg.Addr, runner.Handler(manager, string(cfg.Token)), cfg.CertFile, cfg.KeyFile, func(ctx context.Context) error {
		initialized := false
		for ctx.Err() == nil {
			probe, cancel := context.WithTimeout(ctx, 30*time.Second)
			if !initialized {
				initialized = manager.Initialize(probe) == nil
				if !initialized && ctx.Err() == nil {
					slog.Error("runner initialization unavailable")
				}
			} else if manager.Sweep(probe) != nil && ctx.Err() == nil {
				slog.Error("sandbox compensation cleanup failed")
			}
			cancel()
			delay := 30 * time.Second
			if !initialized {
				delay = time.Second
			}
			if !daemon.Pause(ctx, delay) {
				break
			}
		}
		return nil
	})
}
