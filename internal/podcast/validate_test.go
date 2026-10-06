package podcast

import (
	"bytes"
	"encoding/base64"
	"errors"
	"image"
	"image/jpeg"
	"image/png"
	"testing"

	"github.com/tvaroska/podcaster/internal/episode"
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
	if err := ValidateCreate(CreateInput{ID: "alice", Title: "Alice"}); err != nil {
		t.Fatal(err)
	}
	cases := []CreateInput{
		{ID: "", Title: "x"},
		{ID: "A", Title: "x"},
		{ID: "1alice", Title: "x"},
		{ID: "al", Title: ""},
		{ID: "v1", Title: "x"},
		{ID: "alice-", Title: "x"},
		{ID: "al--ice", Title: "x"},
	}
	for _, in := range cases {
		if err := ValidateCreate(in); err == nil {
			t.Fatalf("expected error for %+v", in)
		}
	}

	err := ValidateCreate(CreateInput{ID: "alice", Title: "Alice", Description: "bad\xff"})
	var verr *episode.ValidationError
	if !errors.As(err, &verr) || verr.Field != "description" {
		t.Fatalf("expected description ValidationError, got %v", err)
	}

	err = ValidateCreate(CreateInput{ID: "alice", Title: "Alice", Author: "bad\xff"})
	if !errors.As(err, &verr) || verr.Field != "author" {
		t.Fatalf("expected author ValidationError, got %v", err)
	}

	if err := ValidateCreate(CreateInput{ID: "alice", Title: "Alice", ImageURL: "https://example.com/icon.png"}); err != nil {
		t.Fatalf("unexpected error for valid ImageURL: %v", err)
	}
	if err := ValidateCreate(CreateInput{ID: "alice", Title: "Alice", ImageURL: samplePNGDataURI(t)}); err != nil {
		t.Fatalf("unexpected error for valid inline PNG ImageURL: %v", err)
	}
	if err := ValidateCreate(CreateInput{ID: "alice", Title: "Alice", ImageURL: sampleJPEGDataURI(t)}); err != nil {
		t.Fatalf("unexpected error for valid inline JPEG ImageURL: %v", err)
	}
	err = ValidateCreate(CreateInput{ID: "alice", Title: "Alice", ImageURL: "data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte("not-a-png"))})
	if !errors.As(err, &verr) || verr.Field != "image_url" {
		t.Fatalf("expected image_url ValidationError for non-image base64 payload, got %v", err)
	}
	err = ValidateCreate(CreateInput{ID: "alice", Title: "Alice", ImageURL: "data:image/svg+xml;base64,PHN2Zz4="})
	if !errors.As(err, &verr) || verr.Field != "image_url" {
		t.Fatalf("expected image_url ValidationError for unsupported media type, got %v", err)
	}
	err = ValidateCreate(CreateInput{ID: "alice", Title: "Alice", ImageURL: "ftp://example.com/icon.png"})
	if !errors.As(err, &verr) || verr.Field != "image_url" {
		t.Fatalf("expected image_url ValidationError, got %v", err)
	}
	err = ValidateCreate(CreateInput{ID: "alice", Title: "Alice", ImageURL: "https://"})
	if !errors.As(err, &verr) || verr.Field != "image_url" {
		t.Fatalf("expected image_url ValidationError for missing host, got %v", err)
	}
}

func TestValidateUpdate(t *testing.T) {
	strPtr := func(s string) *string { return &s }

	if err := ValidateUpdate(UpdateInput{}); err != nil {
		t.Fatalf("empty UpdateInput should be valid: %v", err)
	}
	if err := ValidateUpdate(UpdateInput{
		Title:       strPtr("New Title"),
		Description: strPtr("New Description"),
		Author:      strPtr("New Author"),
		ImageURL:    strPtr("https://example.com/new.png"),
	}); err != nil {
		t.Fatalf("valid UpdateInput failed: %v", err)
	}
	if err := ValidateUpdate(UpdateInput{ImageURL: strPtr(samplePNGDataURI(t))}); err != nil {
		t.Fatalf("valid inline PNG UpdateInput failed: %v", err)
	}
	if err := ValidateUpdate(UpdateInput{ImageURL: strPtr("")}); err != nil {
		t.Fatalf("clearing ImageURL with empty string should be valid: %v", err)
	}

	var verr *episode.ValidationError
	if err := ValidateUpdate(UpdateInput{Title: strPtr("   ")}); !errors.As(err, &verr) || verr.Field != "title" {
		t.Fatalf("expected title ValidationError for blank title, got %v", err)
	}
	if err := ValidateUpdate(UpdateInput{Description: strPtr("bad\xff")}); !errors.As(err, &verr) || verr.Field != "description" {
		t.Fatalf("expected description ValidationError, got %v", err)
	}
	if err := ValidateUpdate(UpdateInput{Author: strPtr("bad\xff")}); !errors.As(err, &verr) || verr.Field != "author" {
		t.Fatalf("expected author ValidationError, got %v", err)
	}
	if err := ValidateUpdate(UpdateInput{ImageURL: strPtr("not-a-url")}); !errors.As(err, &verr) || verr.Field != "image_url" {
		t.Fatalf("expected image_url ValidationError, got %v", err)
	}
}

func TestNewSubmitKey(t *testing.T) {
	k1, err := NewSubmitKey()
	if err != nil {
		t.Fatalf("NewSubmitKey: %v", err)
	}
	k2, err := NewSubmitKey()
	if err != nil {
		t.Fatalf("NewSubmitKey: %v", err)
	}
	if len(k1) != 32 || len(k2) != 32 {
		t.Fatalf("expected 32-char hex submit keys, got %d and %d", len(k1), len(k2))
	}
	if k1 == k2 {
		t.Fatal("expected distinct random submit keys")
	}
}
