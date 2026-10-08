package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/tvaroska/podcaster/internal/auth"
	"github.com/tvaroska/podcaster/internal/config"
	"github.com/tvaroska/podcaster/internal/cover"
	"github.com/tvaroska/podcaster/internal/episode"
	"github.com/tvaroska/podcaster/internal/job"
	"github.com/tvaroska/podcaster/internal/podcast"
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

func (a *App) baseURL() string {
	if a.Cfg != nil {
		return a.Cfg.PublicBaseURL
	}
	return ""
}

// CreateEpisode validates, persists a PENDING record, and enqueues TTS.
func (a *App) CreateEpisode(ctx context.Context, in episode.CreateInput) (*episode.Episode, error) {
	in.Title = strings.TrimSpace(in.Title)
	in.Description = strings.TrimSpace(in.Description)
	in.Content = strings.TrimSpace(in.Content)
	in.Category = strings.TrimSpace(in.Category)
	in.VoiceID = strings.TrimSpace(in.VoiceID)
	in.ImageURL = strings.TrimSpace(in.ImageURL)
	in.Chapters = cleanChapters(in.Chapters)
	if err := episode.ValidateCreate(in, a.Cfg.Limits(), a.Cfg.VoiceAllow); err != nil {
		return nil, err
	}
	if in.VoiceID == "" {
		in.VoiceID = a.Cfg.DefaultVoice
	}
	in.PodcastID = strings.ToLower(strings.TrimSpace(in.PodcastID))
	if in.PodcastID != "" {
		if _, err := a.Store.GetPodcast(ctx, in.PodcastID); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return nil, &episode.ValidationError{Field: "podcast_id", Message: "podcast not found"}
			}
			return nil, fmt.Errorf("load podcast: %w", err)
		}
	}
	now := time.Now().UTC()
	ep := &episode.Episode{
		ID:          episode.NewID(),
		PodcastID:   in.PodcastID,
		Title:       in.Title,
		Description: in.Description,
		ScriptText:  in.Content,
		Category:    in.Category,
		VoiceID:     in.VoiceID,
		ImageURL:    in.ImageURL,
		Chapters:    in.Chapters,
		Status:      episode.StatusPending,
		CreatedAt:   now,
	}
	if cover.IsInlineDataURI(ep.ImageURL) {
		data, ct, err := cover.DecodeInlineDataURI(ep.ImageURL)
		if err != nil {
			return nil, &episode.ValidationError{Field: "image_url", Message: err.Error()}
		}
		if a.Storage != nil {
			if err := a.Storage.Put(ctx, cover.EpisodeCoverKey(ep.ID), bytes.NewReader(data), int64(len(data)), ct); err != nil {
				return nil, fmt.Errorf("store episode cover: %w", err)
			}
		}
		ep.ImageURL = a.baseURL() + ep.CoverPath()
	}
	if err := a.Store.Create(ctx, ep); err != nil {
		return nil, fmt.Errorf("persist episode: %w", err)
	}
	if err := a.Jobs.Enqueue(ctx, ep.ID); err != nil {
		ep.Status = episode.StatusPending
		ep.ErrorMessage = "failed to enqueue synthesis job: " + err.Error()
		_ = a.Store.Update(ctx, ep)
		return nil, fmt.Errorf("enqueue job: %w", err)
	}
	a.logger().Info("episode queued", "episode_id", ep.ID, "title", ep.Title, "podcast_id", ep.PodcastID)
	return ep, nil
}

// UpdateEpisode validates and updates an existing episode's metadata.
func (a *App) UpdateEpisode(ctx context.Context, id string, in episode.UpdateInput) (*episode.Episode, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, &episode.ValidationError{Field: "episode_id", Message: "episode_id is required"}
	}
	if in.Title != nil {
		s := strings.TrimSpace(*in.Title)
		in.Title = &s
	}
	if in.Description != nil {
		s := strings.TrimSpace(*in.Description)
		in.Description = &s
	}
	if in.Category != nil {
		s := strings.TrimSpace(*in.Category)
		in.Category = &s
	}
	if in.ImageURL != nil {
		s := strings.TrimSpace(*in.ImageURL)
		in.ImageURL = &s
	}
	if in.Chapters != nil {
		ch := cleanChapters(*in.Chapters)
		in.Chapters = &ch
	}
	var limits episode.Limits
	if a.Cfg != nil {
		limits = a.Cfg.Limits()
	}
	if err := episode.ValidateUpdate(in, limits); err != nil {
		return nil, err
	}
	ep, err := a.Store.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if in.Title != nil {
		ep.Title = *in.Title
	}
	if in.Description != nil {
		ep.Description = *in.Description
	}
	if in.Category != nil {
		ep.Category = *in.Category
	}
	if in.ImageURL != nil {
		imageURL := *in.ImageURL
		if cover.IsInlineDataURI(imageURL) {
			data, ct, err := cover.DecodeInlineDataURI(imageURL)
			if err != nil {
				return nil, &episode.ValidationError{Field: "image_url", Message: err.Error()}
			}
			if a.Storage != nil {
				if err := a.Storage.Put(ctx, cover.EpisodeCoverKey(ep.ID), bytes.NewReader(data), int64(len(data)), ct); err != nil {
					return nil, fmt.Errorf("store episode cover: %w", err)
				}
			}
			imageURL = a.baseURL() + ep.CoverPath()
		}
		ep.ImageURL = imageURL
	}
	if in.Chapters != nil {
		ep.Chapters = *in.Chapters
	}
	if err := a.Store.Update(ctx, ep); err != nil {
		return nil, fmt.Errorf("update episode: %w", err)
	}
	a.logger().Info("episode updated", "episode_id", ep.ID)
	return ep, nil
}

func cleanChapters(chapters []episode.Chapter) []episode.Chapter {
	if len(chapters) == 0 {
		return nil
	}
	out := make([]episode.Chapter, len(chapters))
	for i, ch := range chapters {
		out[i] = episode.Chapter{
			StartSeconds: ch.StartSeconds,
			Title:        strings.TrimSpace(ch.Title),
			URL:          strings.TrimSpace(ch.URL),
			ImageURL:     strings.TrimSpace(ch.ImageURL),
		}
	}
	return out
}

// CreatePodcast registers a private show with its own feed credentials.
func (a *App) CreatePodcast(ctx context.Context, in podcast.CreateInput) (*podcast.Podcast, error) {
	in.ID = strings.ToLower(strings.TrimSpace(in.ID))
	in.Title = strings.TrimSpace(in.Title)
	in.Description = strings.TrimSpace(in.Description)
	in.Author = strings.TrimSpace(in.Author)
	in.ImageURL = strings.TrimSpace(in.ImageURL)
	in.Password = strings.TrimSpace(in.Password)
	in.Token = strings.TrimSpace(in.Token)
	if err := podcast.ValidateCreate(in); err != nil {
		return nil, err
	}
	password, token, err := podcast.NewSecrets()
	if err != nil {
		return nil, fmt.Errorf("generate credentials: %w", err)
	}
	if in.Password != "" {
		password = in.Password
	}
	if in.Token != "" {
		token = in.Token
	} else if in.Password != "" {
		token = in.Password
	}
	submitKey, err := podcast.NewSubmitKey()
	if err != nil {
		return nil, fmt.Errorf("generate submit key: %w", err)
	}
	p := &podcast.Podcast{
		ID:          in.ID,
		Title:       in.Title,
		Description: in.Description,
		Author:      in.Author,
		ImageURL:    in.ImageURL,
		Username:    in.ID,
		Password:    password,
		Token:       token,
		SubmitKey:   submitKey,
		CreatedAt:   time.Now().UTC(),
	}
	if p.Author == "" && a.Cfg != nil {
		p.Author = a.Cfg.PodcastAuthor
	}
	if p.Description == "" && a.Cfg != nil {
		p.Description = a.Cfg.PodcastDescription
	}
	if cover.IsInlineDataURI(p.ImageURL) {
		data, ct, err := cover.DecodeInlineDataURI(p.ImageURL)
		if err != nil {
			return nil, &episode.ValidationError{Field: "image_url", Message: err.Error()}
		}
		if a.Storage != nil {
			if err := a.Storage.Put(ctx, cover.PodcastCoverKey(p.ID), bytes.NewReader(data), int64(len(data)), ct); err != nil {
				return nil, fmt.Errorf("store podcast cover: %w", err)
			}
		}
		p.ImageURL = a.baseURL() + p.CoverPath()
	}
	if err := a.Store.CreatePodcast(ctx, p); err != nil {
		return nil, fmt.Errorf("persist podcast: %w", err)
	}
	a.logger().Info("podcast created", "podcast_id", p.ID)
	return p, nil
}

// UpdatePodcast validates and updates an existing podcast's metadata.
func (a *App) UpdatePodcast(ctx context.Context, id string, in podcast.UpdateInput) (*podcast.Podcast, error) {
	id = strings.ToLower(strings.TrimSpace(id))
	if id == "" {
		return nil, &episode.ValidationError{Field: "podcast_id", Message: "podcast_id is required"}
	}
	if in.Title != nil {
		s := strings.TrimSpace(*in.Title)
		in.Title = &s
	}
	if in.Description != nil {
		s := strings.TrimSpace(*in.Description)
		in.Description = &s
	}
	if in.Author != nil {
		s := strings.TrimSpace(*in.Author)
		in.Author = &s
	}
	if in.ImageURL != nil {
		s := strings.TrimSpace(*in.ImageURL)
		in.ImageURL = &s
	}
	if err := podcast.ValidateUpdate(in); err != nil {
		return nil, err
	}
	p, err := a.Store.GetPodcast(ctx, id)
	if err != nil {
		return nil, err
	}
	if in.Title != nil {
		p.Title = *in.Title
	}
	if in.Description != nil {
		p.Description = *in.Description
	}
	if in.Author != nil {
		p.Author = *in.Author
	}
	if in.ImageURL != nil {
		imageURL := *in.ImageURL
		if cover.IsInlineDataURI(imageURL) {
			data, ct, err := cover.DecodeInlineDataURI(imageURL)
			if err != nil {
				return nil, &episode.ValidationError{Field: "image_url", Message: err.Error()}
			}
			if a.Storage != nil {
				if err := a.Storage.Put(ctx, cover.PodcastCoverKey(p.ID), bytes.NewReader(data), int64(len(data)), ct); err != nil {
					return nil, fmt.Errorf("store podcast cover: %w", err)
				}
			}
			imageURL = a.baseURL() + p.CoverPath()
		}
		p.ImageURL = imageURL
	}
	if err := a.Store.UpdatePodcast(ctx, p); err != nil {
		return nil, fmt.Errorf("update podcast: %w", err)
	}
	a.logger().Info("podcast updated", "podcast_id", p.ID)
	return p, nil
}

// RotatePodcastCredentials rotates or updates listener and/or submitter credentials for a podcast.
func (a *App) RotatePodcastCredentials(ctx context.Context, id string, opts podcast.RotateOptions) (*podcast.Podcast, error) {
	id = strings.ToLower(strings.TrimSpace(id))
	if id == "" {
		return nil, &episode.ValidationError{Field: "podcast_id", Message: "podcast_id is required"}
	}
	p, err := a.Store.GetPodcast(ctx, id)
	if err != nil {
		return nil, err
	}
	opts.Password = strings.TrimSpace(opts.Password)
	opts.Token = strings.TrimSpace(opts.Token)
	if !opts.RotateListener && !opts.RotateSubmitKey && opts.Password == "" && opts.Token == "" {
		opts.RotateListener = true
	}
	if opts.RotateListener || opts.Password != "" || opts.Token != "" {
		pass, tok, err := podcast.NewSecrets()
		if err != nil {
			return nil, fmt.Errorf("generate credentials: %w", err)
		}
		if opts.Password != "" {
			p.Password = opts.Password
		} else if opts.RotateListener {
			p.Password = pass
		}
		if opts.Token != "" {
			p.Token = opts.Token
		} else if opts.RotateListener {
			p.Token = tok
		}
	}
	if opts.RotateSubmitKey {
		sk, err := podcast.NewSubmitKey()
		if err != nil {
			return nil, fmt.Errorf("generate submit key: %w", err)
		}
		p.SubmitKey = sk
	}
	if err := a.Store.UpdatePodcast(ctx, p); err != nil {
		return nil, fmt.Errorf("update podcast: %w", err)
	}
	a.logger().Info("podcast credentials rotated", "podcast_id", p.ID, "rotate_listener", opts.RotateListener, "rotate_submit_key", opts.RotateSubmitKey)
	return p, nil
}

// AuthenticateBearer resolves a bearer token into a Principal (admin, default submitter, or per-podcast submitter).
func (a *App) AuthenticateBearer(ctx context.Context, token string) (auth.Principal, bool, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return auth.Principal{}, false, nil
	}
	if a.Cfg != nil && a.Cfg.AdminAPIKey != "" {
		if auth.APIKeyMatch(token, a.Cfg.AdminAPIKey) {
			return auth.Principal{Role: auth.RoleAdmin}, true, nil
		}
		if auth.APIKeyMatch(token, a.Cfg.AgentAPIKey) {
			return auth.Principal{Role: auth.RoleDefaultSubmitter}, true, nil
		}
	} else if a.Cfg != nil {
		if auth.APIKeyMatch(token, a.Cfg.AgentAPIKey) {
			return auth.Principal{Role: auth.RoleAdmin}, true, nil
		}
	}
	if a.Store == nil {
		return auth.Principal{}, false, nil
	}
	p, err := a.Store.GetPodcastBySubmitKey(ctx, token)
	if err == nil && p != nil {
		return auth.Principal{Role: auth.RolePodcastSubmitter, PodcastID: p.ID}, true, nil
	}
	if errors.Is(err, store.ErrNotFound) {
		return auth.Principal{}, false, nil
	}
	return auth.Principal{}, false, err
}

// Reconcile sweeps the store for stranded episodes across server restarts or worker crashes.
// Episodes stuck in PROCESSING are reset to PENDING, and all PENDING episodes are re-enqueued.
func (a *App) Reconcile(ctx context.Context) error {
	log := a.logger()
	if a.Store == nil {
		return nil
	}

	var cutoff time.Time
	if a.Cfg != nil && a.Cfg.JobBackend == config.BackendCloudRun {
		timeout := a.Cfg.WorkerTimeout
		if timeout <= 0 {
			timeout = 30 * time.Minute
		}
		cutoff = time.Now().UTC().Add(-timeout)
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
			if !cutoff.IsZero() && !ep.CreatedAt.IsZero() && ep.CreatedAt.After(cutoff) {
				continue
			}
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
				if !cutoff.IsZero() && !ep.CreatedAt.IsZero() && ep.CreatedAt.After(cutoff) {
					continue
				}
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
	case config.EngineKokoro:
		return &tts.KokoroEngine{
			Bin:          cfg.KokoroBin,
			ModelDir:     cfg.KokoroModelDir,
			Model:        cfg.KokoroModel,
			Voices:       cfg.KokoroVoices,
			Tokens:       cfg.KokoroTokens,
			DataDir:      cfg.KokoroDataDir,
			DictDir:      cfg.KokoroDictDir,
			Lexicon:      cfg.KokoroLexicon,
			Speed:        cfg.KokoroSpeed,
			Threads:      cfg.KokoroThreads,
			FFmpegBin:    cfg.FFmpegBin,
			DefaultVoice: cfg.DefaultVoice,
		}, nil
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
