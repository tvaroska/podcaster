package mcp

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/tvaroska/podcaster/internal/app"
	"github.com/tvaroska/podcaster/internal/auth"
	"github.com/tvaroska/podcaster/internal/config"
	"github.com/tvaroska/podcaster/internal/episode"
)

func connectSessionWithContext(t *testing.T, srv *mcpsdk.Server, serverCtx context.Context) *mcpsdk.ClientSession {
	t.Helper()
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "test-client", Version: "v0.0.1"}, nil)
	t1, t2 := mcpsdk.NewInMemoryTransports()
	if _, err := srv.Connect(serverCtx, t1, nil); err != nil {
		t.Fatal(err)
	}
	session, err := client.Connect(context.Background(), t2, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func testSession(t *testing.T) (*app.App, *mcpsdk.ClientSession) {
	t.Helper()
	dir := t.TempDir()
	cfg := &config.Config{
		ListenAddr:         ":0",
		PublicBaseURL:      "https://podcast.example.com/subpath",
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
		PodcastDescription: "Default description",
		PodcastAuthor:      "Default Author",
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

	srv := NewServer(application)
	if HTTPHandler(srv) == nil {
		t.Fatal("expected non-nil HTTPHandler")
	}
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "test-client", Version: "v0.0.1"}, nil)
	t1, t2 := mcpsdk.NewInMemoryTransports()
	if _, err := srv.Connect(context.Background(), t1, nil); err != nil {
		t.Fatal(err)
	}
	session, err := client.Connect(context.Background(), t2, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return application, session
}

func TestMCPToolsEndToEnd(t *testing.T) {
	ctx := context.Background()
	_, session := testSession(t)

	// 1. create_podcast
	createRes, err := session.CallTool(ctx, &mcpsdk.CallToolParams{
		Name: "create_podcast",
		Arguments: map[string]any{
			"id":          "alice",
			"title":       "Alice Daily",
			"description": "Private updates for Alice",
			"author":      "Alice Bot",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if createRes.IsError {
		t.Fatalf("unexpected error: %+v", createRes)
	}
	var createdShow CreatePodcastOutput
	if err := json.Unmarshal([]byte(createRes.Content[0].(*mcpsdk.TextContent).Text), &createdShow); err != nil {
		t.Fatal(err)
	}
	if createdShow.ID != "alice" || createdShow.Description != "Private updates for Alice" || createdShow.Author != "Alice Bot" || createdShow.CreatedAt == "" || createdShow.SubmitKey == "" {
		t.Fatalf("unexpected create_podcast output: %+v", createdShow)
	}
	if createdShow.FeedURL != "https://podcast.example.com/subpath/p/alice/podcast.xml" {
		t.Fatalf("unexpected feed_url: %s", createdShow.FeedURL)
	}
	if !strings.HasSuffix(createdShow.SubscribeURL, "@podcast.example.com/subpath/p/alice/podcast.xml") {
		t.Fatalf("subscribe_url lost path prefix: %s", createdShow.SubscribeURL)
	}

	// Duplicate create_podcast -> IsError
	dupRes, err := session.CallTool(ctx, &mcpsdk.CallToolParams{
		Name:      "create_podcast",
		Arguments: map[string]any{"id": "alice", "title": "Alice Daily"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !dupRes.IsError {
		t.Fatal("expected IsError on duplicate podcast")
	}

	// Invalid create_podcast -> IsError
	badShowRes, err := session.CallTool(ctx, &mcpsdk.CallToolParams{
		Name:      "create_podcast",
		Arguments: map[string]any{"id": "bad--slug", "title": "Bad"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !badShowRes.IsError {
		t.Fatal("expected IsError on invalid podcast slug")
	}

	// 2. list_podcasts
	listShowsRes, err := session.CallTool(ctx, &mcpsdk.CallToolParams{
		Name:      "list_podcasts",
		Arguments: map[string]any{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if listShowsRes.IsError {
		t.Fatalf("list_podcasts error: %+v", listShowsRes)
	}
	var listedShows ListPodcastsOutput
	if err := json.Unmarshal([]byte(listShowsRes.Content[0].(*mcpsdk.TextContent).Text), &listedShows); err != nil {
		t.Fatal(err)
	}
	if len(listedShows.Podcasts) != 1 || listedShows.Podcasts[0].ID != "alice" {
		t.Fatalf("unexpected list_podcasts: %+v", listedShows)
	}

	// 3. get_podcast (with whitespace & uppercase to test trimming/lowercasing)
	getShowRes, err := session.CallTool(ctx, &mcpsdk.CallToolParams{
		Name:      "get_podcast",
		Arguments: map[string]any{"podcast_id": "  ALICE  "},
	})
	if err != nil {
		t.Fatal(err)
	}
	if getShowRes.IsError {
		t.Fatalf("get_podcast error: %+v", getShowRes)
	}
	var gotShow CreatePodcastOutput
	if err := json.Unmarshal([]byte(getShowRes.Content[0].(*mcpsdk.TextContent).Text), &gotShow); err != nil {
		t.Fatal(err)
	}
	if gotShow.ID != "alice" || gotShow.Token != createdShow.Token {
		t.Fatalf("unexpected get_podcast: %+v", gotShow)
	}

	// get_podcast error paths
	for _, args := range []map[string]any{
		{},
		{"podcast_id": "   "},
		{"id": "nonexistent"},
	} {
		errRes, err := session.CallTool(ctx, &mcpsdk.CallToolParams{
			Name:      "get_podcast",
			Arguments: args,
		})
		if err != nil {
			t.Fatal(err)
		}
		if !errRes.IsError {
			t.Fatalf("expected IsError for args %v", args)
		}
	}

	// 4. publish_agent_update
	pubRes, err := session.CallTool(ctx, &mcpsdk.CallToolParams{
		Name: "publish_agent_update",
		Arguments: map[string]any{
			"title":      "Alice Briefing",
			"content":    "Good morning Alice. Here are your private updates for today.",
			"category":   "Daily Briefing",
			"podcast_id": "alice",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if pubRes.IsError {
		t.Fatalf("publish_agent_update error: %+v", pubRes)
	}
	var pubOut PublishOutput
	if err := json.Unmarshal([]byte(pubRes.Content[0].(*mcpsdk.TextContent).Text), &pubOut); err != nil {
		t.Fatal(err)
	}
	if pubOut.Status != "QUEUED" || pubOut.PodcastID != "alice" || pubOut.CreatedAt == "" || !strings.HasPrefix(pubOut.EpisodeID, "ep_") {
		t.Fatalf("unexpected publish output: %+v", pubOut)
	}

	// publish_agent_update validation error
	badPubRes, err := session.CallTool(ctx, &mcpsdk.CallToolParams{
		Name:      "publish_agent_update",
		Arguments: map[string]any{"title": "Short", "content": "tiny"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !badPubRes.IsError {
		t.Fatal("expected IsError on short content")
	}

	// 5. get_episode_status (with surrounding whitespace)
	statusRes, err := session.CallTool(ctx, &mcpsdk.CallToolParams{
		Name:      "get_episode_status",
		Arguments: map[string]any{"episode_id": "  " + pubOut.EpisodeID + "  "},
	})
	if err != nil {
		t.Fatal(err)
	}
	if statusRes.IsError {
		t.Fatalf("get_episode_status error: %+v", statusRes)
	}
	var statusOut GetStatusOutput
	if err := json.Unmarshal([]byte(statusRes.Content[0].(*mcpsdk.TextContent).Text), &statusOut); err != nil {
		t.Fatal(err)
	}
	if statusOut.EpisodeID != pubOut.EpisodeID || statusOut.PodcastID != "alice" || statusOut.Category != "Daily Briefing" || statusOut.VoiceID == "" || statusOut.CreatedAt == "" {
		t.Fatalf("unexpected status output: %+v", statusOut)
	}

	// get_episode_status error paths
	for _, id := range []string{"", "   ", "ep_nonexistent"} {
		errRes, err := session.CallTool(ctx, &mcpsdk.CallToolParams{
			Name:      "get_episode_status",
			Arguments: map[string]any{"episode_id": id},
		})
		if err != nil {
			t.Fatal(err)
		}
		if !errRes.IsError {
			t.Fatalf("expected IsError for episode_id %q", id)
		}
	}

	// 6. list_episodes
	listEpRes, err := session.CallTool(ctx, &mcpsdk.CallToolParams{
		Name:      "list_episodes",
		Arguments: map[string]any{"podcast_id": "alice", "limit": 10},
	})
	if err != nil {
		t.Fatal(err)
	}
	if listEpRes.IsError {
		t.Fatalf("list_episodes error: %+v", listEpRes)
	}
	var listedEps ListEpisodesOutput
	if err := json.Unmarshal([]byte(listEpRes.Content[0].(*mcpsdk.TextContent).Text), &listedEps); err != nil {
		t.Fatal(err)
	}
	if len(listedEps.Episodes) != 1 || listedEps.Episodes[0].EpisodeID != pubOut.EpisodeID {
		t.Fatalf("unexpected list_episodes output: %+v", listedEps)
	}

	// 7. rotate_podcast_credentials
	rotRes, err := session.CallTool(ctx, &mcpsdk.CallToolParams{
		Name:      "rotate_podcast_credentials",
		Arguments: map[string]any{"podcast_id": "alice"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if rotRes.IsError {
		t.Fatalf("rotate_podcast_credentials error: %+v", rotRes)
	}
	var rotShow CreatePodcastOutput
	if err := json.Unmarshal([]byte(rotRes.Content[0].(*mcpsdk.TextContent).Text), &rotShow); err != nil {
		t.Fatal(err)
	}
	if rotShow.Password == createdShow.Password || rotShow.Token == createdShow.Token {
		t.Fatalf("expected rotated password/token: old=%+v new=%+v", createdShow, rotShow)
	}
	if rotShow.SubmitKey != createdShow.SubmitKey {
		t.Fatalf("expected submit_key unchanged, got %s", rotShow.SubmitKey)
	}

	// Rotate submit_key only
	rotKeyRes, err := session.CallTool(ctx, &mcpsdk.CallToolParams{
		Name: "rotate_podcast_credentials",
		Arguments: map[string]any{
			"id":                "alice",
			"rotate_listener":   false,
			"rotate_submit_key": true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if rotKeyRes.IsError {
		t.Fatalf("rotate_podcast_credentials submit_key error: %+v", rotKeyRes)
	}
	var rotKeyShow CreatePodcastOutput
	if err := json.Unmarshal([]byte(rotKeyRes.Content[0].(*mcpsdk.TextContent).Text), &rotKeyShow); err != nil {
		t.Fatal(err)
	}
	if rotKeyShow.SubmitKey == "" || rotKeyShow.SubmitKey == createdShow.SubmitKey {
		t.Fatalf("expected rotated submit_key, got %s", rotKeyShow.SubmitKey)
	}
	if rotKeyShow.Password != rotShow.Password || rotKeyShow.Token != rotShow.Token {
		t.Fatalf("expected listener credentials preserved when rotate_listener=false")
	}

	// rotate_podcast_credentials error paths
	for _, args := range []map[string]any{
		{},
		{"id": "nonexistent"},
	} {
		errRes, err := session.CallTool(ctx, &mcpsdk.CallToolParams{
			Name:      "rotate_podcast_credentials",
			Arguments: args,
		})
		if err != nil {
			t.Fatal(err)
		}
		if !errRes.IsError {
			t.Fatalf("expected IsError for rotate_podcast_credentials args %v", args)
		}
	}

	// 8. update_podcast
	updShowRes, err := session.CallTool(ctx, &mcpsdk.CallToolParams{
		Name: "update_podcast",
		Arguments: map[string]any{
			"podcast_id":  "alice",
			"title":       "Alice Executive Daily",
			"description": "Updated show description",
			"author":      "Alice AI Agent",
			"image_url":   "https://example.com/alice-icon.png",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if updShowRes.IsError {
		t.Fatalf("update_podcast error: %+v", updShowRes)
	}
	var updShow CreatePodcastOutput
	if err := json.Unmarshal([]byte(updShowRes.Content[0].(*mcpsdk.TextContent).Text), &updShow); err != nil {
		t.Fatal(err)
	}
	if updShow.Title != "Alice Executive Daily" || updShow.Description != "Updated show description" || updShow.Author != "Alice AI Agent" || updShow.ImageURL != "https://example.com/alice-icon.png" {
		t.Fatalf("unexpected update_podcast output: %+v", updShow)
	}

	// update_podcast error paths
	for _, args := range []map[string]any{
		{},
		{"id": "nonexistent", "title": "No"},
		{"id": "alice", "image_url": "ftp://invalid"},
	} {
		errRes, err := session.CallTool(ctx, &mcpsdk.CallToolParams{
			Name:      "update_podcast",
			Arguments: args,
		})
		if err != nil {
			t.Fatal(err)
		}
		if !errRes.IsError {
			t.Fatalf("expected IsError for update_podcast args %v", args)
		}
	}

	// 9. update_episode
	updEpRes, err := session.CallTool(ctx, &mcpsdk.CallToolParams{
		Name: "update_episode",
		Arguments: map[string]any{
			"episode_id":  pubOut.EpisodeID,
			"title":       "Alice Briefing (Updated)",
			"description": "Updated show notes for Alice.",
			"category":    "Executive Briefing",
			"image_url":   "https://example.com/ep-icon.png",
			"chapters": []map[string]any{
				{"start_seconds": 0, "title": "Intro", "url": "https://example.com/intro", "image_url": "https://example.com/intro.png"},
				{"start_seconds": 45, "title": "Highlights"},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if updEpRes.IsError {
		t.Fatalf("update_episode error: %+v", updEpRes)
	}
	var updEp GetStatusOutput
	if err := json.Unmarshal([]byte(updEpRes.Content[0].(*mcpsdk.TextContent).Text), &updEp); err != nil {
		t.Fatal(err)
	}
	if updEp.Title != "Alice Briefing (Updated)" ||
		updEp.Description != "Updated show notes for Alice." ||
		updEp.Category != "Executive Briefing" ||
		updEp.ImageURL != "https://example.com/ep-icon.png" ||
		len(updEp.Chapters) != 2 ||
		updEp.Chapters[0].Title != "Intro" ||
		updEp.Chapters[1].StartSeconds != 45 {
		t.Fatalf("unexpected update_episode output: %+v", updEp)
	}

	// update_episode error paths
	for _, args := range []map[string]any{
		{},
		{"episode_id": "ep_nonexistent", "title": "No"},
		{"episode_id": pubOut.EpisodeID, "title": ""},
	} {
		errRes, err := session.CallTool(ctx, &mcpsdk.CallToolParams{
			Name:      "update_episode",
			Arguments: args,
		})
		if err != nil {
			t.Fatal(err)
		}
		if !errRes.IsError {
			t.Fatalf("expected IsError for update_episode args %v", args)
		}
	}
}

func TestMCPPrincipalScoping(t *testing.T) {
	ctx := context.Background()
	application, adminSession := testSession(t)
	srv := NewServer(application)

	// Admin creates alice and bob
	for _, id := range []string{"alice", "bob"} {
		res, err := adminSession.CallTool(ctx, &mcpsdk.CallToolParams{
			Name:      "create_podcast",
			Arguments: map[string]any{"id": id, "title": id + " show"},
		})
		if err != nil || res.IsError {
			t.Fatalf("create %s: err=%v res=%+v", id, err, res)
		}
	}

	// Admin publishes a default episode
	defPubRes, err := adminSession.CallTool(ctx, &mcpsdk.CallToolParams{
		Name: "publish_agent_update",
		Arguments: map[string]any{
			"title":   "Default Episode",
			"content": "Good morning. This is a default show briefing.",
		},
	})
	if err != nil || defPubRes.IsError {
		t.Fatalf("publish default: err=%v res=%+v", err, defPubRes)
	}
	var defPub PublishOutput
	_ = json.Unmarshal([]byte(defPubRes.Content[0].(*mcpsdk.TextContent).Text), &defPub)

	// 1. RolePodcastSubmitter scoped to "alice"
	aliceSession := connectSessionWithContext(t, srv, auth.WithPrincipal(ctx, auth.Principal{
		Role:      auth.RolePodcastSubmitter,
		PodcastID: "alice",
	}))

	// Cannot create or list podcasts
	if res, err := aliceSession.CallTool(ctx, &mcpsdk.CallToolParams{
		Name:      "create_podcast",
		Arguments: map[string]any{"id": "carol", "title": "Carol"},
	}); err != nil || !res.IsError {
		t.Fatalf("expected IsError when alice creates podcast, got %+v", res)
	}
	if res, err := aliceSession.CallTool(ctx, &mcpsdk.CallToolParams{
		Name:      "list_podcasts",
		Arguments: map[string]any{},
	}); err != nil || !res.IsError {
		t.Fatalf("expected IsError when alice lists podcasts, got %+v", res)
	}

	// Can get_podcast and rotate_podcast_credentials for alice, but not bob
	if res, err := aliceSession.CallTool(ctx, &mcpsdk.CallToolParams{
		Name:      "get_podcast",
		Arguments: map[string]any{"id": "alice"},
	}); err != nil || res.IsError {
		t.Fatalf("expected alice to get_podcast alice, got err=%v res=%+v", err, res)
	}
	if res, err := aliceSession.CallTool(ctx, &mcpsdk.CallToolParams{
		Name:      "get_podcast",
		Arguments: map[string]any{"id": "bob"},
	}); err != nil || !res.IsError {
		t.Fatalf("expected IsError when alice gets bob, got %+v", res)
	}
	if res, err := aliceSession.CallTool(ctx, &mcpsdk.CallToolParams{
		Name:      "rotate_podcast_credentials",
		Arguments: map[string]any{"id": "alice"},
	}); err != nil || res.IsError {
		t.Fatalf("expected alice to rotate alice credentials, got err=%v res=%+v", err, res)
	}
	if res, err := aliceSession.CallTool(ctx, &mcpsdk.CallToolParams{
		Name:      "rotate_podcast_credentials",
		Arguments: map[string]any{"id": "bob"},
	}); err != nil || !res.IsError {
		t.Fatalf("expected IsError when alice rotates bob credentials, got %+v", res)
	}
	if res, err := aliceSession.CallTool(ctx, &mcpsdk.CallToolParams{
		Name:      "update_podcast",
		Arguments: map[string]any{"id": "alice", "title": "Alice Updated"},
	}); err != nil || res.IsError {
		t.Fatalf("expected alice to update_podcast alice, got err=%v res=%+v", err, res)
	}
	if res, err := aliceSession.CallTool(ctx, &mcpsdk.CallToolParams{
		Name:      "update_podcast",
		Arguments: map[string]any{"id": "bob", "title": "Bob Updated"},
	}); err != nil || !res.IsError {
		t.Fatalf("expected IsError when alice updates bob, got %+v", res)
	}

	// publish_agent_update auto-scopes empty podcast_id to "alice", and rejects "bob"
	alicePubRes, err := aliceSession.CallTool(ctx, &mcpsdk.CallToolParams{
		Name: "publish_agent_update",
		Arguments: map[string]any{
			"title":   "Alice Update",
			"content": "Good morning Alice. Auto-scoped update via MCP.",
		},
	})
	if err != nil || alicePubRes.IsError {
		t.Fatalf("alice publish: err=%v res=%+v", err, alicePubRes)
	}
	var alicePub PublishOutput
	_ = json.Unmarshal([]byte(alicePubRes.Content[0].(*mcpsdk.TextContent).Text), &alicePub)
	if alicePub.PodcastID != "alice" {
		t.Fatalf("expected auto-scoped podcast_id=alice, got %q", alicePub.PodcastID)
	}
	if res, err := aliceSession.CallTool(ctx, &mcpsdk.CallToolParams{
		Name: "publish_agent_update",
		Arguments: map[string]any{
			"title":      "Bob Update",
			"content":    "Good morning Bob. Should be rejected.",
			"podcast_id": "bob",
		},
	}); err != nil || !res.IsError {
		t.Fatalf("expected IsError when alice publishes to bob, got %+v", res)
	}

	// get_episode_status and list_episodes scoping for alice
	if res, err := aliceSession.CallTool(ctx, &mcpsdk.CallToolParams{
		Name:      "get_episode_status",
		Arguments: map[string]any{"episode_id": alicePub.EpisodeID},
	}); err != nil || res.IsError {
		t.Fatalf("expected alice to get own episode status, got err=%v res=%+v", err, res)
	}
	if res, err := aliceSession.CallTool(ctx, &mcpsdk.CallToolParams{
		Name:      "get_episode_status",
		Arguments: map[string]any{"episode_id": defPub.EpisodeID},
	}); err != nil || !res.IsError {
		t.Fatalf("expected IsError when alice gets default episode status, got %+v", res)
	}
	if res, err := aliceSession.CallTool(ctx, &mcpsdk.CallToolParams{
		Name:      "update_episode",
		Arguments: map[string]any{"episode_id": alicePub.EpisodeID, "description": "Alice notes"},
	}); err != nil || res.IsError {
		t.Fatalf("expected alice to update own episode, got err=%v res=%+v", err, res)
	}
	if res, err := aliceSession.CallTool(ctx, &mcpsdk.CallToolParams{
		Name:      "update_episode",
		Arguments: map[string]any{"episode_id": defPub.EpisodeID, "description": "No"},
	}); err != nil || !res.IsError {
		t.Fatalf("expected IsError when alice updates default episode, got %+v", res)
	}
	aliceListRes, err := aliceSession.CallTool(ctx, &mcpsdk.CallToolParams{
		Name:      "list_episodes",
		Arguments: map[string]any{},
	})
	if err != nil || aliceListRes.IsError {
		t.Fatalf("alice list_episodes: err=%v res=%+v", err, aliceListRes)
	}
	var aliceList ListEpisodesOutput
	_ = json.Unmarshal([]byte(aliceListRes.Content[0].(*mcpsdk.TextContent).Text), &aliceList)
	if len(aliceList.Episodes) != 1 || aliceList.Episodes[0].EpisodeID != alicePub.EpisodeID {
		t.Fatalf("expected only alice's episode in list_episodes, got %+v", aliceList.Episodes)
	}
	if res, err := aliceSession.CallTool(ctx, &mcpsdk.CallToolParams{
		Name:      "list_episodes",
		Arguments: map[string]any{"only_default": true},
	}); err != nil || !res.IsError {
		t.Fatalf("expected IsError when alice lists only_default, got %+v", res)
	}
	if res, err := aliceSession.CallTool(ctx, &mcpsdk.CallToolParams{
		Name:      "list_episodes",
		Arguments: map[string]any{"podcast_id": "bob"},
	}); err != nil || !res.IsError {
		t.Fatalf("expected IsError when alice lists bob episodes, got %+v", res)
	}

	// 2. RoleDefaultSubmitter
	defSession := connectSessionWithContext(t, srv, auth.WithPrincipal(ctx, auth.Principal{
		Role: auth.RoleDefaultSubmitter,
	}))
	if res, err := defSession.CallTool(ctx, &mcpsdk.CallToolParams{
		Name:      "create_podcast",
		Arguments: map[string]any{"id": "dave", "title": "Dave"},
	}); err != nil || !res.IsError {
		t.Fatalf("expected IsError when default submitter creates podcast, got %+v", res)
	}
	if res, err := defSession.CallTool(ctx, &mcpsdk.CallToolParams{
		Name: "publish_agent_update",
		Arguments: map[string]any{
			"title":      "Alice Update",
			"content":    "Good morning Alice. Should be rejected.",
			"podcast_id": "alice",
		},
	}); err != nil || !res.IsError {
		t.Fatalf("expected IsError when default submitter publishes to alice, got %+v", res)
	}
	if res, err := defSession.CallTool(ctx, &mcpsdk.CallToolParams{
		Name:      "get_episode_status",
		Arguments: map[string]any{"episode_id": alicePub.EpisodeID},
	}); err != nil || !res.IsError {
		t.Fatalf("expected IsError when default submitter gets alice episode, got %+v", res)
	}
	if res, err := defSession.CallTool(ctx, &mcpsdk.CallToolParams{
		Name:      "update_episode",
		Arguments: map[string]any{"episode_id": alicePub.EpisodeID, "title": "No"},
	}); err != nil || !res.IsError {
		t.Fatalf("expected IsError when default submitter updates alice episode, got %+v", res)
	}
	if res, err := defSession.CallTool(ctx, &mcpsdk.CallToolParams{
		Name:      "update_episode",
		Arguments: map[string]any{"episode_id": defPub.EpisodeID, "description": "Default show notes"},
	}); err != nil || res.IsError {
		t.Fatalf("expected default submitter to update default episode, got err=%v res=%+v", err, res)
	}
	if res, err := defSession.CallTool(ctx, &mcpsdk.CallToolParams{
		Name:      "list_episodes",
		Arguments: map[string]any{"podcast_id": "alice"},
	}); err != nil || !res.IsError {
		t.Fatalf("expected IsError when default submitter lists alice episodes, got %+v", res)
	}
	defListRes, err := defSession.CallTool(ctx, &mcpsdk.CallToolParams{
		Name:      "list_episodes",
		Arguments: map[string]any{},
	})
	if err != nil || defListRes.IsError {
		t.Fatalf("default submitter list_episodes: err=%v res=%+v", err, defListRes)
	}
	var defList ListEpisodesOutput
	_ = json.Unmarshal([]byte(defListRes.Content[0].(*mcpsdk.TextContent).Text), &defList)
	if len(defList.Episodes) != 1 || defList.Episodes[0].EpisodeID != defPub.EpisodeID {
		t.Fatalf("expected only default episode in list_episodes, got %+v", defList.Episodes)
	}
}
