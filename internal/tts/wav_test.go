package tts

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPolishChunkWAVAppliesFadesAndSilence(t *testing.T) {
	const sampleRate = 24000
	dir := t.TempDir()
	wavPath := filepath.Join(dir, "chunk.wav")

	// Construct two 300ms voiced segments (amplitude 10000) separated by a 100ms
	// internal sentence silence gap (amplitude 0), which PolishChunkWAV should
	// expand to 300ms (SentenceSilenceDuration) and append 700ms (ParagraphSilenceDuration).
	voiceSeg := make([]int16, durationToSamples(sampleRate, 300*time.Millisecond))
	for i := range voiceSeg {
		voiceSeg[i] = 10000
	}
	shortGap := make([]int16, durationToSamples(sampleRate, 100*time.Millisecond))

	var rawSamples []int16
	rawSamples = append(rawSamples, voiceSeg...)
	rawSamples = append(rawSamples, shortGap...)
	rawSamples = append(rawSamples, voiceSeg...)

	if err := os.WriteFile(wavPath, encodePCM16MonoWAV(sampleRate, rawSamples), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := PolishChunkWAV(wavPath, ParagraphSilenceDuration); err != nil {
		t.Fatalf("PolishChunkWAV error: %v", err)
	}

	polishedBytes, err := os.ReadFile(wavPath)
	if err != nil {
		t.Fatal(err)
	}
	gotRate, gotSamples, ok := parsePCM16MonoWAV(polishedBytes)
	if !ok || gotRate != sampleRate {
		t.Fatalf("parse polished WAV failed: ok=%v rate=%d", ok, gotRate)
	}

	// Expected total length:
	// 300ms voice + 300ms expanded internal sentence pause + 300ms voice + 700ms trailing paragraph silence = 1600ms.
	wantSamples := durationToSamples(sampleRate, 1600*time.Millisecond)
	if len(gotSamples) != wantSamples {
		t.Fatalf("polished sample count = %d, want %d", len(gotSamples), wantSamples)
	}

	// First sample must be 0 due to 8ms linear fade-in.
	if gotSamples[0] != 0 {
		t.Fatalf("expected first sample to be 0 from 8ms linear fade-in, got %d", gotSamples[0])
	}
	fadeLen := durationToSamples(sampleRate, BoundaryFadeDuration)
	if gotSamples[fadeLen/2] <= 0 || gotSamples[fadeLen/2] >= 10000 {
		t.Fatalf("expected midpoint of 8ms fade-in to be ramped, got %d", gotSamples[fadeLen/2])
	}

	// Last 700ms must be exact digital silence (0).
	trailStart := len(gotSamples) - durationToSamples(sampleRate, ParagraphSilenceDuration)
	for i := trailStart; i < len(gotSamples); i++ {
		if gotSamples[i] != 0 {
			t.Fatalf("expected trailing silence sample %d to be 0, got %d", i, gotSamples[i])
		}
	}
	// Sample immediately preceding trailing silence must be 0 from 8ms fade-out.
	if gotSamples[trailStart-1] != 0 {
		t.Fatalf("expected last voiced sample before trailing silence to be 0 from fade-out, got %d", gotSamples[trailStart-1])
	}
}

func TestPolishChunkWAVNoOpOnNonWAV(t *testing.T) {
	dir := t.TempDir()
	fakePath := filepath.Join(dir, "fake.wav")
	orig := []byte("RIFFfakewavdata")
	if err := os.WriteFile(fakePath, orig, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := PolishChunkWAV(fakePath, SentenceSilenceDuration); err != nil {
		t.Fatalf("PolishChunkWAV error on fake data: %v", err)
	}
	got, err := os.ReadFile(fakePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(orig) {
		t.Fatalf("expected non-WAVE file to remain unchanged, got %q", string(got))
	}
}
