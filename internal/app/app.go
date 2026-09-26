package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/tvaroska/podcaster/internal/config"
	"github.com/tvaroska/podcaster/internal/episode"
	"github.com/tvaroska/podcaster/internal/job"
	"github.com/tvaroska/podcaster/internal/storage"
	"github.com/tvaroska/podcaster/internal/store"
	"github.com/tvaroska/podcaster/internal/tts"
	"github.com/tvaroska/podcaster/internal/worker"
)

// App is the control-plane core shared by REST and MCP.
type App struct {
	Cfg      *config.Config
	Store    store.Store
	Storage  storage.Storage
	Jobs     job.Dispatcher
	Worker   *worker.Worker
	Engine   tts.Engine
	Log      *slog.Logger
	closers  []io.Closer
	localJob *job.LocalDispatcher
}

func (a *App) logger() *slog.Logger {
	if a.Log != nil {
		return a.Log
	}
	return slog.Default()
}

// CreateEpisode validates, persists a PENDING record, and enqueues TTS.
func (a *App) CreateEpisode(ctx context.Context, in episode.CreateInput) (*episode.Episode, error) {
	in.Title = strings.TrimSpace(in.Title)
	in.Content = strings.TrimSpace(in.Content)
	in.Category = strings.TrimSpace(in.Category)
	in.VoiceID = strings.TrimSpace(in.VoiceID)
	if err := episode.ValidateCreate(in, a.Cfg.Limits(), a.Cfg.VoiceAllow); err != nil {
		return nil, err
	}
	if in.VoiceID == "" {
		in.VoiceID = a.Cfg.DefaultVoice
	}
	now := time.Now().UTC()
	ep := &episode.Episode{
		ID:         episode.NewID(),
		Title:      in.Title,
		ScriptText: in.Content,
		Category:   in.Category,
		VoiceID:    in.VoiceID,
		Status:     episode.StatusPending,
		CreatedAt:  now,
	}
	if err := a.Store.Create(ctx, ep); err != nil {
		return nil, fmt.Errorf("persist episode: %w", err)
	}
	if err := a.Jobs.Enqueue(ctx, ep.ID); err != nil {
		ep.Status = episode.StatusFailed
		ep.ErrorMessage = "failed to enqueue synthesis job: " + err.Error()
		_ = a.Store.Update(ctx, ep)
		return nil, fmt.Errorf("enqueue job: %w", err)
	}
	a.logger().Info("episode queued", "episode_id", ep.ID, "title", ep.Title)
	return ep, nil
}

// Reconcile sweeps the store for stranded episodes across server restarts or worker crashes.
// Episodes stuck in PROCESSING are reset to PENDING, and all PENDING episodes are re-enqueued.
func (a *App) Reconcile(ctx context.Context) error {
	log := a.logger()
	if a.Store == nil {
		return nil
	}

	// 1. Recover episodes stuck in PROCESSING (e.g. server crash or unhandled kill)
	procList, err := a.Store.List(ctx, episode.ListFilter{
		Status: episode.StatusProcessing,
		Limit:  100,
	})
	if err != nil {
		log.Warn("reconcile: list PROCESSING episodes failed", "err", err)
	} else {
		for _, ep := range procList {
			log.Info("reconcile: resetting stranded PROCESSING episode to PENDING", "episode_id", ep.ID)
			_, casErr := a.Store.CompareAndSwapStatus(ctx, ep.ID, episode.StatusProcessing, episode.StatusPending)
			if casErr != nil && !errors.Is(casErr, store.ErrConflict) {
				log.Warn("reconcile: failed to reset PROCESSING episode", "episode_id", ep.ID, "err", casErr)
			}
		}
	}

	// 2. Re-enqueue episodes in PENDING dropped from memory or waiting for synthesis
	if a.Jobs != nil {
		pendList, err := a.Store.List(ctx, episode.ListFilter{
			Status: episode.StatusPending,
			Limit:  100,
		})
		if err != nil {
			log.Warn("reconcile: list PENDING episodes failed", "err", err)
		} else {
			for _, ep := range pendList {
				log.Info("reconcile: re-enqueueing PENDING episode", "episode_id", ep.ID)
				if qerr := a.Jobs.Enqueue(ctx, ep.ID); qerr != nil {
					log.Warn("reconcile: failed to enqueue episode", "episode_id", ep.ID, "err", qerr)
				}
			}
		}
	}
	return nil
}

func (a *App) AddCloser(c io.Closer) {
	if c != nil {
		a.closers = append(a.closers, c)
	}
}

func (a *App) Close() error {
	if a.localJob != nil {
		a.localJob.Close()
	}
	var first error
	for i := len(a.closers) - 1; i >= 0; i-- {
		if err := a.closers[i].Close(); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// Options controls optional bootstrap behaviour.
type Options struct {
	DisableDispatcher bool
}

// Open constructs store, storage, TTS, and the job dispatcher from config.
func Open(ctx context.Context, cfg *config.Config, log *slog.Logger) (*App, error) {
	return OpenWithOptions(ctx, cfg, log, Options{})
}

func OpenWithOptions(ctx context.Context, cfg *config.Config, log *slog.Logger, opts Options) (*App, error) {
	if log == nil {
		log = slog.Default()
	}
	a := &App{Cfg: cfg, Log: log}

	st, err := openStore(ctx, cfg)
	if err != nil {
		return nil, err
	}
	a.Store = st
	a.AddCloser(st)

	blob, err := openStorage(ctx, cfg)
	if err != nil {
		_ = a.Close()
		return nil, err
	}
	a.Storage = blob
	if c, ok := blob.(io.Closer); ok {
		a.AddCloser(c)
	}

	eng, err := openEngine(cfg)
	if err != nil {
		_ = a.Close()
		return nil, err
	}
	a.Engine = eng

	a.Worker = &worker.Worker{
		Store:        a.Store,
		Storage:      a.Storage,
		Engine:       a.Engine,
		FFmpegBin:    cfg.FFmpegBin,
		DefaultVoice: cfg.DefaultVoice,
		Log:          log,
	}

	if !opts.DisableDispatcher {
		disp, err := a.openDispatcher(ctx, cfg)
		if err != nil {
			_ = a.Close()
			return nil, err
		}
		a.Jobs = disp
		if c, ok := disp.(io.Closer); ok {
			a.AddCloser(c)
		}
	}
	return a, nil
}

func openStore(ctx context.Context, cfg *config.Config) (store.Store, error) {
	switch cfg.StoreBackend {
	case config.BackendSQLite:
		return store.OpenSQLite(cfg.SQLitePath)
	case config.BackendFirestore:
		return store.OpenFirestore(ctx, cfg.GCPProject)
	default:
		return nil, fmt.Errorf("unknown store backend %q", cfg.StoreBackend)
	}
}

func openStorage(ctx context.Context, cfg *config.Config) (storage.Storage, error) {
	switch cfg.StorageBackend {
	case config.BackendLocal:
		return storage.OpenLocal(cfg.LocalDataDir)
	case config.BackendGCS:
		return storage.OpenGCS(ctx, cfg.GCSBucket)
	default:
		return nil, fmt.Errorf("unknown storage backend %q", cfg.StorageBackend)
	}
}

func openEngine(cfg *config.Config) (tts.Engine, error) {
	switch cfg.TTSEngine {
	case config.EngineMock:
		return &tts.MockEngine{}, nil
	case config.EnginePiper:
		return &tts.PiperEngine{
			Bin:          cfg.PiperBin,
			Model:        cfg.PiperModel,
			Config:       cfg.PiperConfig,
			FFmpegBin:    cfg.FFmpegBin,
			DefaultVoice: cfg.DefaultVoice,
		}, nil
	default:
		return nil, fmt.Errorf("unknown tts engine %q", cfg.TTSEngine)
	}
}

func (a *App) openDispatcher(ctx context.Context, cfg *config.Config) (job.Dispatcher, error) {
	switch cfg.JobBackend {
	case config.BackendLocal:
		d := job.NewLocal(a.Worker.Process, 1, cfg.WorkerTimeout, a.Log)
		a.localJob = d
		return d, nil
	case config.BackendCloudRun:
		return job.NewCloudRun(ctx, cfg.GCPProject, cfg.CloudRunRegion, cfg.CloudRunJob)
	default:
		return nil, fmt.Errorf("unknown job backend %q", cfg.JobBackend)
	}
}
