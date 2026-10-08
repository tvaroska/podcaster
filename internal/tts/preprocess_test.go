package tts

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestPreprocessStripsSSML(t *testing.T) {
	chunks := Preprocess(`<speak>Hello <break time="300ms"/> world &amp; friends.</speak>`)
	if len(chunks) != 1 {
		t.Fatalf("chunks: %v", chunks)
	}
	if strings.Contains(chunks[0], "<") {
		t.Fatalf("ssml leaked: %q", chunks[0])
	}
	if !strings.Contains(chunks[0], "Hello") || !strings.Contains(chunks[0], "world & friends.") {
		t.Fatalf("lost text: %q", chunks[0])
	}
}

func TestPreprocessPreservesPlainTextComparisons(t *testing.T) {
	input := "System check: latency < 5ms and throughput > 100 req/s."
	chunks := Preprocess(input)
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %v", chunks)
	}
	if chunks[0] != input {
		t.Fatalf("got %q, want %q", chunks[0], input)
	}
}

func TestPreprocessChunksLongText(t *testing.T) {
	sentence := strings.Repeat("This is a reasonably long sentence about the daily briefing. ", 80)
	chunks := Preprocess(sentence)
	if len(chunks) < 2 {
		t.Fatalf("expected multiple chunks, got %d (%d runes)", len(chunks), utf8.RuneCountInString(sentence))
	}
	for _, c := range chunks {
		if utf8.RuneCountInString(c) > maxChunkRunes {
			t.Fatalf("chunk too long: %d", utf8.RuneCountInString(c))
		}
	}
}

func TestChunkSeparatorBoundary(t *testing.T) {
	// Two sentences whose lengths sum to exactly maxRunes (so adding a space separator exceeds maxRunes).
	half := maxChunkRunes/2 - 1
	s1 := strings.Repeat("a", half) + "."
	s2 := strings.Repeat("b", half) + "."
	got := chunk(s1+" "+s2, maxChunkRunes)
	if len(got) != 2 {
		t.Fatalf("expected 2 chunks, got %d", len(got))
	}
	for i, c := range got {
		if n := utf8.RuneCountInString(c); n > maxChunkRunes {
			t.Fatalf("chunk %d rune count %d exceeds %d", i, n, maxChunkRunes)
		}
	}
}

func TestPreprocessCleansMarkdownAndSplitsParagraphs(t *testing.T) {
	input := "# Morning Briefing\n\n- **First** update with `inline_code` and a [report link](https://example.com/a).\n- Second bullet point https://example.com/raw\n\n---\n\nSecond paragraph after rule."
	chunks := Preprocess(input)
	if len(chunks) != 3 {
		t.Fatalf("expected 3 paragraph chunks (heading, list, second paragraph), got %d: %#v", len(chunks), chunks)
	}
	if chunks[0] != "Morning Briefing." {
		t.Fatalf("expected heading chunk with period, got %q", chunks[0])
	}
	if strings.ContainsAny(chunks[1], "*`#[]") || strings.Contains(chunks[1], "https://") {
		t.Fatalf("markdown or URL leaked into chunk: %q", chunks[1])
	}
	if !strings.Contains(chunks[1], "First update with inline_code and a report link.") {
		t.Fatalf("unexpected cleaned list chunk: %q", chunks[1])
	}
	if chunks[2] != "Second paragraph after rule." {
		t.Fatalf("unexpected final paragraph chunk: %q", chunks[2])
	}
}
