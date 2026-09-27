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
