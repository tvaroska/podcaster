package tts

import "testing"

func TestMockReportsDuration(t *testing.T) {
	eng := &MockEngine{}
	res, err := eng.Synthesize(t.Context(), "Good morning. Duration check.", "mock")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Close()
	if res.DurationSeconds < 1.0 || res.DurationSeconds > 2.5 {
		t.Fatalf("duration %v, want ~1.57s fixture", res.DurationSeconds)
	}
}

func TestMP3DurationFixture(t *testing.T) {
	d := mp3DurationSeconds(defaultMockMP3())
	if d < 1.0 || d > 2.5 {
		t.Fatalf("parsed duration %v", d)
	}
}
