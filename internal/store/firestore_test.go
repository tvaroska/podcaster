package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tvaroska/podcaster/internal/episode"
	"github.com/tvaroska/podcaster/internal/podcast"
)

func TestFirestoreDocConversionAndUTC(t *testing.T) {
	loc := time.FixedZone("CET", 2*3600)
	created := time.Date(2026, 6, 15, 14, 30, 0, 123000000, loc)
	published := time.Date(2026, 6, 15, 15, 0, 0, 456000000, loc)

	ep := &episode.Episode{
		ID:          "ep_fs1",
		PodcastID:   "daily-news",
		Title:       "Test Firestore Doc",
		Description: "Detailed episode summary",
		ScriptText:  "Hello Firestore",
		Category:    "tech",
		VoiceID:     "en_US-lessac-medium",
		ImageURL:    "https://example.com/fs-ep.png",
		Chapters: []episode.Chapter{
			{StartSeconds: 0, Title: "Opening", URL: "https://example.com/open"},
			{StartSeconds: 30, Title: "Closing", ImageURL: "https://example.com/close.png"},
		},
		Status:          episode.StatusReady,
		AudioURI:        "audio/ep_fs1.mp3",
		DurationSeconds: 42.5,
		FileSizeBytes:   680000,
		ContentType:     "audio/mpeg",
		ErrorMessage:    "",
		CreatedAt:       created,
		PublishedAt:     &published,
	}

	doc := toDoc(ep)
	if doc.CreatedAt.Location() != time.UTC {
		t.Fatalf("expected doc.CreatedAt in UTC, got %v", doc.CreatedAt.Location())
	}
	if doc.PublishedAt == nil || doc.PublishedAt.Location() != time.UTC {
		t.Fatalf("expected doc.PublishedAt in UTC, got %v", doc.PublishedAt)
	}
	if !doc.CreatedAt.Equal(created) || !doc.PublishedAt.Equal(published) {
		t.Fatalf("timestamps changed value during UTC normalization")
	}

	// Ensure fromDoc also normalizes non-UTC timestamps from raw fsDoc
	rawPub := time.Date(2026, 6, 15, 16, 0, 0, 0, loc)
	doc.CreatedAt = created
	doc.PublishedAt = &rawPub

	roundTrip := fromDoc(doc)
	if roundTrip.ID != ep.ID || roundTrip.PodcastID != ep.PodcastID || roundTrip.Title != ep.Title ||
		roundTrip.Description != ep.Description || roundTrip.ImageURL != ep.ImageURL ||
		len(roundTrip.Chapters) != 2 || roundTrip.Chapters[0].Title != "Opening" || roundTrip.Chapters[1].StartSeconds != 30 ||
		roundTrip.ScriptText != ep.ScriptText || roundTrip.Category != ep.Category || roundTrip.VoiceID != ep.VoiceID ||
		roundTrip.Status != ep.Status || roundTrip.AudioURI != ep.AudioURI ||
		roundTrip.DurationSeconds != ep.DurationSeconds || roundTrip.FileSizeBytes != ep.FileSizeBytes ||
		roundTrip.ContentType != ep.ContentType {
		t.Fatalf("roundTrip mismatch: %+v", roundTrip)
	}
	if roundTrip.CreatedAt.Location() != time.UTC {
		t.Fatalf("expected roundTrip.CreatedAt in UTC, got %v", roundTrip.CreatedAt.Location())
	}
	if roundTrip.PublishedAt == nil || roundTrip.PublishedAt.Location() != time.UTC {
		t.Fatalf("expected roundTrip.PublishedAt in UTC, got %v", roundTrip.PublishedAt)
	}

	// Nil PublishedAt should stay nil
	epNoPub := &episode.Episode{
		ID:        "ep_fs2",
		Status:    episode.StatusPending,
		CreatedAt: created,
	}
	docNoPub := toDoc(epNoPub)
	if docNoPub.PublishedAt != nil {
		t.Fatalf("expected nil PublishedAt, got %v", docNoPub.PublishedAt)
	}
	fromNoPub := fromDoc(docNoPub)
	if fromNoPub.PublishedAt != nil {
		t.Fatalf("expected nil PublishedAt fromDoc, got %v", fromNoPub.PublishedAt)
	}
}

func TestFirestorePodcastDocConversion(t *testing.T) {
	loc := time.FixedZone("EST", -5*3600)
	created := time.Date(2026, 7, 1, 9, 0, 0, 0, loc)

	orig := &podcast.Podcast{
		ID:          "sec-brief",
		Title:       "Security Briefing",
		Description: "Daily sec",
		Author:      "SecBot",
		ImageURL:    "https://example.com/sec.png",
		Username:    "sec-brief",
		Password:    "pw123",
		Token:       "tok456",
		SubmitKey:   "sub789",
		CreatedAt:   created,
	}
	d := toPodcastDoc(orig)
	if d.SubmitKey != "sub789" || d.ImageURL != "https://example.com/sec.png" {
		t.Fatalf("expected SubmitKey and ImageURL in fsPodcastDoc, got %+v", d)
	}
	if d.CreatedAt.Location() != time.UTC || !d.CreatedAt.Equal(created) {
		t.Fatalf("expected d.CreatedAt in UTC, got %v", d.CreatedAt)
	}
	d.CreatedAt = created
	p := podcastFromDoc(d)
	if p.ID != d.ID || p.Title != d.Title || p.Description != d.Description || p.Author != d.Author ||
		p.ImageURL != d.ImageURL ||
		p.Username != d.Username || p.Password != d.Password || p.Token != d.Token || p.SubmitKey != d.SubmitKey {
		t.Fatalf("podcastFromDoc mismatch: %+v", p)
	}
	if p.CreatedAt.Location() != time.UTC || !p.CreatedAt.Equal(created) {
		t.Fatalf("expected CreatedAt in UTC equal to original, got %v", p.CreatedAt)
	}

	var fs Firestore
	if _, err := fs.GetPodcastBySubmitKey(context.Background(), "   "); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound on blank submit key, got %v", err)
	}
}
