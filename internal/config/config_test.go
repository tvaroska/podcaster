package config

import (
	"testing"
)

func TestFromEnvDevDefaults(t *testing.T) {
	t.Setenv("AGENT_API_KEY", "")
	t.Setenv("FEED_USERNAME", "")
	t.Setenv("FEED_PASSWORD", "")
	t.Setenv("STORE_BACKEND", "sqlite")
	t.Setenv("STORAGE_BACKEND", "local")
	t.Setenv("JOB_BACKEND", "local")
	t.Setenv("TTS_ENGINE", "mock")
	t.Setenv("PODCASTER_DEV", "1")

	c, err := FromEnv()
	if err != nil {
		t.Fatalf("FromEnv: %v", err)
	}
	if !c.DevDefaults {
		t.Fatal("expected dev defaults")
	}
	if c.AgentAPIKey != "dev-agent-key" {
		t.Fatalf("api key: %q", c.AgentAPIKey)
	}
	if c.FeedUsername != "podcast" || c.FeedPassword != "podcast" {
		t.Fatalf("feed creds: %s/%s", c.FeedUsername, c.FeedPassword)
	}
}

func TestPortOverridesListenAddr(t *testing.T) {
	t.Setenv("AGENT_API_KEY", "k")
	t.Setenv("FEED_USERNAME", "u")
	t.Setenv("FEED_PASSWORD", "p")
	t.Setenv("STORE_BACKEND", "sqlite")
	t.Setenv("STORAGE_BACKEND", "local")
	t.Setenv("JOB_BACKEND", "local")
	t.Setenv("TTS_ENGINE", "mock")
	t.Setenv("PORT", "9090")
	t.Setenv("LISTEN_ADDR", "")

	c, err := FromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if c.ListenAddr != ":9090" {
		t.Fatalf("listen %q", c.ListenAddr)
	}
}

func TestValidateProduction(t *testing.T) {
	c := &Config{
		ListenAddr:     ":8080",
		PublicBaseURL:  "https://podcast.example.com",
		AgentAPIKey:    "secret",
		FeedUsername:   "u",
		FeedPassword:   "p",
		StoreBackend:   BackendFirestore,
		StorageBackend: BackendGCS,
		JobBackend:     BackendCloudRun,
		TTSEngine:      EnginePiper,
	}
	if err := c.Validate(); err == nil {
		t.Fatal("expected error for missing GCP fields")
	}
	c.GCPProject = "demo"
	c.GCSBucket = "bucket"
	c.CloudRunJob = "tts-worker"
	c.PiperModel = "/models/en.onnx"
	if err := c.Validate(); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
}

func TestCloudRunJobNameEnv(t *testing.T) {
	t.Setenv("AGENT_API_KEY", "k")
	t.Setenv("FEED_USERNAME", "u")
	t.Setenv("FEED_PASSWORD", "p")
	t.Setenv("STORE_BACKEND", "firestore")
	t.Setenv("STORAGE_BACKEND", "gcs")
	t.Setenv("JOB_BACKEND", "cloudrun")
	t.Setenv("TTS_ENGINE", "mock")
	t.Setenv("GCP_PROJECT", "demo")
	t.Setenv("GCS_BUCKET", "bucket")
	t.Setenv("CLOUD_RUN_JOB", "") // reserved on Cloud Run; must not be required
	t.Setenv("CLOUD_RUN_JOB_NAME", "podcaster-worker")
	t.Setenv("CLOUD_RUN_REGION", "us-central1")

	c, err := FromEnv()
	if err != nil {
		t.Fatalf("FromEnv: %v", err)
	}
	if c.CloudRunJob != "podcaster-worker" {
		t.Fatalf("CloudRunJob=%q", c.CloudRunJob)
	}
}

func TestVoiceAllowlistParsing(t *testing.T) {
	t.Setenv("AGENT_API_KEY", "k")
	t.Setenv("FEED_USERNAME", "u")
	t.Setenv("FEED_PASSWORD", "p")
	t.Setenv("STORE_BACKEND", "sqlite")
	t.Setenv("STORAGE_BACKEND", "local")
	t.Setenv("JOB_BACKEND", "local")
	t.Setenv("TTS_ENGINE", "mock")
	t.Setenv("VOICE_ALLOWLIST", " en_US-lessac-medium , , en_GB-alba-medium ")

	c, err := FromEnv()
	if err != nil {
		t.Fatalf("FromEnv: %v", err)
	}
	if len(c.VoiceAllow) != 2 || c.VoiceAllow[0] != "en_US-lessac-medium" || c.VoiceAllow[1] != "en_GB-alba-medium" {
		t.Fatalf("unexpected VoiceAllow: %#v", c.VoiceAllow)
	}
}

func TestPartialLocalEnvPopulatesFeedCreds(t *testing.T) {
	t.Setenv("AGENT_API_KEY", "custom-key")
	t.Setenv("FEED_USERNAME", "")
	t.Setenv("FEED_PASSWORD", "")
	t.Setenv("FEED_TOKEN", "")
	t.Setenv("STORE_BACKEND", "sqlite")
	t.Setenv("STORAGE_BACKEND", "local")
	t.Setenv("JOB_BACKEND", "local")
	t.Setenv("TTS_ENGINE", "mock")
	t.Setenv("PODCASTER_DEV", "")

	c, err := FromEnv()
	if err != nil {
		t.Fatalf("FromEnv: %v", err)
	}
	if c.AgentAPIKey != "custom-key" {
		t.Fatalf("expected custom api key, got %q", c.AgentAPIKey)
	}
	if c.FeedUsername != "podcast" || c.FeedPassword != "podcast" || c.FeedToken != "dev-feed-token" {
		t.Fatalf("expected default feed creds, got %q/%q/%q", c.FeedUsername, c.FeedPassword, c.FeedToken)
	}
	if !c.DevDefaults {
		t.Fatal("expected DevDefaults=true when feed creds were defaulted")
	}
}

func TestPodcasterDevDoesNotPopulateCloudBackend(t *testing.T) {
	t.Setenv("AGENT_API_KEY", "")
	t.Setenv("FEED_USERNAME", "")
	t.Setenv("FEED_PASSWORD", "")
	t.Setenv("STORE_BACKEND", "firestore")
	t.Setenv("STORAGE_BACKEND", "local")
	t.Setenv("JOB_BACKEND", "local")
	t.Setenv("GCP_PROJECT", "demo")
	t.Setenv("PODCASTER_DEV", "1")

	if _, err := FromEnv(); err == nil {
		t.Fatal("expected error when PODCASTER_DEV=1 with STORE_BACKEND=firestore and missing credentials")
	}
}

func TestAdminAPIKeyAndEffectiveAdminKey(t *testing.T) {
	t.Setenv("AGENT_API_KEY", "agent-key")
	t.Setenv("ADMIN_API_KEY", "")
	t.Setenv("FEED_USERNAME", "u")
	t.Setenv("FEED_PASSWORD", "p")
	t.Setenv("STORE_BACKEND", "sqlite")
	t.Setenv("STORAGE_BACKEND", "local")
	t.Setenv("JOB_BACKEND", "local")
	t.Setenv("TTS_ENGINE", "mock")

	c, err := FromEnv()
	if err != nil {
		t.Fatalf("FromEnv: %v", err)
	}
	if c.AdminAPIKey != "" {
		t.Fatalf("expected empty AdminAPIKey, got %q", c.AdminAPIKey)
	}
	if got := c.EffectiveAdminKey(); got != "agent-key" {
		t.Fatalf("expected EffectiveAdminKey fallback to agent-key, got %q", got)
	}

	t.Setenv("ADMIN_API_KEY", "  super-admin-key  ")
	c2, err := FromEnv()
	if err != nil {
		t.Fatalf("FromEnv with ADMIN_API_KEY: %v", err)
	}
	if c2.AdminAPIKey != "super-admin-key" {
		t.Fatalf("expected trimmed AdminAPIKey, got %q", c2.AdminAPIKey)
	}
	if got := c2.EffectiveAdminKey(); got != "super-admin-key" {
		t.Fatalf("expected EffectiveAdminKey to return super-admin-key, got %q", got)
	}
}
