package episode

import (
	"bytes"
	"encoding/base64"
	"errors"
	"image"
	"image/jpeg"
	"image/png"
	"strings"
	"testing"
)

func samplePNGDataURI(t *testing.T) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())
}

func sampleJPEGDataURI(t *testing.T) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatal(err)
	}
	return "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())
}

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

	t.Run("voice traversal rejected without allow-list", func(t *testing.T) {
		for _, bad := range []string{"../evil", "dir/voice", `dir\voice`} {
			in := ok
			in.VoiceID = bad
			err := ValidateCreate(in, Limits{}, nil)
			if !errors.Is(err, ErrInvalidVoice) {
				t.Fatalf("voice %q: got %v, want ErrInvalidVoice", bad, err)
			}
		}
	})

	t.Run("category validation", func(t *testing.T) {
		in := ok
		in.Category = strings.Repeat("c", DefaultMaxTitleLength+1)
		if err := ValidateCreate(in, Limits{}, nil); err == nil {
			t.Fatal("expected error for overlong category")
		}
		in.Category = "bad\xffutf8"
		if err := ValidateCreate(in, Limits{}, nil); err == nil {
			t.Fatal("expected error for invalid UTF-8 category")
		}
	})

	t.Run("description image_url and chapters validation", func(t *testing.T) {
		in := ok
		in.Description = "Show notes with links"
		in.ImageURL = "https://example.com/art.png"
		in.Chapters = []Chapter{
			{StartSeconds: 0, Title: "Intro", URL: "https://example.com/intro", ImageURL: "https://example.com/ch1.png"},
			{StartSeconds: 45.5, Title: "Deep Dive"},
		}
		if err := ValidateCreate(in, Limits{}, nil); err != nil {
			t.Fatalf("unexpected error for valid metadata: %v", err)
		}

		badDesc := ok
		badDesc.Description = "bad\xff"
		if err := ValidateCreate(badDesc, Limits{}, nil); err == nil {
			t.Fatal("expected error for invalid UTF-8 description")
		}

		badImg := ok
		badImg.ImageURL = "ftp://example.com/art.png"
		if err := ValidateCreate(badImg, Limits{}, nil); err == nil {
			t.Fatal("expected error for non-http image_url")
		}

		inlinePNG := ok
		inlinePNG.ImageURL = samplePNGDataURI(t)
		if err := ValidateCreate(inlinePNG, Limits{}, nil); err != nil {
			t.Fatalf("unexpected error for inline PNG image_url: %v", err)
		}

		inlineJPEG := ok
		inlineJPEG.ImageURL = sampleJPEGDataURI(t)
		if err := ValidateCreate(inlineJPEG, Limits{}, nil); err != nil {
			t.Fatalf("unexpected error for inline JPEG image_url: %v", err)
		}

		badInline := ok
		badInline.ImageURL = "data:image/png;base64,not-an-image"
		if err := ValidateCreate(badInline, Limits{}, nil); err == nil {
			t.Fatal("expected error for invalid inline image_url")
		}

		badChapNeg := ok
		badChapNeg.Chapters = []Chapter{{StartSeconds: -1, Title: "Neg"}}
		if err := ValidateCreate(badChapNeg, Limits{}, nil); err == nil {
			t.Fatal("expected error for negative chapter start_seconds")
		}

		badChapTitle := ok
		badChapTitle.Chapters = []Chapter{{StartSeconds: 0, Title: "   "}}
		if err := ValidateCreate(badChapTitle, Limits{}, nil); err == nil {
			t.Fatal("expected error for empty chapter title")
		}

		badChapURL := ok
		badChapURL.Chapters = []Chapter{{StartSeconds: 10, Title: "Link", URL: "javascript:alert(1)"}}
		if err := ValidateCreate(badChapURL, Limits{}, nil); err == nil {
			t.Fatal("expected error for non-http chapter url")
		}
	})
}

func TestValidateUpdate(t *testing.T) {
	strPtr := func(s string) *string { return &s }

	if err := ValidateUpdate(UpdateInput{}, Limits{}); err != nil {
		t.Fatalf("empty UpdateInput should be valid: %v", err)
	}
	chaps := []Chapter{{StartSeconds: 0, Title: "Part 1"}}
	if err := ValidateUpdate(UpdateInput{
		Title:       strPtr("Updated Title"),
		Description: strPtr("Updated notes"),
		Category:    strPtr("news"),
		ImageURL:    strPtr("https://example.com/ep.png"),
		Chapters:    &chaps,
	}, Limits{}); err != nil {
		t.Fatalf("valid UpdateInput failed: %v", err)
	}
	if err := ValidateUpdate(UpdateInput{ImageURL: strPtr("")}, Limits{}); err != nil {
		t.Fatalf("empty ImageURL should be valid to clear icon: %v", err)
	}
	if err := ValidateUpdate(UpdateInput{ImageURL: strPtr(samplePNGDataURI(t))}, Limits{}); err != nil {
		t.Fatalf("inline PNG ImageURL should be valid on update: %v", err)
	}

	var verr *ValidationError
	if err := ValidateUpdate(UpdateInput{Title: strPtr("")}, Limits{}); !errors.As(err, &verr) || verr.Field != "title" {
		t.Fatalf("expected title ValidationError, got %v", err)
	}
	if err := ValidateUpdate(UpdateInput{ImageURL: strPtr("bad-url")}, Limits{}); !errors.As(err, &verr) || verr.Field != "image_url" {
		t.Fatalf("expected image_url ValidationError, got %v", err)
	}
	if err := ValidateUpdate(UpdateInput{ImageURL: strPtr("data:image/png;base64,YWJj")}, Limits{}); !errors.As(err, &verr) || verr.Field != "image_url" {
		t.Fatalf("expected image_url ValidationError for non-PNG data URI, got %v", err)
	}
	badChaps := []Chapter{{StartSeconds: -5, Title: "Bad"}}
	if err := ValidateUpdate(UpdateInput{Chapters: &badChaps}, Limits{}); !errors.As(err, &verr) || verr.Field != "chapters" {
		t.Fatalf("expected chapters ValidationError, got %v", err)
	}
}

func TestCoverPath(t *testing.T) {
	defEp := &Episode{ID: "ep_123"}
	if got := defEp.CoverPath(); got != "/episodes/ep_123/cover.png" {
		t.Fatalf("got %q, want /episodes/ep_123/cover.png", got)
	}
	podEp := &Episode{ID: "ep_123", PodcastID: "alice"}
	if got := podEp.CoverPath(); got != "/p/alice/episodes/ep_123/cover.png" {
		t.Fatalf("got %q, want /p/alice/episodes/ep_123/cover.png", got)
	}
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
