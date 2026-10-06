package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/tvaroska/podcaster/internal/api"
	"github.com/tvaroska/podcaster/internal/app"
	"github.com/tvaroska/podcaster/internal/config"
	"github.com/tvaroska/podcaster/internal/cover"
	mcpserver "github.com/tvaroska/podcaster/internal/mcp"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(log)

	cfg, err := config.FromEnv()
	if err != nil {
		log.Error("config", "err", err)
		os.Exit(1)
	}
	if cfg.DevDefaults {
		log.Warn("running with development defaults; set AGENT_API_KEY, FEED_USERNAME, and FEED_PASSWORD for production")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	application, err := app.Open(context.Background(), cfg, log)
	if err != nil {
		log.Error("bootstrap", "err", err)
		os.Exit(1)
	}
	defer application.Close()

	go func() {
		if err := application.Reconcile(ctx); err != nil {
			log.Warn("startup reconciliation completed with errors", "err", err)
		}
	}()

	png, err := cover.Load(cfg.PodcastImageFile)
	if err != nil {
		log.Error("cover art", "err", err)
		os.Exit(1)
	}

	mcp := mcpserver.NewServer(application)
	h := &api.Handler{
		App:   application,
		Cfg:   cfg,
		Cover: png,
		Log:   log,
		MCP:   mcpserver.HTTPHandler(mcp),
	}

	srv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           h.Router(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		// WriteTimeout is set to 0 (disabled) to avoid terminating long audio streaming or downloads.
		// Slowloris and hung read protection is maintained by ReadHeaderTimeout and IdleTimeout.
		WriteTimeout: 0,
		IdleTimeout:  90 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Info("listening", "addr", cfg.ListenAddr, "base_url", cfg.PublicBaseURL, "store", cfg.StoreBackend, "storage", cfg.StorageBackend, "jobs", cfg.JobBackend, "tts", cfg.TTSEngine)
		errCh <- srv.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
		defer cancel()
		if err := srv.Shutdown(shutdown); err != nil {
			log.Error("shutdown", "err", err)
		}
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("listen", "err", err)
			os.Exit(1)
		}
	}
}
