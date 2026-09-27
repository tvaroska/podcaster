package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/tvaroska/podcaster/internal/app"
	"github.com/tvaroska/podcaster/internal/config"
	"github.com/tvaroska/podcaster/internal/cover"
	"github.com/tvaroska/podcaster/internal/episode"
	mcpserver "github.com/tvaroska/podcaster/internal/mcp"
	"github.com/tvaroska/podcaster/internal/storage"
)

func testApp(t *testing.T) (*app.App, *config.Config, http.Handler) {
	t.Helper()
	dir := t.TempDir()
	cfg := &config.Config{
		ListenAddr:         ":0",
		PublicBaseURL:      "http://podcast.example.com",
		AgentAPIKey:        "secret-key",
		FeedUsername:       "podcast",
		FeedPassword:       "s3cret",
		FeedToken:          "feed-token",
		StoreBackend:       config.BackendSQLite,
		SQLitePath:         filepath.Join(dir, "podcaster.db"),
		StorageBackend:     config.BackendLocal,
		LocalDataDir:       filepath.Join(dir, "data"),
		JobBackend:         config.BackendLocal,
		TTSEngine:          config.EngineMock,
		FFmpegBin:          "ffmpeg",
		DefaultVoice:       config.DefaultVoice,
		PodcastTitle:       "Private Agent Briefing",
		PodcastDescription: "Automated private briefings.",
		PodcastAuthor:      "Podcaster",
		PodcastLanguage:    "en-us",
		PodcastCategory:    "Technology",
		MinContentLength:   episode.DefaultMinContentLength,
		MaxContentLength:   episode.DefaultMaxContentLength,
		MaxTitleLength:     episode.DefaultMaxTitleLength,
		WorkerTimeout:      time.Minute,
	}
	application, err := app.Open(context.Background(), cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = application.Close() })
	h := &Handler{
		App:   application,
		Cfg:   cfg,
		Cover: cover.Generate(),
		Log:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		MCP:   mcpserver.HTTPHandler(mcpserver.NewServer(application)),
	}
	return application, cfg, h.Router()
}

func TestHealthz(t *testing.T) {
	_, _, h := testApp(t)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rr.Code != 200 {
		t.Fatalf("status %d", rr.Code)
	}
}

func TestCreateRequiresAuth(t *testing.T) {
	_, _, h := testApp(t)
	body := `{"title":"Morning Briefing","content":"Good morning. Here are your top updates for today."}`
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/episodes", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status %d", rr.Code)
	}
}

func TestCreateValidates(t *testing.T) {
	_, _, h := testApp(t)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/episodes", strings.NewReader(`{"title":"x","content":"hi"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer secret-key")
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status %d body %s", rr.Code, rr.Body.String())
	}
}

func TestCreateAndFeed(t *testing.T) {
	_, _, h := testApp(t)
	rr := httptest.NewRecorder()
	payload := `{"title":"Morning Briefing - Sept 26, 2026","content":"Good morning. Here are your top updates for today. Markets opened mixed.","category":"Daily Briefing"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/episodes", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer secret-key")
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status %d body %s", rr.Code, rr.Body.String())
	}
	var created struct {
		EpisodeID string `json:"episode_id"`
		Status    string `json:"status"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.Status != "QUEUED" || !strings.HasPrefix(created.EpisodeID, "ep_") {
		t.Fatalf("created %+v", created)
	}

	deadline := time.Now().Add(10 * time.Second)
	var status string
	for time.Now().Before(deadline) {
		gr := httptest.NewRecorder()
		greq := httptest.NewRequest(http.MethodGet, "/v1/episodes/"+created.EpisodeID, nil)
		greq.Header.Set("Authorization", "Bearer secret-key")
		h.ServeHTTP(gr, greq)
		if gr.Code != 200 {
			t.Fatalf("get status %d %s", gr.Code, gr.Body.String())
		}
		var got map[string]any
		_ = json.Unmarshal(gr.Body.Bytes(), &got)
		status, _ = got["status"].(string)
		if status == "READY" {
			break
		}
		if status == "FAILED" {
			t.Fatalf("episode failed: %v", got)
		}
		time.Sleep(25 * time.Millisecond)
	}
	if status != "READY" {
		t.Fatalf("never became READY, last status %s", status)
	}

	feedReq := httptest.NewRequest(http.MethodGet, "/podcast.xml", nil)
	feedReq.SetBasicAuth("podcast", "s3cret")
	feedRR := httptest.NewRecorder()
	h.ServeHTTP(feedRR, feedReq)
	if feedRR.Code != 200 {
		t.Fatalf("feed %d %s", feedRR.Code, feedRR.Body.String())
	}
	xml := feedRR.Body.String()
	if !strings.Contains(xml, created.EpisodeID) {
		t.Fatalf("feed missing episode:\n%s", xml)
	}
	if !strings.Contains(xml, "audio/"+created.EpisodeID+".mp3?token=feed-token") {
		t.Fatalf("enclosure missing token:\n%s", xml)
	}
	if !strings.Contains(xml, "<itunes:block>yes</itunes:block>") {
		t.Fatal("expected itunes:block")
	}

	bad := httptest.NewRecorder()
	h.ServeHTTP(bad, httptest.NewRequest(http.MethodGet, "/podcast.xml", nil))
	if bad.Code != http.StatusUnauthorized {
		t.Fatalf("unauth feed %d", bad.Code)
	}

	audioReq := httptest.NewRequest(http.MethodGet, "/audio/"+created.EpisodeID+".mp3?token=feed-token", nil)
	audioRR := httptest.NewRecorder()
	h.ServeHTTP(audioRR, audioReq)
	if audioRR.Code != 200 {
		t.Fatalf("audio %d %s", audioRR.Code, audioRR.Body.String())
	}
	if ct := audioRR.Header().Get("Content-Type"); ct != "audio/mpeg" {
		t.Fatalf("content-type %s", ct)
	}
	if audioRR.Body.Len() < 100 {
		t.Fatalf("tiny audio %d", audioRR.Body.Len())
	}

	coverRR := httptest.NewRecorder()
	h.ServeHTTP(coverRR, httptest.NewRequest(http.MethodGet, "/cover.png", nil))
	if coverRR.Code != 200 || coverRR.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("cover %d %s", coverRR.Code, coverRR.Header().Get("Content-Type"))
	}
}

func TestMCPPublishTool(t *testing.T) {
	application, _, _ := testApp(t)
	server := mcpserver.NewServer(application)
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "test", Version: "v0.0.1"}, nil)
	t1, t2 := mcpsdk.NewInMemoryTransports()
	if _, err := server.Connect(context.Background(), t1, nil); err != nil {
		t.Fatal(err)
	}
	session, err := client.Connect(context.Background(), t2, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })

	res, err := session.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name: "publish_agent_update",
		Arguments: map[string]any{
			"title":    "MCP Briefing",
			"content":  "Good morning. This update was posted by an MCP client.",
			"category": "Daily Briefing",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("tool error: %+v", res)
	}
	if len(res.Content) == 0 {
		t.Fatal("no content")
	}
	text := res.Content[0].(*mcpsdk.TextContent).Text
	if !strings.Contains(text, `"status":"QUEUED"`) {
		t.Fatalf("unexpected result %s", text)
	}
}

func TestUnknownJSONField(t *testing.T) {
	_, _, h := testApp(t)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/episodes", strings.NewReader(`{"title":"Morning Briefing","content":"Good morning. Here are your top updates for today.","nope":true}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer secret-key")
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status %d body %s", rr.Code, rr.Body.String())
	}
}

func TestParseAudioFile(t *testing.T) {
	id, ok := parseAudioFile("ep_abc.mp3")
	if !ok || id != "ep_abc" {
		t.Fatalf("%s %v", id, ok)
	}
	if _, ok := parseAudioFile("../x.mp3"); ok {
		t.Fatal("traversal")
	}
	if _, ok := parseAudioFile("ep_abc.txt"); ok {
		t.Fatal("bad ext")
	}
}

func TestCoverGenerate(t *testing.T) {
	b := cover.Generate()
	if len(b) < 100 || !bytes.Equal(b[:8], []byte{137, 80, 78, 71, 13, 10, 26, 10}) {
		t.Fatalf("not a png (%d bytes)", len(b))
	}
}

type mockSignedStorage struct {
	storage.Storage
	signedURL string
}

func (m *mockSignedStorage) SignedURL(ctx context.Context, key string, opts storage.SignedURLOptions) (string, error) {
	return m.signedURL, nil
}

func TestAudioSignedURLRedirect(t *testing.T) {
	app, _, h := testApp(t)
	// Seed a ready episode
	ep := &episode.Episode{
		ID:        "ep_signed",
		Title:     "Signed Test",
		Status:    episode.StatusReady,
		AudioURI:  "audio/ep_signed.mp3",
		CreatedAt: time.Now().UTC(),
	}
	if err := app.Store.Create(context.Background(), ep); err != nil {
		t.Fatal(err)
	}

	app.Storage = &mockSignedStorage{
		Storage:   app.Storage,
		signedURL: "https://storage.googleapis.com/test-bucket/audio/ep_signed.mp3?signature=xyz",
	}

	req := httptest.NewRequest(http.MethodGet, "/audio/ep_signed.mp3?token=feed-token", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusTemporaryRedirect {
		t.Fatalf("expected status 307, got %d", rr.Code)
	}
	loc := rr.Header().Get("Location")
	if loc != "https://storage.googleapis.com/test-bucket/audio/ep_signed.mp3?signature=xyz" {
		t.Fatalf("unexpected redirect location: %s", loc)
	}
}

func TestPerUserPodcastIsolation(t *testing.T) {
	_, _, h := testApp(t)
	agent := func(method, path, body string) *httptest.ResponseRecorder {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		req.Header.Set("Authorization", "Bearer secret-key")
		h.ServeHTTP(rr, req)
		return rr
	}

	rr := agent(http.MethodPost, "/v1/podcasts", `{"id":"alice","title":"Alice Briefing"}`)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create alice %d %s", rr.Code, rr.Body.String())
	}
	var alice map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &alice); err != nil {
		t.Fatal(err)
	}
	rr = agent(http.MethodPost, "/v1/podcasts", `{"id":"bob","title":"Bob Briefing"}`)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create bob %d %s", rr.Code, rr.Body.String())
	}
	var bob map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &bob)

	payload := `{"title":"Hello Alice","content":"Good morning Alice. This is your private briefing for today."}`
	rr = agent(http.MethodPost, "/v1/podcasts/alice/episodes", payload)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("ep alice %d %s", rr.Code, rr.Body.String())
	}
	var created struct {
		EpisodeID string `json:"episode_id"`
		PodcastID string `json:"podcast_id"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &created)
	if created.PodcastID != "alice" {
		t.Fatalf("podcast_id %q", created.PodcastID)
	}

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		gr := agent(http.MethodGet, "/v1/episodes/"+created.EpisodeID, "")
		var got map[string]any
		_ = json.Unmarshal(gr.Body.Bytes(), &got)
		if got["status"] == "READY" {
			break
		}
		if got["status"] == "FAILED" {
			t.Fatalf("failed %v", got)
		}
		time.Sleep(25 * time.Millisecond)
	}

	aliceUser, _ := alice["username"].(string)
	alicePass, _ := alice["password"].(string)
	aliceTok, _ := alice["token"].(string)
	bobUser, _ := bob["username"].(string)
	bobPass, _ := bob["password"].(string)

	feedReq := httptest.NewRequest(http.MethodGet, "/p/alice/podcast.xml", nil)
	feedReq.SetBasicAuth(aliceUser, alicePass)
	feedRR := httptest.NewRecorder()
	h.ServeHTTP(feedRR, feedReq)
	if feedRR.Code != 200 {
		t.Fatalf("alice feed %d %s", feedRR.Code, feedRR.Body.String())
	}
	xml := feedRR.Body.String()
	if !strings.Contains(xml, created.EpisodeID) || !strings.Contains(xml, "Alice Briefing") {
		t.Fatalf("alice feed missing episode:\n%s", xml)
	}
	if !strings.Contains(xml, "/p/alice/audio/"+created.EpisodeID+".mp3?token="+aliceTok) {
		t.Fatalf("enclosure path:\n%s", xml)
	}

	// Bob cannot read Alice's feed
	bad := httptest.NewRequest(http.MethodGet, "/p/alice/podcast.xml", nil)
	bad.SetBasicAuth(bobUser, bobPass)
	badRR := httptest.NewRecorder()
	h.ServeHTTP(badRR, bad)
	if badRR.Code != http.StatusUnauthorized {
		t.Fatalf("bob reading alice feed: %d", badRR.Code)
	}

	// Default feed must not include Alice's episode
	defReq := httptest.NewRequest(http.MethodGet, "/podcast.xml", nil)
	defReq.SetBasicAuth("podcast", "s3cret")
	defRR := httptest.NewRecorder()
	h.ServeHTTP(defRR, defReq)
	if defRR.Code != 200 {
		t.Fatalf("default feed %d", defRR.Code)
	}
	if strings.Contains(defRR.Body.String(), created.EpisodeID) {
		t.Fatalf("default feed leaked alice episode")
	}

	// Bob cannot fetch Alice audio
	audio := httptest.NewRequest(http.MethodGet, "/p/alice/audio/"+created.EpisodeID+".mp3?token="+aliceTok, nil)
	audioRR := httptest.NewRecorder()
	h.ServeHTTP(audioRR, audio)
	if audioRR.Code != 200 {
		t.Fatalf("alice audio %d %s", audioRR.Code, audioRR.Body.String())
	}
	bobAudio := httptest.NewRequest(http.MethodGet, "/p/bob/audio/"+created.EpisodeID+".mp3", nil)
	bobAudio.SetBasicAuth(bobUser, bobPass)
	bobRR := httptest.NewRecorder()
	h.ServeHTTP(bobRR, bobAudio)
	if bobRR.Code != http.StatusNotFound && bobRR.Code != http.StatusUnauthorized {
		t.Fatalf("bob audio of alice ep: %d", bobRR.Code)
	}

	// Global audio path must not serve per-user episodes
	glob := httptest.NewRequest(http.MethodGet, "/audio/"+created.EpisodeID+".mp3?token=feed-token", nil)
	globRR := httptest.NewRecorder()
	h.ServeHTTP(globRR, glob)
	if globRR.Code != http.StatusNotFound {
		t.Fatalf("global audio leaked: %d", globRR.Code)
	}
}
