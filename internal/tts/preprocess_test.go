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
	if len(chunks) != 1 {
		t.Fatalf("expected short paragraphs to coalesce into 1 chunk, got %d: %#v", len(chunks), chunks)
	}
	got := chunks[0]
	if !strings.HasPrefix(got, "Morning Briefing. ") {
		t.Fatalf("expected heading prefix with period, got %q", got)
	}
	if strings.ContainsAny(got, "*`#[]") || strings.Contains(got, "https://") {
		t.Fatalf("markdown or URL leaked into chunk: %q", got)
	}
	if !strings.Contains(got, "First update with inline_code and a report link.") {
		t.Fatalf("unexpected cleaned list in chunk: %q", got)
	}
	if !strings.HasSuffix(got, "Second bullet point. Second paragraph after rule.") {
		t.Fatalf("expected coalesced paragraphs with sentence punctuation, got %q", got)
	}

	// Verify paragraphs whose combined length exceeds maxChunkRunes stay in separate chunks.
	p1 := "First long section. " + strings.Repeat("alpha ", 45) + "end."
	p2 := "Second long section. " + strings.Repeat("bravo ", 45) + "end."
	splitChunks := Preprocess(p1 + "\n\n" + p2)
	if len(splitChunks) != 2 {
		t.Fatalf("expected 2 chunks for long paragraphs, got %d", len(splitChunks))
	}
	if splitChunks[0] != p1 || splitChunks[1] != p2 {
		t.Fatalf("unexpected split paragraph chunks: %#v", splitChunks)
	}
}
