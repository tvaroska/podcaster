package worker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	"github.com/tvaroska/podcaster/internal/episode"
	"github.com/tvaroska/podcaster/internal/storage"
	"github.com/tvaroska/podcaster/internal/store"
	"github.com/tvaroska/podcaster/internal/tts"
)

// Worker synthesizes an episode and publishes the enclosure.
type Worker struct {
	Store                  store.Store
	Storage                storage.Storage
	Engine                 tts.Engine
	FFmpegBin              string
	DefaultVoice           string
	AllowReclaimProcessing bool
	Log                    *slog.Logger
}

func (w *Worker) logger() *slog.Logger {
	if w.Log != nil {
		return w.Log
	}
	return slog.Default()
}

// claim checks status and transitions the episode to PROCESSING, allowing
// retries if the episode previously failed or was interrupted.
func (w *Worker) claim(ctx context.Context, id string, log *slog.Logger) (*episode.Episode, error) {
	// First, attempt standard transition from PENDING -> PROCESSING.
	ep, err := w.Store.CompareAndSwapStatus(ctx, id, episode.StatusPending, episode.StatusProcessing)
	if err == nil {
		return ep, nil
	}
	if !errors.Is(err, store.ErrConflict) {
		return nil, fmt.Errorf("claim episode: %w", err)
	}

	// Status was not PENDING. Load current episode state.
	if ep == nil {
		var gerr error
		ep, gerr = w.Store.Get(ctx, id)
		if gerr != nil {
			return nil, fmt.Errorf("get episode: %w", gerr)
		}
	}

	switch ep.Status {
	case episode.StatusReady:
		log.Info("skip episode already READY")
		return nil, nil

	case episode.StatusFailed:
		log.Info("retrying failed episode", "prev_error", ep.ErrorMessage)
		claimed, err := w.Store.CompareAndSwapStatus(ctx, id, episode.StatusFailed, episode.StatusProcessing)
		if err != nil {
			if errors.Is(err, store.ErrConflict) {
				log.Info("conflict retrying failed episode; skipping", "status", ep.Status)
				return nil, nil
			}
			return nil, fmt.Errorf("claim failed episode for retry: %w", err)
		}
		return claimed, nil

	case episode.StatusProcessing:
		// If running under Cloud Run Jobs with task retry attempt > 0, or if worker allows reclaiming PROCESSING:
		attempt := os.Getenv("CLOUD_RUN_TASK_ATTEMPT")
		if (attempt != "" && attempt != "0") || w.AllowReclaimProcessing {
			log.Info("reclaiming PROCESSING episode on task retry", "attempt", attempt)
			return ep, nil
		}
		log.Info("skip episode already in PROCESSING by another worker")
		return nil, nil

	default:
		log.Info("skip episode not in claimable state", "status", ep.Status)
		return nil, nil
	}
}

// Process fetches the episode, synthesizes audio, stores it, and marks READY.
func (w *Worker) Process(ctx context.Context, id string) error {
	log := w.logger().With("episode_id", id)
	ep, err := w.claim(ctx, id, log)
	if err != nil {
		return err
	}
	if ep == nil {
		return nil
	}

	voice := ep.VoiceID
	if voice == "" {
		voice = w.DefaultVoice
	}

	result, err := w.Engine.Synthesize(ctx, ep.ScriptText, voice)
	if err != nil {
		return w.fail(ctx, ep, fmt.Errorf("synthesize: %w", err))
	}
	defer result.Close()

	ext := result.Extension
	if ext == "" {
		ext = "mp3"
	}
	key := episode.AudioObjectKey(ep.ID, ext)

	tmp, err := os.CreateTemp("", "podcaster-audio-*."+ext)
	if err != nil {
		return w.fail(ctx, ep, err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	n, err := io.Copy(tmp, result.Reader)
	closeErr := tmp.Close()
	if err != nil {
		return w.fail(ctx, ep, err)
	}
	if closeErr != nil {
		return w.fail(ctx, ep, closeErr)
	}

	duration := result.DurationSeconds
	if duration <= 0 && w.FFmpegBin != "" {
		if d, perr := tts.ProbeDuration(ctx, w.FFmpegBin, tmpName); perr == nil {
			duration = d
		} else {
			log.Warn("duration probe failed", "err", perr)
		}
	}

	f, err := os.Open(tmpName)
	if err != nil {
		return w.fail(ctx, ep, err)
	}
	defer f.Close()
	if err := w.Storage.Put(ctx, key, f, n, result.ContentType); err != nil {
		return w.fail(ctx, ep, fmt.Errorf("store audio: %w", err))
	}

	now := time.Now().UTC()
	ep.Status = episode.StatusReady
	ep.AudioURI = key
	ep.FileSizeBytes = n
	ep.ContentType = result.ContentType
	ep.DurationSeconds = duration
	ep.PublishedAt = &now
	ep.ErrorMessage = ""
	if err := w.Store.Update(ctx, ep); err != nil {
		return fmt.Errorf("mark ready: %w", err)
	}
	log.Info("episode published", "bytes", n, "duration", duration, "key", key)
	return nil
}

func (w *Worker) fail(ctx context.Context, ep *episode.Episode, cause error) error {
	ep.Status = episode.StatusFailed
	ep.ErrorMessage = cause.Error()
	if err := w.Store.Update(ctx, ep); err != nil {
		w.logger().Error("failed to persist FAILED status", "episode_id", ep.ID, "err", err, "cause", cause)
	}
	return cause
}
