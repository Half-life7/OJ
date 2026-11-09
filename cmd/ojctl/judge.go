package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"os"

	"biue/internal/config"
	"biue/internal/judge"
	"biue/internal/storage"
)

func judgeCommand(ctx context.Context, cfg config.Config, args []string) error {
	if len(args) < 1 || (args[0] != "dead-list" && args[0] != "retry") {
		return errors.New("usage: ojctl judge dead-list|retry --actor-user-id ID")
	}
	flags := flag.NewFlagSet("judge", flag.ContinueOnError)
	actor := flags.Uint64("actor-user-id", 0, "active super administrator ID")
	limit := flags.Int("limit", 20, "dead-list limit")
	id := flags.Uint64("dead-letter-id", 0, "dead letter ID")
	generation := flags.Uint64("generation", 0, "expected current generation")
	if flags.Parse(args[1:]) != nil || flags.NArg() != 0 || *actor == 0 {
		return errors.New("invalid judge command arguments")
	}
	db, err := storage.OpenMySQL(cfg)
	if err != nil {
		return err
	}
	defer db.Close()
	admin, err := judge.NewJudgeAdmin(db, cfg.JudgeQueueCapacity)
	if err != nil {
		return err
	}
	if args[0] == "dead-list" {
		items, err := admin.ListDead(ctx, *actor, *limit)
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(items)
	}
	task, err := admin.RetryDead(ctx, *actor, *id, *generation)
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(task)
}
