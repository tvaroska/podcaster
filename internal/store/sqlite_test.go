package store

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/tvaroska/podcaster/internal/episode"
	"github.com/tvaroska/podcaster/internal/podcast"
)

func TestSQLiteCRUD(t *testing.T) {
	ctx := context.Background()
	s, err := OpenSQLite(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })

	now := time.Now().UTC().Truncate(time.Millisecond)
	ep := &episode.Episode{
		ID:          "ep_abc",
		Title:       "Hello",
		Description: "Initial episode description",
		ScriptText:  "Hello world from the briefing.",
		ImageURL:    "https://example.com/ep1.png",
		Chapters: []episode.Chapter{
			{StartSeconds: 0, Title: "Intro", URL: "https://example.com/intro"},
			{StartSeconds: 12.5, Title: "Main Topic", ImageURL: "https://example.com/topic.png"},
		},
		Status:    episode.StatusPending,
		CreatedAt: now,
	}
	if err := s.Create(ctx, ep); err != nil {
		t.Fatal(err)
	}

	got, err := s.Get(ctx, ep.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != ep.Title || got.Status != episode.StatusPending ||
		got.Description != ep.Description || got.ImageURL != ep.ImageURL || len(got.Chapters) != 2 ||
		got.Chapters[0].Title != "Intro" || got.Chapters[1].StartSeconds != 12.5 {
		t.Fatalf("got %+v", got)
	}

	if _, err := s.Get(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected not found, got %v", err)
	}

	swapped, err := s.CompareAndSwapStatus(ctx, ep.ID, episode.StatusPending, episode.StatusProcessing)
	if err != nil {
		t.Fatal(err)
	}
	if swapped.Status != episode.StatusProcessing {
		t.Fatalf("status %s", swapped.Status)
	}
	if _, err := s.CompareAndSwapStatus(ctx, ep.ID, episode.StatusPending, episode.StatusReady); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected conflict, got %v", err)
	}

	pub := now.Add(time.Minute)
	swapped.Status = episode.StatusReady
	swapped.AudioURI = "audio/ep_abc.mp3"
	swapped.Description = "Updated episode description"
	swapped.ImageURL = "https://example.com/ep1-updated.png"
	swapped.Chapters = []episode.Chapter{{StartSeconds: 0, Title: "Single Chapter"}}
	swapped.PublishedAt = &pub
	if err := s.Update(ctx, swapped); err != nil {
		t.Fatal(err)
	}

	list, err := s.List(ctx, episode.ListFilter{Status: episode.StatusReady, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].AudioURI == "" || list[0].PublishedAt == nil ||
		list[0].Description != "Updated episode description" ||
		list[0].ImageURL != "https://example.com/ep1-updated.png" ||
		len(list[0].Chapters) != 1 || list[0].Chapters[0].Title != "Single Chapter" {
		t.Fatalf("list: %+v", list)
	}

	if err := s.Delete(ctx, ep.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, ep.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected not found after delete, got %v", err)
	}
}

func TestSQLitePodcastIsolation(t *testing.T) {
	ctx := context.Background()
	s, err := OpenSQLite(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })

	now := time.Now().UTC()
	alice := &podcast.Podcast{
		ID: "alice", Title: "Alice", ImageURL: "https://example.com/alice.png",
		Username: "alice", Password: "a", Token: "ta", SubmitKey: "sk_alice_123", CreatedAt: now,
	}
	if err := s.CreatePodcast(ctx, alice); err != nil {
		t.Fatal(err)
	}
	if err := s.CreatePodcast(ctx, alice); !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("dup: %v", err)
	}
	got, err := s.GetPodcast(ctx, "alice")
	if err != nil || got.Title != "Alice" || got.SubmitKey != "sk_alice_123" || got.ImageURL != "https://example.com/alice.png" {
		t.Fatalf("get %+v %v", got, err)
	}

	byKey, err := s.GetPodcastBySubmitKey(ctx, "sk_alice_123")
	if err != nil || byKey.ID != "alice" || byKey.ImageURL != "https://example.com/alice.png" {
		t.Fatalf("GetPodcastBySubmitKey: %+v %v", byKey, err)
	}
	if _, err := s.GetPodcastBySubmitKey(ctx, "wrong_key"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound for wrong key, got %v", err)
	}
	if _, err := s.GetPodcastBySubmitKey(ctx, "   "); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound for empty key, got %v", err)
	}

	alice.Password = "new_pw"
	alice.Token = "new_tok"
	alice.SubmitKey = "sk_alice_456"
	alice.ImageURL = "https://example.com/alice-v2.png"
	if err := s.UpdatePodcast(ctx, alice); err != nil {
		t.Fatalf("UpdatePodcast: %v", err)
	}
	updated, err := s.GetPodcast(ctx, "alice")
	if err != nil || updated.Password != "new_pw" || updated.Token != "new_tok" || updated.SubmitKey != "sk_alice_456" || updated.ImageURL != "https://example.com/alice-v2.png" {
		t.Fatalf("updated podcast mismatch: %+v %v", updated, err)
	}
	if _, err := s.GetPodcastBySubmitKey(ctx, "sk_alice_123"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected old submit key to return ErrNotFound, got %v", err)
	}
	if byNewKey, err := s.GetPodcastBySubmitKey(ctx, "sk_alice_456"); err != nil || byNewKey.ID != "alice" {
		t.Fatalf("expected new submit key to match alice, got %+v %v", byNewKey, err)
	}
	if err := s.UpdatePodcast(ctx, &podcast.Podcast{ID: "missing", Title: "Missing"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound updating missing podcast, got %v", err)
	}

	a := &episode.Episode{ID: "ep_a", PodcastID: "alice", Title: "A", ScriptText: "hello", Status: episode.StatusReady, CreatedAt: now}
	b := &episode.Episode{ID: "ep_b", PodcastID: "bob", Title: "B", ScriptText: "hello", Status: episode.StatusReady, CreatedAt: now}
	def := &episode.Episode{ID: "ep_d", Title: "D", ScriptText: "hello", Status: episode.StatusReady, CreatedAt: now}
	for _, ep := range []*episode.Episode{a, b, def} {
		if err := s.Create(ctx, ep); err != nil {
			t.Fatal(err)
		}
	}

	list, err := s.List(ctx, episode.ListFilter{PodcastID: "alice", Status: episode.StatusReady, Limit: 10})
	if err != nil || len(list) != 1 || list[0].ID != "ep_a" {
		t.Fatalf("alice list %+v %v", list, err)
	}
	list, err = s.List(ctx, episode.ListFilter{OnlyDefault: true, Status: episode.StatusReady, Limit: 10})
	if err != nil || len(list) != 1 || list[0].ID != "ep_d" {
		t.Fatalf("default list %+v %v", list, err)
	}
}

func TestSQLiteSubSecondOrderingAndLimitClamp(t *testing.T) {
	ctx := context.Background()
	s, err := OpenSQLite(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })

	base := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	earlier := &episode.Episode{
		ID:         "ep_000ms",
		Title:      "Earlier (0ns)",
		ScriptText: "First",
		Status:     episode.StatusReady,
		CreatedAt:  base,
	}
	later := &episode.Episode{
		ID:         "ep_500ms",
		Title:      "Later (500ms)",
		ScriptText: "Second",
		Status:     episode.StatusReady,
		CreatedAt:  base.Add(500 * time.Millisecond),
	}
	if err := s.Create(ctx, earlier); err != nil {
		t.Fatal(err)
	}
	if err := s.Create(ctx, later); err != nil {
		t.Fatal(err)
	}

	list, err := s.List(ctx, episode.ListFilter{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].ID != "ep_500ms" || list[1].ID != "ep_000ms" {
		t.Fatalf("expected newest-first [ep_500ms, ep_000ms], got [%s, %s]", list[0].ID, list[1].ID)
	}

	// Insert enough additional episodes to exceed 100 and verify Limit: 200 clamps to 100.
	for i := 0; i < 105; i++ {
		ep := &episode.Episode{
			ID:         fmt.Sprintf("ep_bulk_%03d", i),
			Title:      "Bulk",
			ScriptText: "Text",
			Status:     episode.StatusReady,
			CreatedAt:  base.Add(time.Duration(i+1) * time.Second),
		}
		if err := s.Create(ctx, ep); err != nil {
			t.Fatal(err)
		}
	}
	clamped, err := s.List(ctx, episode.ListFilter{Limit: 200})
	if err != nil {
		t.Fatal(err)
	}
	if len(clamped) != 100 {
		t.Fatalf("expected Limit: 200 to clamp to 100 results, got %d", len(clamped))
	}
}
