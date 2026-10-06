package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tvaroska/podcaster/internal/auth"
	"github.com/tvaroska/podcaster/internal/config"
	"github.com/tvaroska/podcaster/internal/cover"
	"github.com/tvaroska/podcaster/internal/episode"
	"github.com/tvaroska/podcaster/internal/podcast"
	"github.com/tvaroska/podcaster/internal/storage"
	"github.com/tvaroska/podcaster/internal/store"
)

type spyDispatcher struct {
	enqueued []string
	err      error
}

func (d *spyDispatcher) Enqueue(_ context.Context, episodeID string) error {
	if d.err != nil {
		return d.err
	}
	d.enqueued = append(d.enqueued, episodeID)
	return nil
}

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

func TestAppReconcileCloudRunStalenessCutoff(t *testing.T) {
	ctx := context.Background()
	st, err := store.OpenSQLite(filepath.Join(t.TempDir(), "cloudrun.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	now := time.Now().UTC()
	recentProc := &episode.Episode{
		ID:         "ep_recent_proc",
		Title:      "Recent Processing",
		ScriptText: "Still running on Cloud Run Job",
		Status:     episode.StatusProcessing,
		CreatedAt:  now.Add(-2 * time.Minute),
	}
	staleProc := &episode.Episode{
		ID:         "ep_stale_proc",
		Title:      "Stale Processing",
		ScriptText: "Timed out on Cloud Run Job",
		Status:     episode.StatusProcessing,
		CreatedAt:  now.Add(-45 * time.Minute),
	}
	recentPend := &episode.Episode{
		ID:         "ep_recent_pend",
		Title:      "Recent Pending",
		ScriptText: "Just enqueued",
		Status:     episode.StatusPending,
		CreatedAt:  now.Add(-1 * time.Minute),
	}
	stalePend := &episode.Episode{
		ID:         "ep_stale_pend",
		Title:      "Stale Pending",
		ScriptText: "Stranded pending",
		Status:     episode.StatusPending,
		CreatedAt:  now.Add(-40 * time.Minute),
	}
	for _, ep := range []*episode.Episode{recentProc, staleProc, recentPend, stalePend} {
		if err := st.Create(ctx, ep); err != nil {
			t.Fatal(err)
		}
	}

	disp := &spyDispatcher{}
	a := &App{
		Cfg: &config.Config{
			JobBackend:    config.BackendCloudRun,
			WorkerTimeout: 15 * time.Minute,
		},
		Store: st,
		Jobs:  disp,
		Log:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	if err := a.Reconcile(ctx); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	// Recent PROCESSING episode must NOT be clobbered.
	gotRecentProc, err := st.Get(ctx, recentProc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gotRecentProc.Status != episode.StatusProcessing {
		t.Fatalf("expected recent PROCESSING episode to stay PROCESSING, got %s", gotRecentProc.Status)
	}

	// Stale PROCESSING episode MUST be reset to PENDING.
	gotStaleProc, err := st.Get(ctx, staleProc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gotStaleProc.Status != episode.StatusPending {
		t.Fatalf("expected stale PROCESSING episode to be reset to PENDING, got %s", gotStaleProc.Status)
	}

	// Only stale episodes (staleProc after reset + stalePend) should be re-enqueued.
	if len(disp.enqueued) != 2 {
		t.Fatalf("expected 2 enqueued episodes, got %v", disp.enqueued)
	}
	seen := map[string]bool{}
	for _, id := range disp.enqueued {
		seen[id] = true
	}
	if !seen[staleProc.ID] || !seen[stalePend.ID] || seen[recentProc.ID] || seen[recentPend.ID] {
		t.Fatalf("unexpected enqueued set: %v", disp.enqueued)
	}
}

func TestCreateEpisodeEnqueueFailureAndValidation(t *testing.T) {
	ctx := context.Background()
	st, err := store.OpenSQLite(filepath.Join(t.TempDir(), "create.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	disp := &spyDispatcher{err: errors.New("cloud run quota exceeded")}
	a := &App{
		Cfg: &config.Config{
			DefaultVoice:     config.DefaultVoice,
			MinContentLength: 5,
			MaxContentLength: 1000,
			MaxTitleLength:   100,
		},
		Store: st,
		Jobs:  disp,
		Log:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	// 1. Validation error on unknown podcast_id
	_, err = a.CreateEpisode(ctx, episode.CreateInput{
		Title:     "Test",
		Content:   "Valid content string.",
		PodcastID: "nonexistent",
	})
	var verr *episode.ValidationError
	if !errors.As(err, &verr) || verr.Field != "podcast_id" {
		t.Fatalf("expected podcast_id ValidationError, got %v", err)
	}

	// 2. Enqueue failure keeps episode in PENDING with ErrorMessage so Reconcile can recover it later
	_, err = a.CreateEpisode(ctx, episode.CreateInput{
		Title:   "Enqueue Failure Test",
		Content: "Valid content string for synthesis.",
	})
	if err == nil || !strings.Contains(err.Error(), "enqueue job") {
		t.Fatalf("expected enqueue job error, got %v", err)
	}

	list, err := st.List(ctx, episode.ListFilter{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 persisted episode, got %d", len(list))
	}
	if list[0].Status != episode.StatusPending {
		t.Fatalf("expected episode status to remain PENDING for reconciliation, got %s", list[0].Status)
	}
	if !strings.Contains(list[0].ErrorMessage, "failed to enqueue synthesis job: cloud run quota exceeded") {
		t.Fatalf("unexpected ErrorMessage: %q", list[0].ErrorMessage)
	}
}

func TestPodcastSubmitKeyRotationAndAuthenticateBearer(t *testing.T) {
	ctx := context.Background()
	st, err := store.OpenSQLite(filepath.Join(t.TempDir(), "auth.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	a := &App{
		Cfg: &config.Config{
			AgentAPIKey:        "default-agent-key",
			PodcastAuthor:      "Podcaster",
			PodcastDescription: "Default desc",
		},
		Store: st,
		Jobs:  &spyDispatcher{},
		Log:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	p, err := a.CreatePodcast(ctx, podcast.CreateInput{
		ID:    "alice",
		Title: "Alice Show",
	})
	if err != nil {
		t.Fatalf("CreatePodcast: %v", err)
	}
	if p.SubmitKey == "" || p.Password == "" || p.Token == "" {
		t.Fatalf("expected non-empty credentials, got %+v", p)
	}
	origPass := p.Password
	origTok := p.Token
	origSubmitKey := p.SubmitKey

	// 1. When AdminAPIKey is unset, AgentAPIKey acts as RoleAdmin
	pr, ok, err := a.AuthenticateBearer(ctx, "default-agent-key")
	if err != nil || !ok || pr.Role != auth.RoleAdmin {
		t.Fatalf("expected RoleAdmin when AdminAPIKey is unset, got %+v ok=%v err=%v", pr, ok, err)
	}

	// 2. When AdminAPIKey is set, AdminAPIKey -> RoleAdmin, AgentAPIKey -> RoleDefaultSubmitter
	a.Cfg.AdminAPIKey = "super-admin-key"
	pr, ok, err = a.AuthenticateBearer(ctx, "super-admin-key")
	if err != nil || !ok || pr.Role != auth.RoleAdmin {
		t.Fatalf("expected RoleAdmin for AdminAPIKey, got %+v ok=%v err=%v", pr, ok, err)
	}
	pr, ok, err = a.AuthenticateBearer(ctx, "default-agent-key")
	if err != nil || !ok || pr.Role != auth.RoleDefaultSubmitter {
		t.Fatalf("expected RoleDefaultSubmitter for AgentAPIKey, got %+v ok=%v err=%v", pr, ok, err)
	}

	// 3. Per-user submit key -> RolePodcastSubmitter for "alice"
	pr, ok, err = a.AuthenticateBearer(ctx, origSubmitKey)
	if err != nil || !ok || pr.Role != auth.RolePodcastSubmitter || pr.PodcastID != "alice" {
		t.Fatalf("expected RolePodcastSubmitter(alice), got %+v ok=%v err=%v", pr, ok, err)
	}

	// 4. Invalid or empty token -> false
	if _, ok, err := a.AuthenticateBearer(ctx, "bad-token"); err != nil || ok {
		t.Fatalf("expected false for bad token, got ok=%v err=%v", ok, err)
	}
	if _, ok, err := a.AuthenticateBearer(ctx, ""); err != nil || ok {
		t.Fatalf("expected false for empty token, got ok=%v err=%v", ok, err)
	}

	// 5. Default RotatePodcastCredentials rotates listener Password & Token, keeps SubmitKey
	rotated1, err := a.RotatePodcastCredentials(ctx, "alice", podcast.RotateOptions{})
	if err != nil {
		t.Fatalf("RotatePodcastCredentials default: %v", err)
	}
	if rotated1.Password == origPass || rotated1.Token == origTok {
		t.Fatalf("expected rotated listener credentials, got %+v", rotated1)
	}
	if rotated1.SubmitKey != origSubmitKey {
		t.Fatalf("expected SubmitKey unchanged, got %q != %q", rotated1.SubmitKey, origSubmitKey)
	}

	// 6. Custom Password & Token + RotateSubmitKey
	rotated2, err := a.RotatePodcastCredentials(ctx, "alice", podcast.RotateOptions{
		RotateSubmitKey: true,
		Password:        "custom-pass",
		Token:           "custom-token",
	})
	if err != nil {
		t.Fatalf("RotatePodcastCredentials custom+submit: %v", err)
	}
	if rotated2.Password != "custom-pass" || rotated2.Token != "custom-token" {
		t.Fatalf("expected custom password/token, got %+v", rotated2)
	}
	if rotated2.SubmitKey == origSubmitKey || rotated2.SubmitKey == "" {
		t.Fatalf("expected rotated SubmitKey, got %q", rotated2.SubmitKey)
	}

	// Old submit key is rejected, new submit key is accepted
	if _, ok, err := a.AuthenticateBearer(ctx, origSubmitKey); err != nil || ok {
		t.Fatalf("expected old submit key rejected, got ok=%v err=%v", ok, err)
	}
	pr, ok, err = a.AuthenticateBearer(ctx, rotated2.SubmitKey)
	if err != nil || !ok || pr.Role != auth.RolePodcastSubmitter || pr.PodcastID != "alice" {
		t.Fatalf("expected new submit key accepted, got %+v ok=%v err=%v", pr, ok, err)
	}

	// Validation error on empty id and ErrNotFound on unknown podcast
	if _, err := a.RotatePodcastCredentials(ctx, "  ", podcast.RotateOptions{}); err == nil {
		t.Fatal("expected error for empty podcast_id")
	}
	if _, err := a.RotatePodcastCredentials(ctx, "unknown", podcast.RotateOptions{}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("expected store.ErrNotFound for unknown podcast, got %v", err)
	}
}

func TestUpdatePodcastAndEpisode(t *testing.T) {
	ctx := context.Background()
	st, err := store.OpenSQLite(filepath.Join(t.TempDir(), "update.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	a := &App{
		Cfg: &config.Config{
			DefaultVoice:       config.DefaultVoice,
			MinContentLength:   5,
			MaxContentLength:   1000,
			MaxTitleLength:     100,
			PodcastAuthor:      "Podcaster",
			PodcastDescription: "Default desc",
		},
		Store: st,
		Jobs:  &spyDispatcher{},
		Log:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	strPtr := func(s string) *string { return &s }

	p, err := a.CreatePodcast(ctx, podcast.CreateInput{
		ID:       "tech-weekly",
		Title:    "Tech Weekly",
		ImageURL: "https://example.com/initial.png",
	})
	if err != nil {
		t.Fatalf("CreatePodcast: %v", err)
	}
	if p.ImageURL != "https://example.com/initial.png" {
		t.Fatalf("expected ImageURL on CreatePodcast, got %q", p.ImageURL)
	}

	updatedPod, err := a.UpdatePodcast(ctx, "tech-weekly", podcast.UpdateInput{
		Title:       strPtr("Tech Weekly 2.0"),
		Description: strPtr("Updated show description"),
		Author:      strPtr("AI Host"),
		ImageURL:    strPtr("https://example.com/v2.png"),
	})
	if err != nil {
		t.Fatalf("UpdatePodcast: %v", err)
	}
	if updatedPod.Title != "Tech Weekly 2.0" || updatedPod.Description != "Updated show description" ||
		updatedPod.Author != "AI Host" || updatedPod.ImageURL != "https://example.com/v2.png" {
		t.Fatalf("unexpected updated podcast: %+v", updatedPod)
	}

	if _, err := a.UpdatePodcast(ctx, " ", podcast.UpdateInput{}); err == nil {
		t.Fatal("expected ValidationError for empty podcast_id")
	}
	if _, err := a.UpdatePodcast(ctx, "missing", podcast.UpdateInput{}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("expected ErrNotFound for missing podcast, got %v", err)
	}

	ep, err := a.CreateEpisode(ctx, episode.CreateInput{
		Title:       "Episode 1",
		Description: "Initial notes",
		Content:     "Hello world from episode one.",
		PodcastID:   "tech-weekly",
		ImageURL:    "https://example.com/ep1.png",
		Chapters:    []episode.Chapter{{StartSeconds: 0, Title: "Intro"}},
	})
	if err != nil {
		t.Fatalf("CreateEpisode: %v", err)
	}
	if ep.Description != "Initial notes" || ep.ImageURL != "https://example.com/ep1.png" || len(ep.Chapters) != 1 {
		t.Fatalf("unexpected created episode metadata: %+v", ep)
	}

	newChaps := []episode.Chapter{
		{StartSeconds: 0, Title: "Intro"},
		{StartSeconds: 30, Title: "Part 2", URL: "https://example.com/p2"},
	}
	updatedEp, err := a.UpdateEpisode(ctx, ep.ID, episode.UpdateInput{
		Title:       strPtr("Episode 1 Remastered"),
		Description: strPtr("Extended show notes"),
		Category:    strPtr("engineering"),
		ImageURL:    strPtr("https://example.com/ep1-new.png"),
		Chapters:    &newChaps,
	})
	if err != nil {
		t.Fatalf("UpdateEpisode: %v", err)
	}
	if updatedEp.Title != "Episode 1 Remastered" || updatedEp.Description != "Extended show notes" ||
		updatedEp.Category != "engineering" || updatedEp.ImageURL != "https://example.com/ep1-new.png" ||
		len(updatedEp.Chapters) != 2 {
		t.Fatalf("unexpected updated episode: %+v", updatedEp)
	}

	if _, err := a.UpdateEpisode(ctx, " ", episode.UpdateInput{}); err == nil {
		t.Fatal("expected ValidationError for empty episode_id")
	}
	if _, err := a.UpdateEpisode(ctx, "ep_missing", episode.UpdateInput{}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("expected ErrNotFound for missing episode, got %v", err)
	}
}

func makeTestPNG(t *testing.T, c color.RGBA) ([]byte, string) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			img.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	raw := buf.Bytes()
	return raw, "data:image/png;base64," + base64.StdEncoding.EncodeToString(raw)
}

func TestInlineCoverPersistenceAndRewrite(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	st, err := store.OpenSQLite(filepath.Join(dir, "inline.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	blob, err := storage.OpenLocal(filepath.Join(dir, "data"))
	if err != nil {
		t.Fatal(err)
	}

	a := &App{
		Cfg: &config.Config{
			PublicBaseURL:      "http://podcast.example.com",
			DefaultVoice:       config.DefaultVoice,
			MinContentLength:   5,
			MaxContentLength:   1000,
			MaxTitleLength:     100,
			PodcastAuthor:      "Podcaster",
			PodcastDescription: "Default desc",
		},
		Store:   st,
		Storage: blob,
		Jobs:    &spyDispatcher{},
		Log:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	png1, uri1 := makeTestPNG(t, color.RGBA{R: 255, A: 255})
	png2, uri2 := makeTestPNG(t, color.RGBA{G: 255, A: 255})

	// 1. CreatePodcast with inline data URI stores bytes at covers/podcasts/alice and rewrites ImageURL
	p, err := a.CreatePodcast(ctx, podcast.CreateInput{
		ID:       "alice",
		Title:    "Alice Show",
		ImageURL: uri1,
	})
	if err != nil {
		t.Fatalf("CreatePodcast inline image: %v", err)
	}
	if p.ImageURL != "http://podcast.example.com/p/alice/cover.png" {
		t.Fatalf("expected rewritten podcast ImageURL, got %q", p.ImageURL)
	}
	rc, err := blob.Open(ctx, cover.PodcastCoverKey("alice"))
	if err != nil {
		t.Fatalf("Open stored podcast cover: %v", err)
	}
	gotBytes, _ := io.ReadAll(rc)
	_ = rc.Close()
	if !bytes.Equal(gotBytes, png1) {
		t.Fatalf("stored podcast cover bytes mismatch")
	}

	// 2. UpdatePodcast with new inline data URI overwrites stored bytes
	p2, err := a.UpdatePodcast(ctx, "alice", podcast.UpdateInput{ImageURL: &uri2})
	if err != nil {
		t.Fatalf("UpdatePodcast inline image: %v", err)
	}
	if p2.ImageURL != "http://podcast.example.com/p/alice/cover.png" {
		t.Fatalf("expected rewritten podcast ImageURL on update, got %q", p2.ImageURL)
	}
	rc2, err := blob.Open(ctx, cover.PodcastCoverKey("alice"))
	if err != nil {
		t.Fatalf("Open updated podcast cover: %v", err)
	}
	gotBytes2, _ := io.ReadAll(rc2)
	_ = rc2.Close()
	if !bytes.Equal(gotBytes2, png2) {
		t.Fatalf("updated podcast cover bytes mismatch")
	}

	// 3. CreateEpisode and UpdateEpisode with inline data URI
	ep, err := a.CreateEpisode(ctx, episode.CreateInput{
		Title:     "Ep 1",
		Content:   "Good morning Alice, here is your update.",
		PodcastID: "alice",
		ImageURL:  uri1,
	})
	if err != nil {
		t.Fatalf("CreateEpisode inline image: %v", err)
	}
	wantEpCover := "http://podcast.example.com/p/alice/episodes/" + ep.ID + "/cover.png"
	if ep.ImageURL != wantEpCover {
		t.Fatalf("got %q, want %q", ep.ImageURL, wantEpCover)
	}
	epRC, err := blob.Open(ctx, cover.EpisodeCoverKey(ep.ID))
	if err != nil {
		t.Fatalf("Open stored episode cover: %v", err)
	}
	gotEpBytes, _ := io.ReadAll(epRC)
	_ = epRC.Close()
	if !bytes.Equal(gotEpBytes, png1) {
		t.Fatalf("stored episode cover bytes mismatch")
	}

	epUpdated, err := a.UpdateEpisode(ctx, ep.ID, episode.UpdateInput{ImageURL: &uri2})
	if err != nil {
		t.Fatalf("UpdateEpisode inline image: %v", err)
	}
	if epUpdated.ImageURL != wantEpCover {
		t.Fatalf("got %q, want %q", epUpdated.ImageURL, wantEpCover)
	}
	epRC2, err := blob.Open(ctx, cover.EpisodeCoverKey(ep.ID))
	if err != nil {
		t.Fatalf("Open updated episode cover: %v", err)
	}
	gotEpBytes2, _ := io.ReadAll(epRC2)
	_ = epRC2.Close()
	if !bytes.Equal(gotEpBytes2, png2) {
		t.Fatalf("updated episode cover bytes mismatch")
	}
}
