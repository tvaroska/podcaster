package tts

import (
	"context"
	"io"
)

// Result is a synthesized audio stream plus metadata.
type Result struct {
	Reader          io.ReadCloser
	ContentType     string
	Extension       string
	DurationSeconds float64
}

// Engine converts text to an audio enclosure.
type Engine interface {
	Synthesize(ctx context.Context, text, voiceID string) (*Result, error)
}

func (r *Result) Close() error {
	if r == nil || r.Reader == nil {
		return nil
	}
	return r.Reader.Close()
}
