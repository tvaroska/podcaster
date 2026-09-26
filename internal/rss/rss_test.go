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
		Category:    "Technology",
		ImageURL:    "https://podcast.example.com/cover.png",
		FeedURL:     "https://podcast.example.com/podcast.xml",
	}, []*episode.Episode{
		{
			ID:              "ep_ready",
			Title:           "Morning Briefing",
			Category:        "Daily Briefing",
			Status:          episode.StatusReady,
			FileSizeBytes:   12345,
			ContentType:     "audio/mpeg",
			DurationSeconds: 125,
			PublishedAt:     &pub,
			CreatedAt:       pub,
		},
		{
			ID:     "ep_pending",
			Title:  "Not yet",
			Status: episode.StatusPending,
		},
	}, ItemOptions{BaseURL: "https://podcast.example.com", Token: "tok"})
	if err != nil {
		t.Fatal(err)
	}
	s := string(xmlb)
	for _, want := range []string{
		`<?xml version="1.0" encoding="UTF-8"?>`,
		`<rss version="2.0"`,
		`xmlns:itunes="http://www.itunes.com/dtds/podcast-1.0.dtd"`,
		`<title>Private Agent Briefing</title>`,
		`<itunes:block>yes</itunes:block>`,
		`<title>Morning Briefing</title>`,
		`url="https://podcast.example.com/audio/ep_ready.mp3?token=tok"`,
		`type="audio/mpeg"`,
		`<itunes:duration>2:05</itunes:duration>`,
		`<guid isPermaLink="false">ep_ready</guid>`,
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
	if g := formatDuration(5); g != "0:05" {
		t.Fatalf("got %s", g)
	}
	if g := formatDuration(3661); g != "1:01:01" {
		t.Fatalf("got %s", g)
	}
}
