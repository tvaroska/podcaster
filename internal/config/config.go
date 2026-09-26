package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/tvaroska/podcaster/internal/episode"
)

const (
	BackendSQLite     = "sqlite"
	BackendFirestore  = "firestore"
	BackendLocal      = "local"
	BackendGCS        = "gcs"
	BackendCloudRun   = "cloudrun"
	EnginePiper       = "piper"
	EngineMock        = "mock"
	DefaultVoice      = "en_US-lessac-medium"
	DefaultListenAddr = ":8080"
)

// Config is assembled entirely from environment variables.
type Config struct {
	ListenAddr    string
	PublicBaseURL string
	AgentAPIKey   string
	FeedUsername  string
	FeedPassword  string
	FeedToken     string

	StoreBackend string
	SQLitePath   string
	GCPProject   string

	StorageBackend string
	LocalDataDir   string
	GCSBucket      string

	JobBackend      string
	CloudRunJob     string
	CloudRunRegion  string
	WorkerTimeout   time.Duration
	ShutdownTimeout time.Duration

	TTSEngine    string
	PiperBin     string
	PiperModel   string
	PiperConfig  string
	FFmpegBin    string
	DefaultVoice string
	VoiceAllow   []string

	PodcastTitle       string
	PodcastDescription string
	PodcastAuthor      string
	PodcastLanguage    string
	PodcastCategory    string
	PodcastExplicit    bool
	PodcastImageFile   string
	PodcastOwnerEmail  string

	MinContentLength int
	MaxContentLength int
	MaxTitleLength   int

	DevDefaults bool
}

// FromEnv loads configuration. Missing local-dev secrets are filled with
// documented defaults when PODCASTER_DEV=1 or no AGENT_API_KEY is set and
// store/storage backends are local.
func FromEnv() (*Config, error) {
	c := &Config{
		ListenAddr:         env("LISTEN_ADDR", DefaultListenAddr),
		PublicBaseURL:      strings.TrimRight(env("PUBLIC_BASE_URL", "http://localhost:8080"), "/"),
		AgentAPIKey:        os.Getenv("AGENT_API_KEY"),
		FeedUsername:       env("FEED_USERNAME", ""),
		FeedPassword:       env("FEED_PASSWORD", ""),
		FeedToken:          os.Getenv("FEED_TOKEN"),
		StoreBackend:       strings.ToLower(env("STORE_BACKEND", BackendSQLite)),
		SQLitePath:         env("SQLITE_PATH", "data/podcaster.db"),
		GCPProject:         first(os.Getenv("GCP_PROJECT"), os.Getenv("GOOGLE_CLOUD_PROJECT")),
		StorageBackend:     strings.ToLower(env("STORAGE_BACKEND", BackendLocal)),
		LocalDataDir:       env("LOCAL_DATA_DIR", "data"),
		GCSBucket:          os.Getenv("GCS_BUCKET"),
		JobBackend:         strings.ToLower(env("JOB_BACKEND", BackendLocal)),
		CloudRunJob:        os.Getenv("CLOUD_RUN_JOB"),
		CloudRunRegion:     env("CLOUD_RUN_REGION", "us-central1"),
		WorkerTimeout:      envDuration("WORKER_TIMEOUT", 30*time.Minute),
		ShutdownTimeout:    envDuration("SHUTDOWN_TIMEOUT", 25*time.Second),
		TTSEngine:          strings.ToLower(env("TTS_ENGINE", EngineMock)),
		PiperBin:           env("PIPER_BIN", "piper"),
		PiperModel:         os.Getenv("PIPER_MODEL"),
		PiperConfig:        os.Getenv("PIPER_CONFIG"),
		FFmpegBin:          env("FFMPEG_BIN", "ffmpeg"),
		DefaultVoice:       env("DEFAULT_VOICE", DefaultVoice),
		PodcastTitle:       env("PODCAST_TITLE", "Private Agent Briefing"),
		PodcastDescription: env("PODCAST_DESCRIPTION", "Automated private briefings synthesized from agent updates."),
		PodcastAuthor:      env("PODCAST_AUTHOR", "Podcaster"),
		PodcastLanguage:    env("PODCAST_LANGUAGE", "en-us"),
		PodcastCategory:    env("PODCAST_CATEGORY", "Technology"),
		PodcastExplicit:    envBool("PODCAST_EXPLICIT", false),
		PodcastImageFile:   os.Getenv("PODCAST_IMAGE_FILE"),
		PodcastOwnerEmail:  os.Getenv("PODCAST_OWNER_EMAIL"),
		MinContentLength:   envInt("MIN_CONTENT_LENGTH", episode.DefaultMinContentLength),
		MaxContentLength:   envInt("MAX_CONTENT_LENGTH", episode.DefaultMaxContentLength),
		MaxTitleLength:     envInt("MAX_TITLE_LENGTH", episode.DefaultMaxTitleLength),
	}

	if v := strings.TrimSpace(os.Getenv("VOICE_ALLOWLIST")); v != "" {
		c.VoiceAllow = splitCSV(v)
	}
	// Cloud Run sets PORT; honour it unless LISTEN_ADDR is explicit.
	if strings.TrimSpace(os.Getenv("LISTEN_ADDR")) == "" {
		if p := strings.TrimSpace(os.Getenv("PORT")); p != "" {
			if !strings.HasPrefix(p, ":") {
				p = ":" + p
			}
			c.ListenAddr = p
		}
	}

	dev := envBool("PODCASTER_DEV", false)
	if c.AgentAPIKey == "" && (dev || (c.StoreBackend == BackendSQLite && c.StorageBackend == BackendLocal && c.JobBackend == BackendLocal)) {
		c.DevDefaults = true
		if c.AgentAPIKey == "" {
			c.AgentAPIKey = "dev-agent-key"
		}
		if c.FeedUsername == "" {
			c.FeedUsername = "podcast"
		}
		if c.FeedPassword == "" {
			c.FeedPassword = "podcast"
		}
		if c.FeedToken == "" {
			c.FeedToken = "dev-feed-token"
		}
	}

	if err := c.Validate(); err != nil {
		return nil, err
	}
	return c, nil
}

// Validate checks required production settings.
func (c *Config) Validate() error {
	if c.AgentAPIKey == "" {
		return fmt.Errorf("AGENT_API_KEY is required")
	}
	if c.FeedUsername == "" || c.FeedPassword == "" {
		return fmt.Errorf("FEED_USERNAME and FEED_PASSWORD are required")
	}
	if c.PublicBaseURL == "" {
		return fmt.Errorf("PUBLIC_BASE_URL is required")
	}
	switch c.StoreBackend {
	case BackendSQLite:
		if c.SQLitePath == "" {
			return fmt.Errorf("SQLITE_PATH is required when STORE_BACKEND=sqlite")
		}
	case BackendFirestore:
		if c.GCPProject == "" {
			return fmt.Errorf("GCP_PROJECT is required when STORE_BACKEND=firestore")
		}
	default:
		return fmt.Errorf("unknown STORE_BACKEND %q (sqlite|firestore)", c.StoreBackend)
	}
	switch c.StorageBackend {
	case BackendLocal:
		if c.LocalDataDir == "" {
			return fmt.Errorf("LOCAL_DATA_DIR is required when STORAGE_BACKEND=local")
		}
	case BackendGCS:
		if c.GCSBucket == "" {
			return fmt.Errorf("GCS_BUCKET is required when STORAGE_BACKEND=gcs")
		}
		if c.GCPProject == "" {
			c.GCPProject = first(os.Getenv("GCP_PROJECT"), os.Getenv("GOOGLE_CLOUD_PROJECT"))
		}
	default:
		return fmt.Errorf("unknown STORAGE_BACKEND %q (local|gcs)", c.StorageBackend)
	}
	switch c.JobBackend {
	case BackendLocal:
	case BackendCloudRun:
		if c.CloudRunJob == "" || c.GCPProject == "" {
			return fmt.Errorf("CLOUD_RUN_JOB and GCP_PROJECT are required when JOB_BACKEND=cloudrun")
		}
	default:
		return fmt.Errorf("unknown JOB_BACKEND %q (local|cloudrun)", c.JobBackend)
	}
	switch c.TTSEngine {
	case EnginePiper, EngineMock:
	default:
		return fmt.Errorf("unknown TTS_ENGINE %q (piper|mock)", c.TTSEngine)
	}
	if c.TTSEngine == EnginePiper && c.PiperModel == "" {
		return fmt.Errorf("PIPER_MODEL is required when TTS_ENGINE=piper")
	}
	return nil
}

func (c *Config) Limits() episode.Limits {
	return episode.Limits{
		MinContentLength: c.MinContentLength,
		MaxContentLength: c.MaxContentLength,
		MaxTitleLength:   c.MaxTitleLength,
	}
}

func (c *Config) CoverURL() string {
	return c.PublicBaseURL + "/cover.png"
}

func env(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && strings.TrimSpace(v) != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}

func envBool(key string, fallback bool) bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	switch v {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	}
	return fallback
}

func envDuration(key string, fallback time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return fallback
	}
	return d
}

func splitCSV(v string) []string {
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func first(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
