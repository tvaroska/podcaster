package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/tvaroska/podcaster/internal/app"
	"github.com/tvaroska/podcaster/internal/episode"
	"github.com/tvaroska/podcaster/internal/store"
)

const Version = "0.1.0"

type publishInput struct {
	Title    string `json:"title" jsonschema:"Title of the podcast episode"`
	Content  string `json:"content" jsonschema:"Plain text script or summary to convert to audio"`
	Category string `json:"category,omitempty" jsonschema:"Update category, e.g. Daily Briefing or Urgent Alert"`
	VoiceID  string `json:"voice_id,omitempty" jsonschema:"Optional Piper voice identifier such as en_US-lessac-medium"`
}

type publishOutput struct {
	Status    string `json:"status" jsonschema:"QUEUED on success"`
	EpisodeID string `json:"episode_id" jsonschema:"Assigned episode identifier"`
	Message   string `json:"message" jsonschema:"Human-readable confirmation"`
}

type statusInput struct {
	EpisodeID string `json:"episode_id" jsonschema:"Episode identifier returned by publish_agent_update"`
}

type statusOutput struct {
	EpisodeID       string  `json:"episode_id"`
	Title           string  `json:"title"`
	Status          string  `json:"status"`
	DurationSeconds float64 `json:"duration_seconds,omitempty"`
	ErrorMessage    string  `json:"error_message,omitempty"`
	CreatedAt       string  `json:"created_at"`
	PublishedAt     string  `json:"published_at,omitempty"`
}

// NewServer builds an MCP server exposing podcast tools.
func NewServer(a *app.App) *mcpsdk.Server {
	s := mcpsdk.NewServer(&mcpsdk.Implementation{
		Name:    "podcaster",
		Version: Version,
		Title:   "Private Podcast Platform",
	}, nil)

	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name:        "publish_agent_update",
		Description: "Queue a new private podcast episode. The text is synthesized to audio asynchronously and appears in the authenticated RSS feed once READY.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in publishInput) (*mcpsdk.CallToolResult, publishOutput, error) {
		ep, err := a.CreateEpisode(ctx, episode.CreateInput{
			Title:    in.Title,
			Content:  in.Content,
			Category: in.Category,
			VoiceID:  in.VoiceID,
		})
		if err != nil {
			var ve *episode.ValidationError
			if errors.As(err, &ve) {
				return &mcpsdk.CallToolResult{
					IsError: true,
					Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: ve.Error()}},
				}, publishOutput{}, nil
			}
			return nil, publishOutput{}, err
		}
		out := publishOutput{
			Status:    string(ep.PublicStatus()),
			EpisodeID: ep.ID,
			Message:   "Batch job initialized. Episode will appear in the feed once synthesis completes.",
		}
		body, _ := json.Marshal(out)
		return &mcpsdk.CallToolResult{
			Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: string(body)}},
		}, out, nil
	})

	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name:        "get_episode_status",
		Description: "Look up the processing status of a previously queued episode.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in statusInput) (*mcpsdk.CallToolResult, statusOutput, error) {
		if in.EpisodeID == "" {
			return &mcpsdk.CallToolResult{
				IsError: true,
				Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: "episode_id is required"}},
			}, statusOutput{}, nil
		}
		ep, err := a.Store.Get(ctx, in.EpisodeID)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return &mcpsdk.CallToolResult{
					IsError: true,
					Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: "episode not found"}},
				}, statusOutput{}, nil
			}
			return nil, statusOutput{}, err
		}
		out := statusOutput{
			EpisodeID:       ep.ID,
			Title:           ep.Title,
			Status:          string(ep.PublicStatus()),
			DurationSeconds: ep.DurationSeconds,
			ErrorMessage:    ep.ErrorMessage,
			CreatedAt:       ep.CreatedAt.UTC().Format(time.RFC3339),
		}
		if ep.PublishedAt != nil {
			out.PublishedAt = ep.PublishedAt.UTC().Format(time.RFC3339)
		}
		body, _ := json.Marshal(out)
		return &mcpsdk.CallToolResult{
			Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: string(body)}},
		}, out, nil
	})

	return s
}

// HTTPHandler serves MCP over Streamable HTTP at a single endpoint.
func HTTPHandler(s *mcpsdk.Server) http.Handler {
	return mcpsdk.NewStreamableHTTPHandler(func(*http.Request) *mcpsdk.Server {
		return s
	}, nil)
}
