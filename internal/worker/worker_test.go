package worker

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tvaroska/podcaster/internal/episode"
	"github.com/tvaroska/podcaster/internal/storage"
	"github.com/tvaroska/podcaster/internal/store"
	"github.com/tvaroska/podcaster/internal/tts"
)

func TestProcessPublishes(t *testing.T) {
	ctx := context.Background()
	st, err := store.OpenSQLite(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	blob, err := storage.OpenLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	mp3, err := os.ReadFile(findBeep(t))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	ep := &episode.Episode{
		ID:         "ep_test",
		Title:      "Hello",
		ScriptText: "Good morning. Here are your top updates for today.",
		Status:     episode.StatusPending,
		CreatedAt:  now,
	}
	if err := st.Create(ctx, ep); err != nil {
		t.Fatal(err)
	}

	w := &Worker{
		Store:        st,
		Storage:      blob,
		Engine:       &tts.MockEngine{MP3: mp3},
		FFmpegBin:    "ffmpeg",
		DefaultVoice: "en_US-lessac-medium",
	}
	if err := w.Process(ctx, ep.ID); err != nil {
		t.Fatal(err)
	}
	got, err := st.Get(ctx, ep.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != episode.StatusReady {
		t.Fatalf("status %s", got.Status)
	}
	if got.AudioURI != "audio/ep_test.mp3" || got.FileSizeBytes == 0 || got.PublishedAt == nil {
		t.Fatalf("episode %+v", got)
	}
	obj, err := blob.Open(ctx, got.AudioURI)
	if err != nil {
		t.Fatal(err)
	}
	defer obj.Close()
	body, _ := io.ReadAll(obj)
	if !bytes.Equal(body, mp3) {
		t.Fatal("stored audio mismatch")
	}

	// Re-processing a READY episode is a no-op.
	if err := w.Process(ctx, ep.ID); err != nil {
		t.Fatal(err)
	}
}

func TestProcessMarksFailed(t *testing.T) {
	ctx := context.Background()
	st, err := store.OpenSQLite(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	blob, err := storage.OpenLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ep := &episode.Episode{
		ID:         "ep_fail",
		Title:      "Hello",
		ScriptText: "Good morning. Here are your top updates for today.",
		Status:     episode.StatusPending,
		CreatedAt:  time.Now().UTC(),
	}
	if err := st.Create(ctx, ep); err != nil {
		t.Fatal(err)
	}
	w := &Worker{
		Store:   st,
		Storage: blob,
		Engine:  failingEngine{},
	}
	if err := w.Process(ctx, ep.ID); err == nil {
		t.Fatal("expected error")
	}
	got, _ := st.Get(ctx, ep.ID)
	if got.Status != episode.StatusFailed || got.ErrorMessage == "" {
		t.Fatalf("got %+v", got)
	}
}

type failingEngine struct{}

func (failingEngine) Synthesize(context.Context, string, string) (*tts.Result, error) {
	return nil, io.ErrUnexpectedEOF
}

func TestProcessRetryFailed(t *testing.T) {
	ctx := context.Background()
	st, err := store.OpenSQLite(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	blob, err := storage.OpenLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	mp3, err := os.ReadFile(findBeep(t))
	if err != nil {
		t.Fatal(err)
	}

	// Create an episode in StatusFailed (as if a previous attempt failed)
	ep := &episode.Episode{
		ID:           "ep_retry",
		Title:        "Retry Me",
		ScriptText:   "Retrying synthesis.",
		Status:       episode.StatusFailed,
		ErrorMessage: "previous transient failure",
		CreatedAt:    time.Now().UTC(),
	}
	if err := st.Create(ctx, ep); err != nil {
		t.Fatal(err)
	}

	w := &Worker{
		Store:        st,
		Storage:      blob,
		Engine:       &tts.MockEngine{MP3: mp3},
		FFmpegBin:    "ffmpeg",
		DefaultVoice: "en_US-lessac-medium",
	}

	// Processing a failed episode should succeed on retry!
	if err := w.Process(ctx, ep.ID); err != nil {
		t.Fatalf("expected retry to succeed, got: %v", err)
	}

	got, err := st.Get(ctx, ep.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != episode.StatusReady {
		t.Fatalf("expected status READY, got %s", got.Status)
	}
}

func TestProcessReclaimProcessingOnTaskRetry(t *testing.T) {
	ctx := context.Background()
	st, err := store.OpenSQLite(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	blob, err := storage.OpenLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	mp3, err := os.ReadFile(findBeep(t))
	if err != nil {
		t.Fatal(err)
	}

	// Create an episode in StatusProcessing (as if previous container crashed)
	ep := &episode.Episode{
		ID:         "ep_crashed",
		Title:      "Crashed Task",
		ScriptText: "Reclaiming crashed task.",
		Status:     episode.StatusProcessing,
		CreatedAt:  time.Now().UTC(),
	}
	if err := st.Create(ctx, ep); err != nil {
		t.Fatal(err)
	}

	w := &Worker{
		Store:        st,
		Storage:      blob,
		Engine:       &tts.MockEngine{MP3: mp3},
		FFmpegBin:    "ffmpeg",
		DefaultVoice: "en_US-lessac-medium",
	}

	// Without retry environment variable, it skips to avoid collision
	if err := w.Process(ctx, ep.ID); err != nil {
		t.Fatal(err)
	}
	got, _ := st.Get(ctx, ep.ID)
	if got.Status != episode.StatusProcessing {
		t.Fatalf("expected status to remain PROCESSING, got %s", got.Status)
	}

	// With CLOUD_RUN_TASK_ATTEMPT=1, it reclaims and finishes!
	t.Setenv("CLOUD_RUN_TASK_ATTEMPT", "1")
	if err := w.Process(ctx, ep.ID); err != nil {
		t.Fatalf("expected reclaim to succeed, got: %v", err)
	}
	got, _ = st.Get(ctx, ep.ID)
	if got.Status != episode.StatusReady {
		t.Fatalf("expected status READY, got %s", got.Status)
	}
}

func findBeep(t *testing.T) string {
	t.Helper()
	candidates := []string{
		filepath.Join("..", "..", "testdata", "beep.mp3"),
		filepath.Join("testdata", "beep.mp3"),
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	t.Fatal("beep.mp3 not found")
	return ""
}

type cancelOnSynthesizeEngine struct {
	cancel context.CancelFunc
}

func (e cancelOnSynthesizeEngine) Synthesize(ctx context.Context, _ string, _ string) (*tts.Result, error) {
	e.cancel()
	return nil, ctx.Err()
}

func TestProcessMarksFailedOnCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	st, err := store.OpenSQLite(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	blob, err := storage.OpenLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ep := &episode.Episode{
		ID:         "ep_cancel",
		Title:      "Canceled",
		ScriptText: "Good morning. This synthesis will time out.",
		Status:     episode.StatusPending,
		CreatedAt:  time.Now().UTC(),
	}
	if err := st.Create(context.Background(), ep); err != nil {
		t.Fatal(err)
	}
	w := &Worker{
		Store:   st,
		Storage: blob,
		Engine:  cancelOnSynthesizeEngine{cancel: cancel},
	}
	if err := w.Process(ctx, ep.ID); err == nil {
		t.Fatal("expected error from canceled context")
	}
	got, err := st.Get(context.Background(), ep.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != episode.StatusFailed || got.ErrorMessage == "" {
		t.Fatalf("expected FAILED status with error message, got %+v", got)
	}
}

type zeroDurationEngine struct {
	mp3 []byte
}

func (e zeroDurationEngine) Synthesize(_ context.Context, _ string, _ string) (*tts.Result, error) {
	return &tts.Result{
		Reader:          io.NopCloser(bytes.NewReader(e.mp3)),
		ContentType:     "audio/mpeg",
		Extension:       "mp3",
		DurationSeconds: 0,
	}, nil
}

func TestProcessFallbackMP3Duration(t *testing.T) {
	ctx := context.Background()
	st, err := store.OpenSQLite(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	blob, err := storage.OpenLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	mp3, err := os.ReadFile(findBeep(t))
	if err != nil {
		t.Fatal(err)
	}
	ep := &episode.Episode{
		ID:         "ep_dur_fallback",
		Title:      "Duration Fallback",
		ScriptText: "Good morning. Testing pure-Go MP3 duration fallback.",
		Status:     episode.StatusPending,
		CreatedAt:  time.Now().UTC(),
	}
	if err := st.Create(ctx, ep); err != nil {
		t.Fatal(err)
	}
	w := &Worker{
		Store:     st,
		Storage:   blob,
		Engine:    zeroDurationEngine{mp3: mp3},
		FFmpegBin: "/nonexistent/ffmpeg",
	}
	if err := w.Process(ctx, ep.ID); err != nil {
		t.Fatal(err)
	}
	got, err := st.Get(ctx, ep.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.DurationSeconds <= 0 {
		t.Fatalf("expected positive fallback duration, got %v", got.DurationSeconds)
	}
}
