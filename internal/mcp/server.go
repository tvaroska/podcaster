package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/tvaroska/podcaster/internal/app"
	"github.com/tvaroska/podcaster/internal/auth"
	"github.com/tvaroska/podcaster/internal/episode"
	"github.com/tvaroska/podcaster/internal/podcast"
	"github.com/tvaroska/podcaster/internal/store"
)

const Version = "0.1.0"

func principalOrAdmin(ctx context.Context) auth.Principal {
	if p, ok := auth.PrincipalFrom(ctx); ok && p.Role != "" {
		return p
	}
	return auth.Principal{Role: auth.RoleAdmin}
}

type publishInput struct {
	Title       string            `json:"title" jsonschema:"Title of the podcast episode"`
	Content     string            `json:"content" jsonschema:"Plain text or Markdown script to convert to audio"`
	Description string            `json:"description,omitempty" jsonschema:"Optional episode show notes or summary (distinct from the content script)"`
	Category    string            `json:"category,omitempty" jsonschema:"Update category, e.g. Daily Briefing or Urgent Alert"`
	VoiceID     string            `json:"voice_id,omitempty" jsonschema:"Optional voice identifier (e.g. af_heart, af_bella, am_adam, am_fenrir, am_michael, bf_emma, bm_george)"`
	ImageURL    string            `json:"image_url,omitempty" jsonschema:"Optional per-episode artwork (http://, https://, or data:image/(png|jpeg);base64,...)"`
	Chapters    []episode.Chapter `json:"chapters,omitempty" jsonschema:"Optional chapter markers"`
	PodcastID   string            `json:"podcast_id,omitempty" jsonschema:"Optional show id created with create_podcast. Empty publishes to the default feed."`
}

// PublishOutput is the output of the publish_agent_update tool.
type PublishOutput struct {
	Status    string `json:"status" jsonschema:"QUEUED on success"`
	EpisodeID string `json:"episode_id" jsonschema:"Assigned episode identifier"`
	PodcastID string `json:"podcast_id,omitempty" jsonschema:"Show identifier when published to a per-user show"`
	CreatedAt string `json:"created_at" jsonschema:"Creation timestamp in RFC3339 format"`
	Message   string `json:"message" jsonschema:"Human-readable confirmation"`
}

type publishOutput = PublishOutput

type statusInput struct {
	EpisodeID string `json:"episode_id" jsonschema:"Episode identifier returned by publish_agent_update"`
}

// GetStatusOutput is the output of the get_episode_status and update_episode tools and item shape for list_episodes.
type GetStatusOutput struct {
	EpisodeID       string            `json:"episode_id"`
	PodcastID       string            `json:"podcast_id,omitempty"`
	Title           string            `json:"title"`
	Description     string            `json:"description,omitempty"`
	Status          string            `json:"status"`
	Category        string            `json:"category,omitempty"`
	VoiceID         string            `json:"voice_id,omitempty"`
	ImageURL        string            `json:"image_url,omitempty"`
	Chapters        []episode.Chapter `json:"chapters,omitempty"`
	DurationSeconds float64           `json:"duration_seconds,omitempty"`
	FileSizeBytes   int64             `json:"file_size_bytes,omitempty"`
	ErrorMessage    string            `json:"error_message,omitempty"`
	CreatedAt       string            `json:"created_at"`
	PublishedAt     string            `json:"published_at,omitempty"`
}

type statusOutput = GetStatusOutput

type listEpisodesInput struct {
	Status      string `json:"status,omitempty" jsonschema:"Optional status filter: QUEUED, PROCESSING, READY, or FAILED"`
	PodcastID   string `json:"podcast_id,omitempty" jsonschema:"Optional show slug to filter episodes"`
	OnlyDefault bool   `json:"only_default,omitempty" jsonschema:"When true, only return episodes on the default feed"`
	Limit       int    `json:"limit,omitempty" jsonschema:"Maximum number of episodes to return (default 50, max 100)"`
	Offset      int    `json:"offset,omitempty" jsonschema:"Pagination offset"`
}

// ListEpisodesOutput is the output of the list_episodes tool.
type ListEpisodesOutput struct {
	Episodes []GetStatusOutput `json:"episodes"`
}

type createPodcastInput struct {
	ID          string `json:"id" jsonschema:"URL slug for the show, e.g. alice. Lowercase letters, digits, hyphens."`
	Title       string `json:"title" jsonschema:"Show title as it appears in podcast apps"`
	Description string `json:"description,omitempty" jsonschema:"Optional RSS description"`
	Author      string `json:"author,omitempty" jsonschema:"Optional itunes:author"`
	ImageURL    string `json:"image_url,omitempty" jsonschema:"Optional custom podcast cover icon (http://, https://, or data:image/(png|jpeg);base64,...)"`
	Password    string `json:"password,omitempty" jsonschema:"Optional custom listener Basic-auth password (generated if omitted)"`
	Token       string `json:"token,omitempty" jsonschema:"Optional custom listener enclosure/feed token (defaults to password if password is set, or generated if omitted)"`
}

// CreatePodcastOutput is the output of create_podcast, update_podcast, get_podcast, rotate_podcast_credentials, and items in list_podcasts.
type CreatePodcastOutput struct {
	ID           string `json:"id"`
	Title        string `json:"title"`
	Description  string `json:"description"`
	Author       string `json:"author"`
	ImageURL     string `json:"image_url,omitempty"`
	Username     string `json:"username"`
	Password     string `json:"password"`
	Token        string `json:"token"`
	SubmitKey    string `json:"submit_key,omitempty"`
	FeedURL      string `json:"feed_url"`
	SubscribeURL string `json:"subscribe_url"`
	CreatedAt    string `json:"created_at"`
	Message      string `json:"message,omitempty"`
}

type createPodcastOutput = CreatePodcastOutput

type listPodcastsInput struct{}

// ListPodcastsOutput is the output of the list_podcasts tool.
type ListPodcastsOutput struct {
	Podcasts []CreatePodcastOutput `json:"podcasts"`
}

type getPodcastInput struct {
	ID        string `json:"id,omitempty" jsonschema:"Podcast slug identifier, e.g. alice"`
	PodcastID string `json:"podcast_id,omitempty" jsonschema:"Alias for id: podcast slug identifier"`
}

type updatePodcastInput struct {
	PodcastID   string  `json:"podcast_id,omitempty" jsonschema:"Podcast slug identifier, e.g. alice"`
	ID          string  `json:"id,omitempty" jsonschema:"Alias for podcast_id: podcast slug identifier"`
	Title       *string `json:"title,omitempty" jsonschema:"Optional updated show title"`
	Description *string `json:"description,omitempty" jsonschema:"Optional updated RSS description (empty string clears)"`
	Author      *string `json:"author,omitempty" jsonschema:"Optional updated itunes:author (empty string clears)"`
	ImageURL    *string `json:"image_url,omitempty" jsonschema:"Optional updated custom podcast cover icon (http://, https://, data:image/(png|jpeg);base64,..., or empty string to clear)"`
}

type updateEpisodeInput struct {
	EpisodeID   string             `json:"episode_id,omitempty" jsonschema:"Episode identifier returned by publish_agent_update"`
	ID          string             `json:"id,omitempty" jsonschema:"Alias for episode_id"`
	Title       *string            `json:"title,omitempty" jsonschema:"Optional updated episode title"`
	Description *string            `json:"description,omitempty" jsonschema:"Optional updated episode show notes or summary"`
	Category    *string            `json:"category,omitempty" jsonschema:"Optional updated episode category"`
	ImageURL    *string            `json:"image_url,omitempty" jsonschema:"Optional updated per-episode artwork (http://, https://, data:image/(png|jpeg);base64,..., or empty string to clear)"`
	Chapters    *[]episode.Chapter `json:"chapters,omitempty" jsonschema:"Optional updated chapter markers (empty array clears)"`
}

type rotatePodcastInput struct {
	PodcastID       string `json:"podcast_id,omitempty" jsonschema:"Podcast slug identifier, e.g. alice"`
	ID              string `json:"id,omitempty" jsonschema:"Alias for podcast_id: podcast slug identifier"`
	RotateListener  *bool  `json:"rotate_listener,omitempty" jsonschema:"Rotate listener password and token (defaults to true when no other option is set)"`
	RotateSubmitKey bool   `json:"rotate_submit_key,omitempty" jsonschema:"Rotate the show's publisher submit_key"`
	Password        string `json:"password,omitempty" jsonschema:"Optional custom listener Basic-auth password"`
	Token           string `json:"token,omitempty" jsonschema:"Optional custom listener feed token"`
}

func toStatusOutput(ep *episode.Episode) GetStatusOutput {
	out := GetStatusOutput{
		EpisodeID:       ep.ID,
		PodcastID:       ep.PodcastID,
		Title:           ep.Title,
		Description:     ep.Description,
		Status:          string(ep.PublicStatus()),
		Category:        ep.Category,
		VoiceID:         ep.VoiceID,
		ImageURL:        ep.ImageURL,
		Chapters:        ep.Chapters,
		DurationSeconds: ep.DurationSeconds,
		FileSizeBytes:   ep.FileSizeBytes,
		ErrorMessage:    ep.ErrorMessage,
		CreatedAt:       ep.CreatedAt.UTC().Format(time.RFC3339),
	}
	if ep.PublishedAt != nil {
		out.PublishedAt = ep.PublishedAt.UTC().Format(time.RFC3339)
	}
	return out
}

func toPodcastOutput(baseURL string, p *podcast.Podcast) CreatePodcastOutput {
	feed := baseURL + p.FeedPath()
	subscribe := feed
	if u, err := url.Parse(feed); err == nil {
		u.User = url.UserPassword(p.Username, p.Password)
		subscribe = u.String()
	}
	return CreatePodcastOutput{
		ID:           p.ID,
		Title:        p.Title,
		Description:  p.Description,
		Author:       p.Author,
		ImageURL:     p.ImageURL,
		Username:     p.Username,
		Password:     p.Password,
		Token:        p.Token,
		SubmitKey:    p.SubmitKey,
		FeedURL:      feed,
		SubscribeURL: subscribe,
		CreatedAt:    p.CreatedAt.UTC().Format(time.RFC3339),
	}
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
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in publishInput) (*mcpsdk.CallToolResult, PublishOutput, error) {
		p := principalOrAdmin(ctx)
		podcastID := strings.ToLower(strings.TrimSpace(in.PodcastID))
		switch p.Role {
		case auth.RoleDefaultSubmitter:
			if podcastID != "" {
				return &mcpsdk.CallToolResult{
					IsError: true,
					Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: "forbidden: default submit key cannot publish to a user podcast"}},
				}, PublishOutput{}, nil
			}
		case auth.RolePodcastSubmitter:
			if podcastID == "" {
				in.PodcastID = p.PodcastID
			} else if podcastID != p.PodcastID {
				return &mcpsdk.CallToolResult{
					IsError: true,
					Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: "forbidden: submit key is scoped to podcast " + p.PodcastID}},
				}, PublishOutput{}, nil
			}
		}
		ep, err := a.CreateEpisode(ctx, episode.CreateInput{
			Title:       in.Title,
			Description: in.Description,
			Content:     in.Content,
			Category:    in.Category,
			VoiceID:     in.VoiceID,
			ImageURL:    in.ImageURL,
			Chapters:    in.Chapters,
			PodcastID:   in.PodcastID,
		})
		if err != nil {
			var ve *episode.ValidationError
			if errors.As(err, &ve) {
				return &mcpsdk.CallToolResult{
					IsError: true,
					Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: ve.Error()}},
				}, PublishOutput{}, nil
			}
			return nil, PublishOutput{}, err
		}
		out := PublishOutput{
			Status:    string(ep.PublicStatus()),
			EpisodeID: ep.ID,
			PodcastID: ep.PodcastID,
			CreatedAt: ep.CreatedAt.UTC().Format(time.RFC3339),
			Message:   "Batch job initialized. Episode will appear in the feed once synthesis completes.",
		}
		body, _ := json.Marshal(out)
		return &mcpsdk.CallToolResult{
			Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: string(body)}},
		}, out, nil
	})

	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name:        "create_podcast",
		Description: "Create a private show for one listener. Returns unique Basic-auth credentials and a subscribe URL. Episodes published with this podcast_id appear only on that feed.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in createPodcastInput) (*mcpsdk.CallToolResult, CreatePodcastOutput, error) {
		if p := principalOrAdmin(ctx); p.Role != auth.RoleAdmin {
			return &mcpsdk.CallToolResult{
				IsError: true,
				Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: "forbidden: admin key required to create podcasts"}},
			}, CreatePodcastOutput{}, nil
		}
		p, err := a.CreatePodcast(ctx, podcast.CreateInput{
			ID: in.ID, Title: in.Title, Description: in.Description, Author: in.Author,
			ImageURL: in.ImageURL, Password: in.Password, Token: in.Token,
		})
		if err != nil {
			var ve *episode.ValidationError
			if errors.As(err, &ve) {
				return &mcpsdk.CallToolResult{
					IsError: true,
					Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: ve.Error()}},
				}, CreatePodcastOutput{}, nil
			}
			if errors.Is(err, store.ErrAlreadyExists) {
				return &mcpsdk.CallToolResult{
					IsError: true,
					Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: "podcast already exists"}},
				}, CreatePodcastOutput{}, nil
			}
			return nil, CreatePodcastOutput{}, err
		}
		out := toPodcastOutput(a.Cfg.PublicBaseURL, p)
		out.Message = "Show created. Subscribe with the username and password, then publish episodes with this podcast_id."
		body, _ := json.Marshal(out)
		return &mcpsdk.CallToolResult{
			Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: string(body)}},
		}, out, nil
	})

	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name:        "get_episode_status",
		Description: "Look up the processing status of a previously queued episode.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in statusInput) (*mcpsdk.CallToolResult, GetStatusOutput, error) {
		id := strings.TrimSpace(in.EpisodeID)
		if id == "" {
			return &mcpsdk.CallToolResult{
				IsError: true,
				Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: "episode_id is required"}},
			}, GetStatusOutput{}, nil
		}
		ep, err := a.Store.Get(ctx, id)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return &mcpsdk.CallToolResult{
					IsError: true,
					Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: "episode not found"}},
				}, GetStatusOutput{}, nil
			}
			return nil, GetStatusOutput{}, err
		}
		p := principalOrAdmin(ctx)
		if p.Role == auth.RoleDefaultSubmitter && ep.PodcastID != "" {
			return &mcpsdk.CallToolResult{
				IsError: true,
				Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: "forbidden: default submit key cannot access user podcast episode"}},
			}, GetStatusOutput{}, nil
		}
		if p.Role == auth.RolePodcastSubmitter && ep.PodcastID != p.PodcastID {
			return &mcpsdk.CallToolResult{
				IsError: true,
				Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: "forbidden: submit key is scoped to podcast " + p.PodcastID}},
			}, GetStatusOutput{}, nil
		}
		out := toStatusOutput(ep)
		body, _ := json.Marshal(out)
		return &mcpsdk.CallToolResult{
			Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: string(body)}},
		}, out, nil
	})

	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name:        "list_episodes",
		Description: "List recent podcast episodes (newest first), optionally filtered by status or podcast_id.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in listEpisodesInput) (*mcpsdk.CallToolResult, ListEpisodesOutput, error) {
		limit := in.Limit
		if limit > 100 {
			limit = 100
		}
		filter := episode.ListFilter{
			Status:      episode.Status(strings.ToUpper(strings.TrimSpace(in.Status))),
			PodcastID:   strings.ToLower(strings.TrimSpace(in.PodcastID)),
			OnlyDefault: in.OnlyDefault,
			Limit:       limit,
			Offset:      in.Offset,
		}
		p := principalOrAdmin(ctx)
		switch p.Role {
		case auth.RoleDefaultSubmitter:
			if filter.PodcastID != "" {
				return &mcpsdk.CallToolResult{
					IsError: true,
					Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: "forbidden: default submit key cannot list episodes for a user podcast"}},
				}, ListEpisodesOutput{}, nil
			}
			filter.OnlyDefault = true
		case auth.RolePodcastSubmitter:
			if filter.OnlyDefault || (filter.PodcastID != "" && filter.PodcastID != p.PodcastID) {
				return &mcpsdk.CallToolResult{
					IsError: true,
					Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: "forbidden: submit key is scoped to podcast " + p.PodcastID}},
				}, ListEpisodesOutput{}, nil
			}
			filter.PodcastID = p.PodcastID
		}
		list, err := a.Store.List(ctx, filter)
		if err != nil {
			return nil, ListEpisodesOutput{}, err
		}
		items := make([]GetStatusOutput, 0, len(list))
		for _, ep := range list {
			items = append(items, toStatusOutput(ep))
		}
		out := ListEpisodesOutput{Episodes: items}
		body, _ := json.Marshal(out)
		return &mcpsdk.CallToolResult{
			Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: string(body)}},
		}, out, nil
	})

	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name:        "list_podcasts",
		Description: "List all private podcast shows and their feed URLs and credentials.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, _ listPodcastsInput) (*mcpsdk.CallToolResult, ListPodcastsOutput, error) {
		if p := principalOrAdmin(ctx); p.Role != auth.RoleAdmin {
			return &mcpsdk.CallToolResult{
				IsError: true,
				Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: "forbidden: admin key required to list podcasts"}},
			}, ListPodcastsOutput{}, nil
		}
		list, err := a.Store.ListPodcasts(ctx)
		if err != nil {
			return nil, ListPodcastsOutput{}, err
		}
		items := make([]CreatePodcastOutput, 0, len(list))
		for _, p := range list {
			items = append(items, toPodcastOutput(a.Cfg.PublicBaseURL, p))
		}
		out := ListPodcastsOutput{Podcasts: items}
		body, _ := json.Marshal(out)
		return &mcpsdk.CallToolResult{
			Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: string(body)}},
		}, out, nil
	})

	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name:        "get_podcast",
		Description: "Retrieve metadata, feed URL, subscribe URL, and credentials for an existing private podcast show.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in getPodcastInput) (*mcpsdk.CallToolResult, CreatePodcastOutput, error) {
		id := strings.ToLower(strings.TrimSpace(in.PodcastID))
		if id == "" {
			id = strings.ToLower(strings.TrimSpace(in.ID))
		}
		if id == "" {
			return &mcpsdk.CallToolResult{
				IsError: true,
				Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: "podcast_id is required"}},
			}, CreatePodcastOutput{}, nil
		}
		pr := principalOrAdmin(ctx)
		if pr.Role != auth.RoleAdmin && !(pr.Role == auth.RolePodcastSubmitter && pr.PodcastID == id) {
			return &mcpsdk.CallToolResult{
				IsError: true,
				Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: "forbidden: not authorized to view this podcast"}},
			}, CreatePodcastOutput{}, nil
		}
		p, err := a.Store.GetPodcast(ctx, id)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return &mcpsdk.CallToolResult{
					IsError: true,
					Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: "podcast not found"}},
				}, CreatePodcastOutput{}, nil
			}
			return nil, CreatePodcastOutput{}, err
		}
		out := toPodcastOutput(a.Cfg.PublicBaseURL, p)
		body, _ := json.Marshal(out)
		return &mcpsdk.CallToolResult{
			Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: string(body)}},
		}, out, nil
	})

	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name:        "rotate_podcast_credentials",
		Description: "Rotate or update listener credentials (password and token) and/or publisher submit_key for an existing private podcast show.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in rotatePodcastInput) (*mcpsdk.CallToolResult, CreatePodcastOutput, error) {
		id := strings.ToLower(strings.TrimSpace(in.PodcastID))
		if id == "" {
			id = strings.ToLower(strings.TrimSpace(in.ID))
		}
		if id == "" {
			return &mcpsdk.CallToolResult{
				IsError: true,
				Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: "podcast_id is required"}},
			}, CreatePodcastOutput{}, nil
		}
		pr := principalOrAdmin(ctx)
		if pr.Role != auth.RoleAdmin && !(pr.Role == auth.RolePodcastSubmitter && pr.PodcastID == id) {
			return &mcpsdk.CallToolResult{
				IsError: true,
				Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: "forbidden: not authorized to rotate credentials for this podcast"}},
			}, CreatePodcastOutput{}, nil
		}
		opts := podcast.RotateOptions{
			RotateSubmitKey: in.RotateSubmitKey,
			Password:        in.Password,
			Token:           in.Token,
		}
		if in.RotateListener != nil {
			opts.RotateListener = *in.RotateListener
		}
		p, err := a.RotatePodcastCredentials(ctx, id, opts)
		if err != nil {
			var ve *episode.ValidationError
			if errors.As(err, &ve) {
				return &mcpsdk.CallToolResult{
					IsError: true,
					Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: ve.Error()}},
				}, CreatePodcastOutput{}, nil
			}
			if errors.Is(err, store.ErrNotFound) {
				return &mcpsdk.CallToolResult{
					IsError: true,
					Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: "podcast not found"}},
				}, CreatePodcastOutput{}, nil
			}
			return nil, CreatePodcastOutput{}, err
		}
		out := toPodcastOutput(a.Cfg.PublicBaseURL, p)
		out.Message = "Podcast credentials rotated."
		body, _ := json.Marshal(out)
		return &mcpsdk.CallToolResult{
			Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: string(body)}},
		}, out, nil
	})

	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name:        "update_podcast",
		Description: "Update an existing private podcast show's metadata (title, description, author, and/or image_url).",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in updatePodcastInput) (*mcpsdk.CallToolResult, CreatePodcastOutput, error) {
		id := strings.ToLower(strings.TrimSpace(in.PodcastID))
		if id == "" {
			id = strings.ToLower(strings.TrimSpace(in.ID))
		}
		if id == "" {
			return &mcpsdk.CallToolResult{
				IsError: true,
				Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: "podcast_id is required"}},
			}, CreatePodcastOutput{}, nil
		}
		pr := principalOrAdmin(ctx)
		if pr.Role != auth.RoleAdmin && !(pr.Role == auth.RolePodcastSubmitter && pr.PodcastID == id) {
			return &mcpsdk.CallToolResult{
				IsError: true,
				Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: "forbidden: not authorized to update this podcast"}},
			}, CreatePodcastOutput{}, nil
		}
		p, err := a.UpdatePodcast(ctx, id, podcast.UpdateInput{
			Title:       in.Title,
			Description: in.Description,
			Author:      in.Author,
			ImageURL:    in.ImageURL,
		})
		if err != nil {
			var ve *episode.ValidationError
			if errors.As(err, &ve) {
				return &mcpsdk.CallToolResult{
					IsError: true,
					Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: ve.Error()}},
				}, CreatePodcastOutput{}, nil
			}
			if errors.Is(err, store.ErrNotFound) {
				return &mcpsdk.CallToolResult{
					IsError: true,
					Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: "podcast not found"}},
				}, CreatePodcastOutput{}, nil
			}
			return nil, CreatePodcastOutput{}, err
		}
		out := toPodcastOutput(a.Cfg.PublicBaseURL, p)
		out.Message = "Podcast metadata updated."
		body, _ := json.Marshal(out)
		return &mcpsdk.CallToolResult{
			Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: string(body)}},
		}, out, nil
	})

	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name:        "update_episode",
		Description: "Update an existing episode's metadata (title, description, category, image_url, and/or chapters) without re-running audio synthesis.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in updateEpisodeInput) (*mcpsdk.CallToolResult, GetStatusOutput, error) {
		id := strings.TrimSpace(in.EpisodeID)
		if id == "" {
			id = strings.TrimSpace(in.ID)
		}
		if id == "" {
			return &mcpsdk.CallToolResult{
				IsError: true,
				Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: "episode_id is required"}},
			}, GetStatusOutput{}, nil
		}
		ep, err := a.Store.Get(ctx, id)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return &mcpsdk.CallToolResult{
					IsError: true,
					Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: "episode not found"}},
				}, GetStatusOutput{}, nil
			}
			return nil, GetStatusOutput{}, err
		}
		pr := principalOrAdmin(ctx)
		if pr.Role == auth.RoleDefaultSubmitter && ep.PodcastID != "" {
			return &mcpsdk.CallToolResult{
				IsError: true,
				Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: "forbidden: default submit key cannot update user podcast episode"}},
			}, GetStatusOutput{}, nil
		}
		if pr.Role == auth.RolePodcastSubmitter && ep.PodcastID != pr.PodcastID {
			return &mcpsdk.CallToolResult{
				IsError: true,
				Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: "forbidden: submit key is scoped to podcast " + pr.PodcastID}},
			}, GetStatusOutput{}, nil
		}
		updated, err := a.UpdateEpisode(ctx, id, episode.UpdateInput{
			Title:       in.Title,
			Description: in.Description,
			Category:    in.Category,
			ImageURL:    in.ImageURL,
			Chapters:    in.Chapters,
		})
		if err != nil {
			var ve *episode.ValidationError
			if errors.As(err, &ve) {
				return &mcpsdk.CallToolResult{
					IsError: true,
					Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: ve.Error()}},
				}, GetStatusOutput{}, nil
			}
			if errors.Is(err, store.ErrNotFound) {
				return &mcpsdk.CallToolResult{
					IsError: true,
					Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: "episode not found"}},
				}, GetStatusOutput{}, nil
			}
			return nil, GetStatusOutput{}, err
		}
		out := toStatusOutput(updated)
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
