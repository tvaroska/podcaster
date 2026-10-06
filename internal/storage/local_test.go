package storage

import (
	"bytes"
	"context"
	"errors"
	"io"
	"sync"
	"testing"
)

func TestLocalStorage(t *testing.T) {
	ctx := context.Background()
	s, err := OpenLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	key := "audio/ep_1.mp3"
	payload := []byte("not-really-mp3-but-fine")
	if err := s.Put(ctx, key, bytes.NewReader(payload), int64(len(payload)), "audio/mpeg"); err != nil {
		t.Fatal(err)
	}
	meta, err := s.Stat(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	if meta.Size != int64(len(payload)) || meta.ContentType != "audio/mpeg" {
		t.Fatalf("meta %+v", meta)
	}
	obj, err := s.Open(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(obj)
	_ = obj.Close()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("got %q", got)
	}
	if err := s.Delete(ctx, key); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Stat(ctx, key); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected not found, got %v", err)
	}
	if err := s.Put(ctx, "../etc/passwd", bytes.NewReader(payload), int64(len(payload)), ""); err == nil {
		t.Fatal("expected path traversal to fail")
	}
}

func TestLocalStorageConcurrentPut(t *testing.T) {
	ctx := context.Background()
	s, err := OpenLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	key := "audio/ep_concurrent.mp3"
	payload := bytes.Repeat([]byte("concurrent-audio-data-"), 512)

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.Put(ctx, key, bytes.NewReader(payload), int64(len(payload)), "audio/mpeg"); err != nil {
				t.Errorf("concurrent Put failed: %v", err)
			}
		}()
	}
	wg.Wait()

	obj, err := s.Open(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	defer obj.Close()
	got, err := io.ReadAll(obj)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("stored payload mismatch: got %d bytes, want %d bytes", len(got), len(payload))
	}
}
