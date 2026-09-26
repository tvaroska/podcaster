package tts

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestPreprocessStripsSSML(t *testing.T) {
	chunks := Preprocess(`<speak>Hello <break time="300ms"/> world.</speak>`)
	if len(chunks) != 1 {
		t.Fatalf("chunks: %v", chunks)
	}
	if strings.Contains(chunks[0], "<") {
		t.Fatalf("ssml leaked: %q", chunks[0])
	}
	if !strings.Contains(chunks[0], "Hello") || !strings.Contains(chunks[0], "world") {
		t.Fatalf("lost text: %q", chunks[0])
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
