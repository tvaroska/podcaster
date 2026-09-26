package episode

import (
	"errors"
	"strings"
	"testing"
)

func TestValidateCreate(t *testing.T) {
	ok := CreateInput{
		Title:   "Morning Briefing",
		Content: "Good morning. Here are your top updates for today.",
	}

	t.Run("ok", func(t *testing.T) {
		if err := ValidateCreate(ok, Limits{}, nil); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("missing title", func(t *testing.T) {
		in := ok
		in.Title = "  "
		err := ValidateCreate(in, Limits{}, nil)
		if !errors.Is(err, ErrInvalidTitle) {
			t.Fatalf("got %v, want ErrInvalidTitle", err)
		}
	})

	t.Run("short content", func(t *testing.T) {
		in := ok
		in.Content = "hi"
		err := ValidateCreate(in, Limits{}, nil)
		if !errors.Is(err, ErrInvalidContent) {
			t.Fatalf("got %v, want ErrInvalidContent", err)
		}
	})

	t.Run("long title", func(t *testing.T) {
		in := ok
		in.Title = strings.Repeat("a", DefaultMaxTitleLength+1)
		err := ValidateCreate(in, Limits{}, nil)
		if !errors.Is(err, ErrInvalidTitle) {
			t.Fatalf("got %v, want ErrInvalidTitle", err)
		}
	})

	t.Run("voice allow-list", func(t *testing.T) {
		in := ok
		in.VoiceID = "nope"
		err := ValidateCreate(in, Limits{}, []string{"en_US-lessac-medium"})
		if !errors.Is(err, ErrInvalidVoice) {
			t.Fatalf("got %v, want ErrInvalidVoice", err)
		}
	})

	t.Run("voice allowed", func(t *testing.T) {
		in := ok
		in.VoiceID = "en_US-lessac-medium"
		if err := ValidateCreate(in, Limits{}, []string{"en_US-lessac-medium"}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

func TestNewID(t *testing.T) {
	a, b := NewID(), NewID()
	if a == b {
		t.Fatal("expected unique ids")
	}
	if !strings.HasPrefix(a, "ep_") || len(a) != 19 {
		t.Fatalf("unexpected id format: %q", a)
	}
}
