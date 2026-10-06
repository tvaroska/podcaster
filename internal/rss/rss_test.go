package rss

import (
	"strings"
	"testing"
	"time"

	"github.com/tvaroska/podcaster/internal/episode"
)

func TestBuild(t *testing.T) {
	pub := time.Date(2026, 9, 26, 13, 30, 0, 0, time.UTC)
	xmlb, err := Build(Channel{
		Title:       "Private Agent Briefing",
		Link:        "https://podcast.example.com",
		Description: "Daily agent updates",
		Language:    "en-us",
		Author:      "Agent",
		OwnerEmail:  "agent@example.com",
		Category:    "Technology",
		ImageURL:    "https://podcast.example.com/cover.png",
		FeedURL:     "https://podcast.example.com/podcast.xml",
	}, []*episode.Episode{
		{
			ID:              "ep_ready",
			Title:           "Morning Briefing",
			ScriptText:      "Here are your top updates for today.",
			Category:        "Daily Briefing",
			Status:          episode.StatusReady,
			FileSizeBytes:   12345,
			ContentType:     "audio/mpeg",
			DurationSeconds: 125,
			PublishedAt:     &pub,
			CreatedAt:       pub,
		},
		{
			ID:              "ep_aac",
			Title:           "AAC Update",
			ScriptText:      "AAC script text only.",
			Status:          episode.StatusReady,
			FileSizeBytes:   6789,
			ContentType:     "audio/aac",
			DurationSeconds: 30,
			CreatedAt:       pub,
		},
		{
			ID:     "ep_pending",
			Title:  "Not yet",
			Status: episode.StatusPending,
		},
	}, ItemOptions{BaseURL: "https://podcast.example.com", Token: "tok+1"})
	if err != nil {
		t.Fatal(err)
	}
	s := string(xmlb)
	for _, want := range []string{
		`<?xml version="1.0" encoding="UTF-8"?>`,
		`<rss version="2.0"`,
		`xmlns:itunes="http://www.itunes.com/dtds/podcast-1.0.dtd"`,
		`<title>Private Agent Briefing</title>`,
		`<atom:link href="https://podcast.example.com/podcast.xml?token=tok%2B1" rel="self" type="application/rss+xml"></atom:link>`,
		`<itunes:block>yes</itunes:block>`,
		`<title>Morning Briefing</title>`,
		`<description>Daily Briefing: Here are your top updates for today.</description>`,
		`url="https://podcast.example.com/audio/ep_ready.mp3?token=tok%2B1"`,
		`type="audio/mpeg"`,
		`<itunes:duration>2:05</itunes:duration>`,
		`<guid isPermaLink="false">ep_ready</guid>`,
		`<description>AAC script text only.</description>`,
		`url="https://podcast.example.com/audio/ep_aac.aac?token=tok%2B1"`,
		`type="audio/aac"`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in:\n%s", want, s)
		}
	}
	if strings.Contains(s, "Not yet") {
		t.Fatal("pending episode leaked into feed")
	}
}

func TestFormatDuration(t *testing.T) {
	if g := formatDuration(0); g != "0" {
		t.Fatalf("got %s", g)
	}
	if g := formatDuration(5); g != "0:05" {
		t.Fatalf("got %s", g)
	}
	if g := formatDuration(3661); g != "1:01:01" {
		t.Fatalf("got %s", g)
	}
}

func TestBuildMetadataAndChapters(t *testing.T) {
	pub := time.Date(2026, 9, 26, 13, 30, 0, 0, time.UTC)
	xmlb, err := Build(Channel{
		Title:       "Alice Briefing",
		Link:        "https://podcast.example.com/p/alice",
		Description: "Private updates for Alice",
		Language:    "en-us",
		Author:      "Alice Agent",
		Category:    "Technology",
		ImageURL:    "https://podcast.example.com/alice-cover.png",
		FeedURL:     "https://podcast.example.com/p/alice/podcast.xml",
	}, []*episode.Episode{
		{
			ID:          "ep_chap",
			PodcastID:   "alice",
			Title:       "Episode With Chapters",
			Description: "Custom show notes for this episode.",
			ScriptText:  "Full spoken script text.",
			Category:    "Deep Dive",
			ImageURL:    "https://podcast.example.com/ep-icon.png",
			Chapters: []episode.Chapter{
				{
					StartSeconds: 0,
					Title:        "Intro",
					URL:          "https://example.com/intro",
					ImageURL:     "https://example.com/intro.png",
				},
				{
					StartSeconds: 135,
					Title:        "Main Topic",
				},
			},
			Status:          episode.StatusReady,
			FileSizeBytes:   54321,
			ContentType:     "audio/mpeg",
			DurationSeconds: 300,
			PublishedAt:     &pub,
			CreatedAt:       pub,
		},
		{
			ID:              "ep_fallback_cover",
			PodcastID:       "alice",
			Title:           "Fallback Artwork Episode",
			ScriptText:      "Script without custom episode icon.",
			Status:          episode.StatusReady,
			FileSizeBytes:   11111,
			ContentType:     "audio/mpeg",
			DurationSeconds: 60,
			PublishedAt:     &pub,
			CreatedAt:       pub,
		},
	}, Options{
		BaseURL:      "https://podcast.example.com",
		Token:        "alice-tok",
		AudioPath:    "/p/alice/audio",
		ChaptersPath: "/p/alice/episodes",
		CoverURL:     "https://podcast.example.com/alice-cover.png",
	})
	if err != nil {
		t.Fatal(err)
	}
	s := string(xmlb)
	for _, want := range []string{
		`xmlns:psc="http://podlove.org/simple-chapters"`,
		`xmlns:podcast="https://podcastindex.org/namespace/1.0"`,
		`Custom show notes for this episode.`,
		`Chapters:`,
		`0 Intro`,
		`2:15 Main Topic`,
		`<itunes:summary>Custom show notes for this episode.`,
		`<content:encoded>Custom show notes for this episode.`,
		`<itunes:image href="https://podcast.example.com/ep-icon.png"></itunes:image>`,
		`<itunes:image href="https://podcast.example.com/alice-cover.png"></itunes:image>`,
		`<psc:chapters version="1.2">`,
		`<psc:chapter start="00:00:00" title="Intro" href="https://example.com/intro" image="https://example.com/intro.png"></psc:chapter>`,
		`<psc:chapter start="00:02:15" title="Main Topic"></psc:chapter>`,
		`<podcast:chapters url="https://podcast.example.com/p/alice/episodes/ep_chap/chapters.json?token=alice-tok" type="application/json+chapters"></podcast:chapters>`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in:\n%s", want, s)
		}
	}
	if strings.Contains(s, "Deep Dive: Custom show notes") {
		t.Errorf("expected custom Description not to be prefixed with Category in:\n%s", s)
	}
}
