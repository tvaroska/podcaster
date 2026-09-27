package store

import (
	"context"
	"errors"
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
		ID:         "ep_abc",
		Title:      "Hello",
		ScriptText: "Hello world from the briefing.",
		Status:     episode.StatusPending,
		CreatedAt:  now,
	}
	if err := s.Create(ctx, ep); err != nil {
		t.Fatal(err)
	}

	got, err := s.Get(ctx, ep.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != ep.Title || got.Status != episode.StatusPending {
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
	swapped.PublishedAt = &pub
	if err := s.Update(ctx, swapped); err != nil {
		t.Fatal(err)
	}

	list, err := s.List(ctx, episode.ListFilter{Status: episode.StatusReady, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].AudioURI == "" || list[0].PublishedAt == nil {
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
	alice := &podcast.Podcast{ID: "alice", Title: "Alice", Username: "alice", Password: "a", Token: "ta", CreatedAt: now}
	if err := s.CreatePodcast(ctx, alice); err != nil {
		t.Fatal(err)
	}
	if err := s.CreatePodcast(ctx, alice); !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("dup: %v", err)
	}
	got, err := s.GetPodcast(ctx, "alice")
	if err != nil || got.Title != "Alice" {
		t.Fatalf("get %+v %v", got, err)
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
