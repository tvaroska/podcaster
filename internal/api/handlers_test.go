package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
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
	"github.com/tvaroska/podcaster/internal/podcast"
	"github.com/tvaroska/podcaster/internal/storage"
	"github.com/tvaroska/podcaster/internal/store"
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
	lastOpts  storage.SignedURLOptions
}

func (m *mockSignedStorage) SignedURL(ctx context.Context, key string, opts storage.SignedURLOptions) (string, error) {
	m.lastOpts = opts
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

	mockBlob := &mockSignedStorage{
		Storage:   app.Storage,
		signedURL: "https://storage.googleapis.com/test-bucket/audio/ep_signed.mp3?signature=xyz",
	}
	app.Storage = mockBlob

	for _, method := range []string{http.MethodGet, http.MethodHead} {
		req := httptest.NewRequest(method, "/audio/ep_signed.mp3?token=feed-token", nil)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)

		if rr.Code != http.StatusTemporaryRedirect {
			t.Fatalf("%s: expected status 307, got %d", method, rr.Code)
		}
		loc := rr.Header().Get("Location")
		if loc != "https://storage.googleapis.com/test-bucket/audio/ep_signed.mp3?signature=xyz" {
			t.Fatalf("%s: unexpected redirect location: %s", method, loc)
		}
		if mockBlob.lastOpts.Method != method {
			t.Fatalf("%s: expected SignedURL method %q, got %q", method, method, mockBlob.lastOpts.Method)
		}
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

type failingPodcastStore struct {
	store.Store
	getPodcastErr error
}

func (f *failingPodcastStore) GetPodcast(ctx context.Context, id string) (*podcast.Podcast, error) {
	if f.getPodcastErr != nil {
		return nil, f.getPodcastErr
	}
	return f.Store.GetPodcast(ctx, id)
}

func TestReadyzAndEndpoints(t *testing.T) {
	application, cfg, h := testApp(t)
	cfg.PublicBaseURL = "http://podcast.example.com/subpath"

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

	// 1. /readyz
	readyRR := httptest.NewRecorder()
	h.ServeHTTP(readyRR, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if readyRR.Code != http.StatusOK || !strings.Contains(readyRR.Body.String(), `"status":"ready"`) {
		t.Fatalf("readyz %d %s", readyRR.Code, readyRR.Body.String())
	}

	// 2. Cover Last-Modified and If-Modified-Since caching
	coverRR := httptest.NewRecorder()
	h.ServeHTTP(coverRR, httptest.NewRequest(http.MethodGet, "/cover.png", nil))
	lastMod := coverRR.Header().Get("Last-Modified")
	if lastMod == "" {
		t.Fatal("expected Last-Modified header on /cover.png")
	}
	imsReq := httptest.NewRequest(http.MethodGet, "/cover.png", nil)
	imsReq.Header.Set("If-Modified-Since", lastMod)
	imsRR := httptest.NewRecorder()
	h.ServeHTTP(imsRR, imsReq)
	if imsRR.Code != http.StatusNotModified {
		t.Fatalf("expected 304 Not Modified, got %d", imsRR.Code)
	}

	// 3. Create podcast, duplicate 409, GET /v1/podcasts, GET /v1/podcasts/{id}
	rr := agent(http.MethodPost, "/v1/podcasts", `{"id":"carol","title":"Carol Show"}`)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create carol %d %s", rr.Code, rr.Body.String())
	}
	var carol map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &carol); err != nil {
		t.Fatal(err)
	}
	subURL, _ := carol["subscribe_url"].(string)
	if !strings.HasSuffix(subURL, "@podcast.example.com/subpath/p/carol/podcast.xml") {
		t.Fatalf("subscribe_url missing base path prefix: %s", subURL)
	}

	dupRR := agent(http.MethodPost, "/v1/podcasts", `{"id":"carol","title":"Carol Show"}`)
	if dupRR.Code != http.StatusConflict {
		t.Fatalf("expected 409 Conflict on duplicate podcast, got %d %s", dupRR.Code, dupRR.Body.String())
	}

	listPodcastsRR := agent(http.MethodGet, "/v1/podcasts", "")
	if listPodcastsRR.Code != http.StatusOK {
		t.Fatalf("list podcasts %d %s", listPodcastsRR.Code, listPodcastsRR.Body.String())
	}
	var listedShows struct {
		Podcasts []map[string]any `json:"podcasts"`
	}
	if err := json.Unmarshal(listPodcastsRR.Body.Bytes(), &listedShows); err != nil {
		t.Fatal(err)
	}
	if len(listedShows.Podcasts) != 1 || listedShows.Podcasts[0]["id"] != "carol" {
		t.Fatalf("unexpected podcasts list: %+v", listedShows)
	}

	getPodcastRR := agent(http.MethodGet, "/v1/podcasts/carol", "")
	if getPodcastRR.Code != http.StatusOK {
		t.Fatalf("get podcast %d %s", getPodcastRR.Code, getPodcastRR.Body.String())
	}
	notFoundPodcastRR := agent(http.MethodGet, "/v1/podcasts/unknown", "")
	if notFoundPodcastRR.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown podcast, got %d", notFoundPodcastRR.Code)
	}

	// 4. POST /v1/podcasts/{id}/episodes with mismatched body podcast_id -> 422
	mismatchRR := agent(http.MethodPost, "/v1/podcasts/carol/episodes", `{"title":"Valid Title","content":"Good morning Carol, this is a valid script body.","podcast_id":"bob"}`)
	if mismatchRR.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 on mismatched podcast_id, got %d %s", mismatchRR.Code, mismatchRR.Body.String())
	}

	// 5. GET /v1/episodes with only_default=true and limit > 100 clamping
	defEpRR := agent(http.MethodPost, "/v1/episodes", `{"title":"Default Ep","content":"Good morning. This episode belongs to the default feed."}`)
	if defEpRR.Code != http.StatusAccepted {
		t.Fatalf("create default ep %d %s", defEpRR.Code, defEpRR.Body.String())
	}
	carolEpRR := agent(http.MethodPost, "/v1/podcasts/carol/episodes", `{"title":"Carol Ep","content":"Good morning Carol. This episode belongs to Carol."}`)
	if carolEpRR.Code != http.StatusAccepted {
		t.Fatalf("create carol ep %d %s", carolEpRR.Code, carolEpRR.Body.String())
	}

	allEpsRR := agent(http.MethodGet, "/v1/episodes?limit=500", "")
	if allEpsRR.Code != http.StatusOK {
		t.Fatalf("list all episodes %d %s", allEpsRR.Code, allEpsRR.Body.String())
	}
	var allEps struct {
		Episodes []map[string]any `json:"episodes"`
	}
	if err := json.Unmarshal(allEpsRR.Body.Bytes(), &allEps); err != nil {
		t.Fatal(err)
	}
	if len(allEps.Episodes) != 2 {
		t.Fatalf("expected 2 total episodes, got %d", len(allEps.Episodes))
	}

	defOnlyRR := agent(http.MethodGet, "/v1/episodes?only_default=true", "")
	if defOnlyRR.Code != http.StatusOK {
		t.Fatalf("list only_default episodes %d %s", defOnlyRR.Code, defOnlyRR.Body.String())
	}
	var defEps struct {
		Episodes []map[string]any `json:"episodes"`
	}
	if err := json.Unmarshal(defOnlyRR.Body.Bytes(), &defEps); err != nil {
		t.Fatal(err)
	}
	if len(defEps.Episodes) != 1 || defEps.Episodes[0]["title"] != "Default Ep" {
		t.Fatalf("expected only Default Ep, got %+v", defEps.Episodes)
	}

	// 6. requirePodcastFeed returns 500 without WWW-Authenticate on transient store errors
	origStore := application.Store
	application.Store = &failingPodcastStore{Store: origStore, getPodcastErr: io.ErrUnexpectedEOF}
	defer func() { application.Store = origStore }()

	errFeedReq := httptest.NewRequest(http.MethodGet, "/p/carol/podcast.xml", nil)
	errFeedRR := httptest.NewRecorder()
	h.ServeHTTP(errFeedRR, errFeedReq)
	if errFeedRR.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 on transient DB error, got %d", errFeedRR.Code)
	}
	if errFeedRR.Header().Get("WWW-Authenticate") != "" {
		t.Fatalf("unexpected WWW-Authenticate header on 500: %q", errFeedRR.Header().Get("WWW-Authenticate"))
	}
}

func TestThreeTierKeysAndRotation(t *testing.T) {
	_, cfg, h := testApp(t)
	cfg.AdminAPIKey = "super-admin-key"
	cfg.AgentAPIKey = "default-submit-key"

	callWithKey := func(key, method, path, body string) *httptest.ResponseRecorder {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		if key != "" {
			req.Header.Set("Authorization", "Bearer "+key)
		}
		h.ServeHTTP(rr, req)
		return rr
	}

	// 1. Default-feed submit key (AGENT_API_KEY when ADMIN_API_KEY is set) cannot create or list podcasts
	if rr := callWithKey("default-submit-key", http.MethodPost, "/v1/podcasts", `{"id":"alice","title":"Alice Briefing"}`); rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403 when default-submit-key creates podcast, got %d %s", rr.Code, rr.Body.String())
	}
	if rr := callWithKey("default-submit-key", http.MethodGet, "/v1/podcasts", ""); rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403 when default-submit-key lists podcasts, got %d", rr.Code)
	}

	// 2. Super key (ADMIN_API_KEY) creates alice and bob, returning per-user submit_key
	aliceRR := callWithKey("super-admin-key", http.MethodPost, "/v1/podcasts", `{"id":"alice","title":"Alice Briefing"}`)
	if aliceRR.Code != http.StatusCreated {
		t.Fatalf("admin create alice: %d %s", aliceRR.Code, aliceRR.Body.String())
	}
	var alice map[string]any
	if err := json.Unmarshal(aliceRR.Body.Bytes(), &alice); err != nil {
		t.Fatal(err)
	}
	aliceSubmitKey, _ := alice["submit_key"].(string)
	alicePass, _ := alice["password"].(string)
	aliceTok, _ := alice["token"].(string)
	if aliceSubmitKey == "" || alicePass == "" || aliceTok == "" {
		t.Fatalf("missing credentials on created podcast: %+v", alice)
	}

	bobRR := callWithKey("super-admin-key", http.MethodPost, "/v1/podcasts", `{"id":"bob","title":"Bob Briefing"}`)
	if bobRR.Code != http.StatusCreated {
		t.Fatalf("admin create bob: %d %s", bobRR.Code, bobRR.Body.String())
	}

	// 3. Default-feed submit key can publish to default feed, but 403 on alice's podcast
	defEpRR := callWithKey("default-submit-key", http.MethodPost, "/v1/episodes", `{"title":"Default Briefing","content":"Good morning. This is a default feed update."}`)
	if defEpRR.Code != http.StatusAccepted {
		t.Fatalf("default submitter default episode: %d %s", defEpRR.Code, defEpRR.Body.String())
	}
	var defEp struct {
		EpisodeID string `json:"episode_id"`
	}
	_ = json.Unmarshal(defEpRR.Body.Bytes(), &defEp)

	if rr := callWithKey("default-submit-key", http.MethodPost, "/v1/podcasts/alice/episodes", `{"title":"Alice Briefing","content":"Good morning Alice. Should be forbidden."}`); rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403 when default-submit-key posts to /v1/podcasts/alice/episodes, got %d", rr.Code)
	}
	if rr := callWithKey("default-submit-key", http.MethodPost, "/v1/episodes", `{"title":"Alice Briefing","content":"Good morning Alice. Should be forbidden.","podcast_id":"alice"}`); rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403 when default-submit-key posts to /v1/episodes with podcast_id=alice, got %d", rr.Code)
	}
	if rr := callWithKey("default-submit-key", http.MethodGet, "/v1/podcasts/alice", ""); rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403 when default-submit-key views alice podcast, got %d", rr.Code)
	}
	if rr := callWithKey("default-submit-key", http.MethodPost, "/v1/podcasts/alice/rotate", "{}"); rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403 when default-submit-key rotates alice podcast, got %d", rr.Code)
	}
	if rr := callWithKey("default-submit-key", http.MethodGet, "/v1/episodes?podcast_id=alice", ""); rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403 when default-submit-key lists alice episodes, got %d", rr.Code)
	}

	// 4. Per-user submit_key (aliceSubmitKey) permissions
	// Can publish via /v1/podcasts/alice/episodes
	aliceEp1RR := callWithKey(aliceSubmitKey, http.MethodPost, "/v1/podcasts/alice/episodes", `{"title":"Alice Ep 1","content":"Good morning Alice. First private update."}`)
	if aliceEp1RR.Code != http.StatusAccepted {
		t.Fatalf("alice submit_key post /v1/podcasts/alice/episodes: %d %s", aliceEp1RR.Code, aliceEp1RR.Body.String())
	}
	// Can publish via /v1/episodes without podcast_id (auto-scoped to alice)
	aliceEp2RR := callWithKey(aliceSubmitKey, http.MethodPost, "/v1/episodes", `{"title":"Alice Ep 2","content":"Good morning Alice. Auto-scoped private update."}`)
	if aliceEp2RR.Code != http.StatusAccepted {
		t.Fatalf("alice submit_key post /v1/episodes: %d %s", aliceEp2RR.Code, aliceEp2RR.Body.String())
	}
	var aliceEp2 struct {
		EpisodeID string `json:"episode_id"`
		PodcastID string `json:"podcast_id"`
	}
	_ = json.Unmarshal(aliceEp2RR.Body.Bytes(), &aliceEp2)
	if aliceEp2.PodcastID != "alice" {
		t.Fatalf("expected auto-scoped podcast_id=alice, got %q", aliceEp2.PodcastID)
	}

	// Can GET /v1/podcasts/alice, GET /v1/episodes (auto-scoped to alice), and GET /v1/episodes/{aliceEp}
	if rr := callWithKey(aliceSubmitKey, http.MethodGet, "/v1/podcasts/alice", ""); rr.Code != http.StatusOK {
		t.Fatalf("alice submit_key get /v1/podcasts/alice: %d %s", rr.Code, rr.Body.String())
	}
	if rr := callWithKey(aliceSubmitKey, http.MethodGet, "/v1/episodes/"+aliceEp2.EpisodeID, ""); rr.Code != http.StatusOK {
		t.Fatalf("alice submit_key get alice episode: %d %s", rr.Code, rr.Body.String())
	}
	aliceListRR := callWithKey(aliceSubmitKey, http.MethodGet, "/v1/episodes", "")
	if aliceListRR.Code != http.StatusOK {
		t.Fatalf("alice submit_key list episodes: %d %s", aliceListRR.Code, aliceListRR.Body.String())
	}
	var aliceListed struct {
		Episodes []map[string]any `json:"episodes"`
	}
	_ = json.Unmarshal(aliceListRR.Body.Bytes(), &aliceListed)
	if len(aliceListed.Episodes) != 2 {
		t.Fatalf("expected 2 alice episodes, got %d", len(aliceListed.Episodes))
	}

	// CANNOT create/list podcasts, view/publish/rotate bob, or access default episodes
	if rr := callWithKey(aliceSubmitKey, http.MethodPost, "/v1/podcasts", `{"id":"eve","title":"Eve"}`); rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403 when alice submit_key creates podcast, got %d", rr.Code)
	}
	if rr := callWithKey(aliceSubmitKey, http.MethodGet, "/v1/podcasts", ""); rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403 when alice submit_key lists podcasts, got %d", rr.Code)
	}
	if rr := callWithKey(aliceSubmitKey, http.MethodGet, "/v1/podcasts/bob", ""); rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403 when alice submit_key gets bob, got %d", rr.Code)
	}
	if rr := callWithKey(aliceSubmitKey, http.MethodPost, "/v1/podcasts/bob/episodes", `{"title":"Bob Ep","content":"Good morning Bob. Should be forbidden."}`); rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403 when alice submit_key publishes to /v1/podcasts/bob/episodes, got %d", rr.Code)
	}
	if rr := callWithKey(aliceSubmitKey, http.MethodPost, "/v1/episodes", `{"title":"Bob Ep","content":"Good morning Bob. Should be forbidden.","podcast_id":"bob"}`); rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403 when alice submit_key publishes to /v1/episodes with podcast_id=bob, got %d", rr.Code)
	}
	if rr := callWithKey(aliceSubmitKey, http.MethodPost, "/v1/podcasts/bob/rotate", "{}"); rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403 when alice submit_key rotates bob, got %d", rr.Code)
	}
	if rr := callWithKey(aliceSubmitKey, http.MethodGet, "/v1/episodes?only_default=true", ""); rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403 when alice submit_key lists only_default, got %d", rr.Code)
	}
	if rr := callWithKey(aliceSubmitKey, http.MethodGet, "/v1/episodes?podcast_id=bob", ""); rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403 when alice submit_key lists bob episodes, got %d", rr.Code)
	}
	if rr := callWithKey(aliceSubmitKey, http.MethodGet, "/v1/episodes/"+defEp.EpisodeID, ""); rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403 when alice submit_key gets default episode, got %d", rr.Code)
	}
	if rr := callWithKey("default-submit-key", http.MethodGet, "/v1/episodes/"+aliceEp2.EpisodeID, ""); rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403 when default-submit-key gets alice episode, got %d", rr.Code)
	}

	// 5. Per-user submit_key CANNOT read RSS feed (/p/alice/podcast.xml)
	for _, req := range []*http.Request{
		func() *http.Request {
			r := httptest.NewRequest(http.MethodGet, "/p/alice/podcast.xml", nil)
			r.Header.Set("Authorization", "Bearer "+aliceSubmitKey)
			return r
		}(),
		func() *http.Request {
			r := httptest.NewRequest(http.MethodGet, "/p/alice/podcast.xml", nil)
			r.SetBasicAuth("alice", aliceSubmitKey)
			return r
		}(),
		httptest.NewRequest(http.MethodGet, "/p/alice/podcast.xml?token="+aliceSubmitKey, nil),
	} {
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusUnauthorized {
			t.Fatalf("submit_key must not authenticate RSS feed, got %d", rr.Code)
		}
	}

	// 6. Listener credential rotation via POST /v1/podcasts/alice/rotate
	rotRR := callWithKey(aliceSubmitKey, http.MethodPost, "/v1/podcasts/alice/rotate", "")
	if rotRR.Code != http.StatusOK {
		t.Fatalf("rotate alice: %d %s", rotRR.Code, rotRR.Body.String())
	}
	var rotated map[string]any
	if err := json.Unmarshal(rotRR.Body.Bytes(), &rotated); err != nil {
		t.Fatal(err)
	}
	newPass, _ := rotated["password"].(string)
	newTok, _ := rotated["token"].(string)
	newSubmitKey, _ := rotated["submit_key"].(string)
	if newPass == "" || newPass == alicePass {
		t.Fatalf("expected rotated password, old=%q new=%q", alicePass, newPass)
	}
	if newTok == "" || newTok == aliceTok {
		t.Fatalf("expected rotated token, old=%q new=%q", aliceTok, newTok)
	}
	if newSubmitKey != aliceSubmitKey {
		t.Fatalf("expected submit_key unchanged when rotate_submit_key=false, got %q", newSubmitKey)
	}

	// Old password and old token now return 401 on /p/alice/podcast.xml
	oldBasicReq := httptest.NewRequest(http.MethodGet, "/p/alice/podcast.xml", nil)
	oldBasicReq.SetBasicAuth("alice", alicePass)
	oldBasicRR := httptest.NewRecorder()
	h.ServeHTTP(oldBasicRR, oldBasicReq)
	if oldBasicRR.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 with old password after rotation, got %d", oldBasicRR.Code)
	}
	oldTokRR := httptest.NewRecorder()
	h.ServeHTTP(oldTokRR, httptest.NewRequest(http.MethodGet, "/p/alice/podcast.xml?token="+aliceTok, nil))
	if oldTokRR.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 with old token after rotation, got %d", oldTokRR.Code)
	}

	// New password and new token return 200 on /p/alice/podcast.xml
	newBasicReq := httptest.NewRequest(http.MethodGet, "/p/alice/podcast.xml", nil)
	newBasicReq.SetBasicAuth("alice", newPass)
	newBasicRR := httptest.NewRecorder()
	h.ServeHTTP(newBasicRR, newBasicReq)
	if newBasicRR.Code != http.StatusOK {
		t.Fatalf("expected 200 with new password after rotation, got %d", newBasicRR.Code)
	}
	newTokRR := httptest.NewRecorder()
	h.ServeHTTP(newTokRR, httptest.NewRequest(http.MethodGet, "/p/alice/podcast.xml?token="+newTok, nil))
	if newTokRR.Code != http.StatusOK {
		t.Fatalf("expected 200 with new token after rotation, got %d", newTokRR.Code)
	}

	// Rotate submit_key via super-admin-key, plus 404 and 400 error cases
	rotSubmitRR := callWithKey("super-admin-key", http.MethodPost, "/v1/podcasts/alice/rotate", `{"rotate_listener":false,"rotate_submit_key":true}`)
	if rotSubmitRR.Code != http.StatusOK {
		t.Fatalf("rotate submit_key: %d %s", rotSubmitRR.Code, rotSubmitRR.Body.String())
	}
	var rotatedSubmit map[string]any
	_ = json.Unmarshal(rotSubmitRR.Body.Bytes(), &rotatedSubmit)
	rotatedKey, _ := rotatedSubmit["submit_key"].(string)
	if rotatedKey == "" || rotatedKey == aliceSubmitKey {
		t.Fatalf("expected new submit_key, got %q", rotatedKey)
	}
	if rr := callWithKey(aliceSubmitKey, http.MethodGet, "/v1/podcasts/alice", ""); rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 with revoked submit_key, got %d", rr.Code)
	}
	if rr := callWithKey(rotatedKey, http.MethodGet, "/v1/podcasts/alice", ""); rr.Code != http.StatusOK {
		t.Fatalf("expected 200 with new submit_key, got %d", rr.Code)
	}
	if rr := callWithKey("super-admin-key", http.MethodPost, "/v1/podcasts/unknown/rotate", `{}`); rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404 rotating unknown podcast, got %d", rr.Code)
	}
	if rr := callWithKey("super-admin-key", http.MethodPost, "/v1/podcasts/alice/rotate", `{"unknown":true}`); rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 on unknown field in rotate body, got %d", rr.Code)
	}
}

func TestPodcastAndEpisodeMetadataUpdatesAndChapters(t *testing.T) {
	_, cfg, h := testApp(t)
	cfg.AdminAPIKey = "super-admin-key"
	cfg.AgentAPIKey = "default-submit-key"

	callWithKey := func(key, method, path, body string) *httptest.ResponseRecorder {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		if key != "" {
			req.Header.Set("Authorization", "Bearer "+key)
		}
		h.ServeHTTP(rr, req)
		return rr
	}

	// 1. Create podcast with custom image_url
	createRR := callWithKey("super-admin-key", http.MethodPost, "/v1/podcasts", `{
		"id":"alice",
		"title":"Alice Briefing",
		"description":"Initial description",
		"author":"Initial Author",
		"image_url":"https://example.com/alice-v1.png"
	}`)
	if createRR.Code != http.StatusCreated {
		t.Fatalf("create alice: %d %s", createRR.Code, createRR.Body.String())
	}
	var alice map[string]any
	if err := json.Unmarshal(createRR.Body.Bytes(), &alice); err != nil {
		t.Fatal(err)
	}
	if alice["image_url"] != "https://example.com/alice-v1.png" {
		t.Fatalf("expected image_url on create, got %+v", alice)
	}
	aliceSubmitKey, _ := alice["submit_key"].(string)
	aliceTok, _ := alice["token"].(string)

	// Create bob for cross-show tests
	bobRR := callWithKey("super-admin-key", http.MethodPost, "/v1/podcasts", `{"id":"bob","title":"Bob Show"}`)
	if bobRR.Code != http.StatusCreated {
		t.Fatalf("create bob: %d %s", bobRR.Code, bobRR.Body.String())
	}
	var bob map[string]any
	_ = json.Unmarshal(bobRR.Body.Bytes(), &bob)
	bobTok, _ := bob["token"].(string)

	// 2. PATCH /v1/podcasts/alice using alice's submit_key
	patchShowRR := callWithKey(aliceSubmitKey, http.MethodPatch, "/v1/podcasts/alice", `{
		"title":"Alice Executive Briefing",
		"description":"Updated description for Alice",
		"author":"Alice AI",
		"image_url":"https://example.com/alice-v2.png"
	}`)
	if patchShowRR.Code != http.StatusOK {
		t.Fatalf("patch alice: %d %s", patchShowRR.Code, patchShowRR.Body.String())
	}
	var updatedAlice map[string]any
	if err := json.Unmarshal(patchShowRR.Body.Bytes(), &updatedAlice); err != nil {
		t.Fatal(err)
	}
	if updatedAlice["title"] != "Alice Executive Briefing" ||
		updatedAlice["description"] != "Updated description for Alice" ||
		updatedAlice["author"] != "Alice AI" ||
		updatedAlice["image_url"] != "https://example.com/alice-v2.png" {
		t.Fatalf("unexpected updated podcast: %+v", updatedAlice)
	}

	// Authorization and error checks on PATCH /v1/podcasts/{id}
	if rr := callWithKey("default-submit-key", http.MethodPatch, "/v1/podcasts/alice", `{"title":"No"}`); rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403 when default-submit-key updates alice, got %d", rr.Code)
	}
	if rr := callWithKey(aliceSubmitKey, http.MethodPatch, "/v1/podcasts/bob", `{"title":"No"}`); rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403 when alice updates bob, got %d", rr.Code)
	}
	if rr := callWithKey("super-admin-key", http.MethodPatch, "/v1/podcasts/unknown", `{"title":"No"}`); rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404 on unknown podcast update, got %d", rr.Code)
	}
	if rr := callWithKey(aliceSubmitKey, http.MethodPatch, "/v1/podcasts/alice", `{"image_url":"ftp://bad"}`); rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 on bad image_url, got %d %s", rr.Code, rr.Body.String())
	}

	// 3. Publish episode with description, image_url, and chapters
	epRR := callWithKey(aliceSubmitKey, http.MethodPost, "/v1/podcasts/alice/episodes", `{
		"title":"Morning Episode",
		"content":"Good morning Alice. Here is the full spoken transcript for today.",
		"description":"Initial show notes.",
		"category":"Tech",
		"image_url":"https://example.com/ep-v1.png",
		"chapters":[
			{"start_seconds":0,"title":"Intro","url":"https://example.com/intro","image_url":"https://example.com/intro.png"},
			{"start_seconds":30,"title":"Deep Dive"}
		]
	}`)
	if epRR.Code != http.StatusAccepted {
		t.Fatalf("create episode: %d %s", epRR.Code, epRR.Body.String())
	}
	var createdEp struct {
		EpisodeID string `json:"episode_id"`
	}
	_ = json.Unmarshal(epRR.Body.Bytes(), &createdEp)

	// Wait for READY
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		gr := callWithKey(aliceSubmitKey, http.MethodGet, "/v1/episodes/"+createdEp.EpisodeID, "")
		var got map[string]any
		_ = json.Unmarshal(gr.Body.Bytes(), &got)
		if got["status"] == "READY" {
			break
		}
		if got["status"] == "FAILED" {
			t.Fatalf("episode failed: %v", got)
		}
		time.Sleep(25 * time.Millisecond)
	}

	// 4. PATCH /v1/episodes/{id}
	patchEpRR := callWithKey(aliceSubmitKey, http.MethodPatch, "/v1/episodes/"+createdEp.EpisodeID, `{
		"title":"Morning Episode (Updated)",
		"description":"Updated episode show notes.",
		"category":"AI News",
		"image_url":"https://example.com/ep-v2.png",
		"chapters":[
			{"start_seconds":0,"title":"Welcome","url":"https://example.com/welcome","image_url":"https://example.com/welcome.png"},
			{"start_seconds":75,"title":"Main Story"}
		]
	}`)
	if patchEpRR.Code != http.StatusOK {
		t.Fatalf("patch episode: %d %s", patchEpRR.Code, patchEpRR.Body.String())
	}
	var updatedEp map[string]any
	if err := json.Unmarshal(patchEpRR.Body.Bytes(), &updatedEp); err != nil {
		t.Fatal(err)
	}
	if updatedEp["title"] != "Morning Episode (Updated)" ||
		updatedEp["description"] != "Updated episode show notes." ||
		updatedEp["category"] != "AI News" ||
		updatedEp["image_url"] != "https://example.com/ep-v2.png" {
		t.Fatalf("unexpected updated episode: %+v", updatedEp)
	}
	chaps, _ := updatedEp["chapters"].([]any)
	if len(chaps) != 2 {
		t.Fatalf("expected 2 chapters in response, got %+v", updatedEp["chapters"])
	}

	// Authorization and error checks on PATCH /v1/episodes/{id}
	if rr := callWithKey("default-submit-key", http.MethodPatch, "/v1/episodes/"+createdEp.EpisodeID, `{"title":"No"}`); rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403 when default-submit-key updates alice episode, got %d", rr.Code)
	}
	if rr := callWithKey(aliceSubmitKey, http.MethodPatch, "/v1/episodes/ep_unknown", `{"title":"No"}`); rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404 on unknown episode update, got %d", rr.Code)
	}
	if rr := callWithKey(aliceSubmitKey, http.MethodPatch, "/v1/episodes/"+createdEp.EpisodeID, `{"title":""}`); rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 on empty title update, got %d", rr.Code)
	}

	// 5. Verify RSS feed /p/alice/podcast.xml includes updated show & episode metadata and chapters
	feedRR := httptest.NewRecorder()
	h.ServeHTTP(feedRR, httptest.NewRequest(http.MethodGet, "/p/alice/podcast.xml?token="+aliceTok, nil))
	if feedRR.Code != http.StatusOK {
		t.Fatalf("alice feed: %d %s", feedRR.Code, feedRR.Body.String())
	}
	feedXML := feedRR.Body.String()
	for _, want := range []string{
		`<title>Alice Executive Briefing</title>`,
		`<itunes:image href="https://example.com/alice-v2.png"></itunes:image>`,
		`<title>Morning Episode (Updated)</title>`,
		`Updated episode show notes.`,
		`<itunes:image href="https://example.com/ep-v2.png"></itunes:image>`,
		`<psc:chapters version="1.2">`,
		`<psc:chapter start="00:00:00" title="Welcome" href="https://example.com/welcome" image="https://example.com/welcome.png"></psc:chapter>`,
		`<psc:chapter start="00:01:15" title="Main Story"></psc:chapter>`,
		`<podcast:chapters url="http://podcast.example.com/p/alice/episodes/` + createdEp.EpisodeID + `/chapters.json?token=` + aliceTok + `" type="application/json+chapters"></podcast:chapters>`,
	} {
		if !strings.Contains(feedXML, want) {
			t.Fatalf("feed missing %q:\n%s", want, feedXML)
		}
	}

	// 6. Verify GET /p/alice/episodes/{ep}/chapters.json and cross-show isolation
	chapRR := httptest.NewRecorder()
	h.ServeHTTP(chapRR, httptest.NewRequest(http.MethodGet, "/p/alice/episodes/"+createdEp.EpisodeID+"/chapters.json?token="+aliceTok, nil))
	if chapRR.Code != http.StatusOK {
		t.Fatalf("alice chapters.json: %d %s", chapRR.Code, chapRR.Body.String())
	}
	if ct := chapRR.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json+chapters") {
		t.Fatalf("unexpected chapters content-type: %s", ct)
	}
	var chapDoc struct {
		Version  string `json:"version"`
		Chapters []struct {
			StartTime float64 `json:"startTime"`
			Title     string  `json:"title"`
			URL       string  `json:"url"`
			Img       string  `json:"img"`
		} `json:"chapters"`
	}
	if err := json.Unmarshal(chapRR.Body.Bytes(), &chapDoc); err != nil {
		t.Fatal(err)
	}
	if chapDoc.Version != "1.2.0" || len(chapDoc.Chapters) != 2 || chapDoc.Chapters[0].Title != "Welcome" || chapDoc.Chapters[1].StartTime != 75 {
		t.Fatalf("unexpected chapters.json payload: %+v", chapDoc)
	}

	// Default /episodes/{id}/chapters.json must 404 for alice's episode
	defChapRR := httptest.NewRecorder()
	h.ServeHTTP(defChapRR, httptest.NewRequest(http.MethodGet, "/episodes/"+createdEp.EpisodeID+"/chapters.json?token=feed-token", nil))
	if defChapRR.Code != http.StatusNotFound {
		t.Fatalf("expected 404 on default chapters endpoint for alice episode, got %d", defChapRR.Code)
	}

	// Bob's /p/bob/episodes/{ep}/chapters.json must 404 for alice's episode
	bobChapRR := httptest.NewRecorder()
	h.ServeHTTP(bobChapRR, httptest.NewRequest(http.MethodGet, "/p/bob/episodes/"+createdEp.EpisodeID+"/chapters.json?token="+bobTok, nil))
	if bobChapRR.Code != http.StatusNotFound {
		t.Fatalf("expected 404 on bob chapters endpoint for alice episode, got %d", bobChapRR.Code)
	}
}

func makeInlinePNG(t *testing.T, c color.RGBA) ([]byte, string) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			img.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	raw := buf.Bytes()
	return raw, "data:image/png;base64," + base64.StdEncoding.EncodeToString(raw)
}

func TestInlineCoverEndToEnd(t *testing.T) {
	_, _, h := testApp(t)

	call := func(method, path, body string) *httptest.ResponseRecorder {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		req.Header.Set("Authorization", "Bearer secret-key")
		h.ServeHTTP(rr, req)
		return rr
	}

	podPNG1, podURI1 := makeInlinePNG(t, color.RGBA{R: 200, G: 20, B: 40, A: 255})
	podPNG2, podURI2 := makeInlinePNG(t, color.RGBA{R: 20, G: 200, B: 40, A: 255})
	epPNG1, epURI1 := makeInlinePNG(t, color.RGBA{R: 40, G: 60, B: 220, A: 255})
	epPNG2, epURI2 := makeInlinePNG(t, color.RGBA{R: 220, G: 180, B: 20, A: 255})

	// 1. POST /v1/podcasts with inline data URI
	createPodRR := call(http.MethodPost, "/v1/podcasts", `{"id":"alice","title":"Alice Inline Show","image_url":"`+podURI1+`"}`)
	if createPodRR.Code != http.StatusCreated {
		t.Fatalf("create alice: %d %s", createPodRR.Code, createPodRR.Body.String())
	}
	var alice map[string]any
	if err := json.Unmarshal(createPodRR.Body.Bytes(), &alice); err != nil {
		t.Fatal(err)
	}
	if alice["image_url"] != "http://podcast.example.com/p/alice/cover.png" {
		t.Fatalf("expected rewritten podcast image_url, got %v", alice["image_url"])
	}
	aliceTok, _ := alice["token"].(string)

	// GET /p/alice/cover.png serves exact podPNG1 bytes
	coverRR := httptest.NewRecorder()
	h.ServeHTTP(coverRR, httptest.NewRequest(http.MethodGet, "/p/alice/cover.png", nil))
	if coverRR.Code != http.StatusOK || !bytes.Equal(coverRR.Body.Bytes(), podPNG1) {
		t.Fatalf("GET /p/alice/cover.png status=%d len=%d want=%d", coverRR.Code, coverRR.Body.Len(), len(podPNG1))
	}

	// 2. PATCH /v1/podcasts/alice with updated inline data URI
	patchPodRR := call(http.MethodPatch, "/v1/podcasts/alice", `{"image_url":"`+podURI2+`"}`)
	if patchPodRR.Code != http.StatusOK {
		t.Fatalf("patch alice: %d %s", patchPodRR.Code, patchPodRR.Body.String())
	}
	coverRR2 := httptest.NewRecorder()
	h.ServeHTTP(coverRR2, httptest.NewRequest(http.MethodGet, "/p/alice/cover.png", nil))
	if coverRR2.Code != http.StatusOK || !bytes.Equal(coverRR2.Body.Bytes(), podPNG2) {
		t.Fatalf("GET /p/alice/cover.png after patch status=%d bytes mismatch", coverRR2.Code)
	}

	// 3. POST /v1/episodes for alice with inline data URI
	createEpRR := call(http.MethodPost, "/v1/episodes", `{"title":"Inline Art Episode","content":"Good morning Alice. This episode has an inline PNG cover.","podcast_id":"alice","image_url":"`+epURI1+`"}`)
	if createEpRR.Code != http.StatusAccepted {
		t.Fatalf("create episode: %d %s", createEpRR.Code, createEpRR.Body.String())
	}
	var createdEp struct {
		EpisodeID string `json:"episode_id"`
	}
	if err := json.Unmarshal(createEpRR.Body.Bytes(), &createdEp); err != nil {
		t.Fatal(err)
	}

	wantEpCoverURL := "http://podcast.example.com/p/alice/episodes/" + createdEp.EpisodeID + "/cover.png"

	// Wait for READY
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		gr := call(http.MethodGet, "/v1/episodes/"+createdEp.EpisodeID, "")
		var got map[string]any
		_ = json.Unmarshal(gr.Body.Bytes(), &got)
		if got["image_url"] != wantEpCoverURL {
			t.Fatalf("expected episode image_url %q, got %v", wantEpCoverURL, got["image_url"])
		}
		if got["status"] == "READY" {
			break
		}
		if got["status"] == "FAILED" {
			t.Fatalf("episode failed: %v", got)
		}
		time.Sleep(25 * time.Millisecond)
	}

	// GET /p/alice/episodes/{ep}/cover.png serves exact epPNG1 bytes
	epCoverRR := httptest.NewRecorder()
	h.ServeHTTP(epCoverRR, httptest.NewRequest(http.MethodGet, "/p/alice/episodes/"+createdEp.EpisodeID+"/cover.png", nil))
	if epCoverRR.Code != http.StatusOK || !bytes.Equal(epCoverRR.Body.Bytes(), epPNG1) {
		t.Fatalf("GET /p/alice/episodes/%s/cover.png status=%d bytes mismatch", createdEp.EpisodeID, epCoverRR.Code)
	}

	// Default /episodes/{ep}/cover.png and wrong-show /p/bob/episodes/{ep}/cover.png must 404
	wrongDefRR := httptest.NewRecorder()
	h.ServeHTTP(wrongDefRR, httptest.NewRequest(http.MethodGet, "/episodes/"+createdEp.EpisodeID+"/cover.png", nil))
	if wrongDefRR.Code != http.StatusNotFound {
		t.Fatalf("expected 404 on default episode cover route for alice episode, got %d", wrongDefRR.Code)
	}
	wrongShowRR := httptest.NewRecorder()
	h.ServeHTTP(wrongShowRR, httptest.NewRequest(http.MethodGet, "/p/bob/episodes/"+createdEp.EpisodeID+"/cover.png", nil))
	if wrongShowRR.Code != http.StatusNotFound {
		t.Fatalf("expected 404 on wrong show episode cover route, got %d", wrongShowRR.Code)
	}

	// 4. PATCH /v1/episodes/{id} with new inline data URI
	patchEpRR := call(http.MethodPatch, "/v1/episodes/"+createdEp.EpisodeID, `{"image_url":"`+epURI2+`"}`)
	if patchEpRR.Code != http.StatusOK {
		t.Fatalf("patch episode: %d %s", patchEpRR.Code, patchEpRR.Body.String())
	}
	epCoverRR2 := httptest.NewRecorder()
	h.ServeHTTP(epCoverRR2, httptest.NewRequest(http.MethodGet, "/p/alice/episodes/"+createdEp.EpisodeID+"/cover.png", nil))
	if epCoverRR2.Code != http.StatusOK || !bytes.Equal(epCoverRR2.Body.Bytes(), epPNG2) {
		t.Fatalf("GET /p/alice/episodes/%s/cover.png after patch status=%d bytes mismatch", createdEp.EpisodeID, epCoverRR2.Code)
	}

	// 5. Verify RSS feed <itunes:image> has HTTP URLs (never raw data: URIs)
	feedRR := httptest.NewRecorder()
	h.ServeHTTP(feedRR, httptest.NewRequest(http.MethodGet, "/p/alice/podcast.xml?token="+aliceTok, nil))
	if feedRR.Code != http.StatusOK {
		t.Fatalf("alice feed: %d %s", feedRR.Code, feedRR.Body.String())
	}
	feedXML := feedRR.Body.String()
	if strings.Contains(feedXML, "data:image/") {
		t.Fatalf("RSS XML must not contain raw data:image/ URI:\n%s", feedXML)
	}
	if !strings.Contains(feedXML, `<itunes:image href="http://podcast.example.com/p/alice/cover.png"></itunes:image>`) {
		t.Fatalf("RSS XML missing hosted podcast cover URL:\n%s", feedXML)
	}
	if !strings.Contains(feedXML, `<itunes:image href="`+wantEpCoverURL+`"></itunes:image>`) {
		t.Fatalf("RSS XML missing hosted episode cover URL:\n%s", feedXML)
	}
}
