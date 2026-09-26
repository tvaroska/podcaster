package app

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/tvaroska/podcaster/internal/config"
	"github.com/tvaroska/podcaster/internal/episode"
)

func TestAppReconciliation(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{
		ListenAddr:       ":0",
		PublicBaseURL:    "http://podcast.example.com",
		AgentAPIKey:      "secret-key",
		FeedUsername:     "podcast",
		FeedPassword:     "s3cret",
		FeedToken:        "feed-token",
		StoreBackend:     config.BackendSQLite,
		SQLitePath:       filepath.Join(dir, "reconcile.db"),
		StorageBackend:   config.BackendLocal,
		LocalDataDir:     filepath.Join(dir, "data"),
		JobBackend:       config.BackendLocal,
		TTSEngine:        config.EngineMock,
		FFmpegBin:        "ffmpeg",
		DefaultVoice:     config.DefaultVoice,
		MinContentLength: 1,
		MaxContentLength: 10000,
		MaxTitleLength:   100,
		WorkerTimeout:    time.Second,
	}

	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	// 1. First open app with dispatcher disabled to seed stranded episodes in DB
	appSeed, err := OpenWithOptions(ctx, cfg, log, Options{DisableDispatcher: true})
	if err != nil {
		t.Fatal(err)
	}

	// Seed one episode stuck in PROCESSING (e.g. previous crash)
	epProc := &episode.Episode{
		ID:         "ep_stranded_proc",
		Title:      "Stranded Processing",
		ScriptText: "Some text to speak.",
		Status:     episode.StatusProcessing,
		CreatedAt:  time.Now().UTC(),
	}
	if err := appSeed.Store.Create(ctx, epProc); err != nil {
		t.Fatal(err)
	}

	// Seed one episode stuck in PENDING (e.g. queued before abrupt server kill)
	epPend := &episode.Episode{
		ID:         "ep_stranded_pend",
		Title:      "Stranded Pending",
		ScriptText: "Another text to speak.",
		Status:     episode.StatusPending,
		CreatedAt:  time.Now().UTC(),
	}
	if err := appSeed.Store.Create(ctx, epPend); err != nil {
		t.Fatal(err)
	}
	_ = appSeed.Close()

	// 2. Open app normally (with local dispatcher enabled) and run Reconcile
	appLive, err := Open(ctx, cfg, log)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = appLive.Close() })

	if err := appLive.Reconcile(ctx); err != nil {
		t.Fatalf("Reconcile returned error: %v", err)
	}

	// Wait for background worker to synthesize both episodes
	deadline := time.Now().Add(3 * time.Second)
	for {
		e1, _ := appLive.Store.Get(ctx, epProc.ID)
		e2, _ := appLive.Store.Get(ctx, epPend.ID)
		if e1 != nil && e1.Status == episode.StatusReady && e2 != nil && e2.Status == episode.StatusReady {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for reconciled episodes to reach READY; status1=%v status2=%v", e1.Status, e2.Status)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
