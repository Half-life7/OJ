package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
	_ "time/tzdata"

	"biue/internal/auth"
	"biue/internal/community"
	"biue/internal/competition"
	"biue/internal/config"
	"biue/internal/operations"
	"biue/internal/problem"
	"biue/internal/problemdata"
	"biue/internal/server"
	"biue/internal/storage"
	"biue/internal/submission"
	"biue/webui"

	"github.com/gin-gonic/gin"
)

func main() {
	if err := run(); err != nil {
		slog.Error("web stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	// Application logging is configured below; disable Gin's debug route output.
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
	svc, err := auth.New(db, client, auth.Config{
		JWTSecret: cfg.JWTSecret, Issuer: "biue", Audience: "biue-web", SessionTTL: cfg.SessionTTL,
		BcryptCost: cfg.BcryptCost, CookieSecure: strings.HasPrefix(cfg.PublicURL, "https://"), Logger: logger,
	})
	if err != nil {
		return errors.New("cannot initialize authentication")
	}
	datasets, err := problemdata.New(cfg.ProblemDataDir)
	if err != nil {
		return errors.New("cannot initialize problem data storage")
	}
	defer datasets.Close()
	problems, err := problem.New(db, datasets, logger)
	if err != nil {
		return errors.New("cannot initialize problems")
	}
	problemHTTP, err := problem.NewHandler(problems, svc, cfg.ProblemImportConcurrency)
	if err != nil {
		return errors.New("cannot initialize problem HTTP handlers")
	}
	problemHTTP.SetSampleDirectory(cfg.ProblemSampleDir)
	submissions, err := submission.New(db, submission.Config{QueueCapacity: cfg.JudgeQueueCapacity}, logger)
	if err != nil {
		return errors.New("cannot initialize submissions")
	}
	submissionHTTP, err := submission.NewHandler(submissions, svc, client)
	if err != nil {
		return errors.New("cannot initialize submission HTTP handlers")
	}
	ui, err := webui.Handler()
	if err != nil {
		return errors.New("cannot initialize templates")
	}
	contests := competition.New(db)
	handler := server.New(cfg, server.Dependencies{
		Ready: func(ctx context.Context) storage.Readiness { return storage.Check(ctx, db, client) },
		RegisterAPI: func(r *gin.RouterGroup) {
			svc.RegisterRoutes(r)
			problemHTTP.RegisterRoutes(r)
			submissionHTTP.RegisterRoutes(r)
			competition.NewHandler(contests, svc).RegisterRoutes(r)
			community.NewHandler(community.New(db), svc).RegisterRoutes(r)
			operations.NewHandler(operations.New(db, client, datasets, problems, contests, submissions, cfg.JudgeQueueCapacity), svc).RegisterRoutes(r)
		}, UI: ui, Logger: logger,
	})
	srv := &http.Server{
		Addr: ":" + strconv.Itoa(cfg.Port), Handler: handler, ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout: 15 * time.Second, WriteTimeout: 60 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 1 << 20,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	result := make(chan error, 1)
	go func() { result <- srv.ListenAndServe() }()
	logger.Info("web listening", "port", cfg.Port, "environment", cfg.Env)
	select {
	case err := <-result:
		if !errors.Is(err, http.ErrServerClosed) {
			return errors.New("HTTP listener failed")
		}
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdown); err != nil {
			_ = srv.Close()
			return errors.New("HTTP shutdown timed out")
		}
		if err := <-result; !errors.Is(err, http.ErrServerClosed) {
			return errors.New("HTTP listener failed")
		}
	}
	return nil
}
