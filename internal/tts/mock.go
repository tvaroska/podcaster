package tts

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
)

// MockEngine returns a canned MP3 for every request. Used in tests and local
// development when Piper is not installed.
type MockEngine struct {
	MP3 []byte
}

// LoadFixtureMP3 reads the first existing path as raw MP3 bytes.
func LoadFixtureMP3(paths ...string) ([]byte, error) {
	var last error
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err == nil && len(b) > 0 {
			return b, nil
		}
		last = err
	}
	if last == nil {
		last = fmt.Errorf("no fixture paths provided")
	}
	return nil, last
}

func (m *MockEngine) Synthesize(_ context.Context, text, _ string) (*Result, error) {
	if text == "" {
		return nil, fmt.Errorf("empty text")
	}
	payload := m.MP3
	if len(payload) == 0 {
		payload = defaultMockMP3()
	}
	if len(payload) == 0 {
		return nil, fmt.Errorf("mock tts has no fixture audio")
	}
	return &Result{
		Reader:          io.NopCloser(bytes.NewReader(payload)),
		ContentType:     "audio/mpeg",
		Extension:       "mp3",
		DurationSeconds: mp3DurationSeconds(payload),
	}, nil
}
