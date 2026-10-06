package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/tvaroska/podcaster/internal/app"
	"github.com/tvaroska/podcaster/internal/config"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(log)

	id := os.Getenv("EPISODE_ID")
	if id == "" {
		log.Error("EPISODE_ID is required")
		os.Exit(2)
	}

	cfg, err := config.FromEnv()
	if err != nil {
		log.Error("config", "err", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if cfg.WorkerTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, cfg.WorkerTimeout)
		defer cancel()
	}

	application, err := app.OpenWithOptions(ctx, cfg, log, app.Options{DisableDispatcher: true})
	if err != nil {
		log.Error("bootstrap", "err", err)
		os.Exit(1)
	}
	defer application.Close()

	log.Info("processing episode", "episode_id", id)
	if err := application.Worker.Process(ctx, id); err != nil {
		log.Error("process failed", "episode_id", id, "err", err)
		os.Exit(1)
	}
}
