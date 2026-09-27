package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/tvaroska/podcaster/internal/app"
	"github.com/tvaroska/podcaster/internal/auth"
	"github.com/tvaroska/podcaster/internal/config"
	"github.com/tvaroska/podcaster/internal/episode"
	"github.com/tvaroska/podcaster/internal/podcast"
	"github.com/tvaroska/podcaster/internal/rss"
	"github.com/tvaroska/podcaster/internal/storage"
	"github.com/tvaroska/podcaster/internal/store"
)

const maxBody = 1 << 20 // 1 MiB JSON body

type Handler struct {
	App   *app.App
	Cfg   *config.Config
	Cover []byte
	Log   *slog.Logger
	MCP   http.Handler
}

func (h *Handler) logger() *slog.Logger {
	if h.Log != nil {
		return h.Log
	}
	return slog.Default()
}

func (h *Handler) feed() auth.FeedCreds {
	return auth.FeedCreds{
		Username: h.Cfg.FeedUsername,
		Password: h.Cfg.FeedPassword,
		Token:    h.Cfg.FeedToken,
	}
}

func (h *Handler) Router() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", h.healthz)
	mux.HandleFunc("GET /readyz", h.readyz)
	mux.HandleFunc("POST /v1/episodes", h.requireAgent(h.createEpisode))
	mux.HandleFunc("GET /v1/episodes", h.requireAgent(h.listEpisodes))
	mux.HandleFunc("GET /v1/episodes/{id}", h.requireAgent(h.getEpisode))
	mux.HandleFunc("POST /v1/podcasts", h.requireAgent(h.createPodcast))
	mux.HandleFunc("GET /v1/podcasts", h.requireAgent(h.listPodcasts))
	mux.HandleFunc("GET /v1/podcasts/{id}", h.requireAgent(h.getPodcast))
	mux.HandleFunc("POST /v1/podcasts/{id}/episodes", h.requireAgent(h.createPodcastEpisode))
	mux.HandleFunc("GET /podcast.xml", h.requireFeed(h.podcastXML))
	mux.HandleFunc("HEAD /podcast.xml", h.requireFeed(h.podcastXML))
	mux.HandleFunc("GET /feed.xml", h.requireFeed(h.podcastXML))
	mux.HandleFunc("HEAD /feed.xml", h.requireFeed(h.podcastXML))
	mux.HandleFunc("GET /audio/{file}", h.requireFeed(h.audio))
	mux.HandleFunc("HEAD /audio/{file}", h.requireFeed(h.audio))
	mux.HandleFunc("GET /cover.png", h.cover)
	mux.HandleFunc("HEAD /cover.png", h.cover)
	mux.HandleFunc("GET /p/{id}/podcast.xml", h.requirePodcastFeed(h.podcastXML))
	mux.HandleFunc("HEAD /p/{id}/podcast.xml", h.requirePodcastFeed(h.podcastXML))
	mux.HandleFunc("GET /p/{id}/feed.xml", h.requirePodcastFeed(h.podcastXML))
	mux.HandleFunc("HEAD /p/{id}/feed.xml", h.requirePodcastFeed(h.podcastXML))
	mux.HandleFunc("GET /p/{id}/audio/{file}", h.requirePodcastFeed(h.audio))
	mux.HandleFunc("HEAD /p/{id}/audio/{file}", h.requirePodcastFeed(h.audio))
	mux.HandleFunc("GET /p/{id}/cover.png", h.cover)
	mux.HandleFunc("HEAD /p/{id}/cover.png", h.cover)
	if h.MCP != nil {
		mcp := h.requireAgentHandler(h.MCP)
		mux.Handle("GET /mcp", mcp)
		mux.Handle("POST /mcp", mcp)
		mux.Handle("DELETE /mcp", mcp)
	}
	return logging(h.logger(), mux)
}

func (h *Handler) healthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) readyz(w http.ResponseWriter, r *http.Request) {
	if err := h.App.Store.Ping(r.Context()); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "unready", "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

func (h *Handler) createEpisode(w http.ResponseWriter, r *http.Request) {
	var in episode.CreateInput
	if err := decodeJSON(r, &in); err != nil {
		writeJSON(w, http.StatusBadRequest, errorBody("invalid json: "+err.Error()))
		return
	}
	h.enqueueEpisode(w, r, in)
}

func (h *Handler) createPodcastEpisode(w http.ResponseWriter, r *http.Request) {
	var in episode.CreateInput
	if err := decodeJSON(r, &in); err != nil {
		writeJSON(w, http.StatusBadRequest, errorBody("invalid json: "+err.Error()))
		return
	}
	in.PodcastID = r.PathValue("id")
	h.enqueueEpisode(w, r, in)
}

func (h *Handler) enqueueEpisode(w http.ResponseWriter, r *http.Request, in episode.CreateInput) {
	ep, err := h.App.CreateEpisode(r.Context(), in)
	if err != nil {
		var ve *episode.ValidationError
		if errors.As(err, &ve) {
			writeJSON(w, http.StatusUnprocessableEntity, errorBody(ve.Error()))
			return
		}
		h.logger().Error("create episode", "err", err)
		writeJSON(w, http.StatusInternalServerError, errorBody("failed to enqueue episode"))
		return
	}
	body := map[string]any{
		"episode_id": ep.ID,
		"status":     ep.PublicStatus(),
		"created_at": ep.CreatedAt.UTC().Format(time.RFC3339),
	}
	if ep.PodcastID != "" {
		body["podcast_id"] = ep.PodcastID
	}
	writeJSON(w, http.StatusAccepted, body)
}

func (h *Handler) createPodcast(w http.ResponseWriter, r *http.Request) {
	var in podcast.CreateInput
	if err := decodeJSON(r, &in); err != nil {
		writeJSON(w, http.StatusBadRequest, errorBody("invalid json: "+err.Error()))
		return
	}
	p, err := h.App.CreatePodcast(r.Context(), in)
	if err != nil {
		var ve *episode.ValidationError
		if errors.As(err, &ve) {
			writeJSON(w, http.StatusUnprocessableEntity, errorBody(ve.Error()))
			return
		}
		if errors.Is(err, store.ErrAlreadyExists) {
			writeJSON(w, http.StatusConflict, errorBody("podcast already exists"))
			return
		}
		h.logger().Error("create podcast", "err", err)
		writeJSON(w, http.StatusInternalServerError, errorBody("failed to create podcast"))
		return
	}
	writeJSON(w, http.StatusCreated, h.podcastJSON(p))
}

func (h *Handler) listPodcasts(w http.ResponseWriter, r *http.Request) {
	list, err := h.App.Store.ListPodcasts(r.Context())
	if err != nil {
		h.logger().Error("list podcasts", "err", err)
		writeJSON(w, http.StatusInternalServerError, errorBody("failed to list podcasts"))
		return
	}
	out := make([]map[string]any, 0, len(list))
	for _, p := range list {
		out = append(out, h.podcastJSON(p))
	}
	writeJSON(w, http.StatusOK, map[string]any{"podcasts": out})
}

func (h *Handler) getPodcast(w http.ResponseWriter, r *http.Request) {
	p, err := h.App.Store.GetPodcast(r.Context(), r.PathValue("id"))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeJSON(w, http.StatusNotFound, errorBody("podcast not found"))
			return
		}
		writeJSON(w, http.StatusInternalServerError, errorBody("failed to load podcast"))
		return
	}
	writeJSON(w, http.StatusOK, h.podcastJSON(p))
}

func (h *Handler) podcastJSON(p *podcast.Podcast) map[string]any {
	feed := h.Cfg.PublicBaseURL + p.FeedPath()
	subscribe := feed
	if u, err := url.Parse(h.Cfg.PublicBaseURL); err == nil {
		u.User = url.UserPassword(p.Username, p.Password)
		u.Path = p.FeedPath()
		subscribe = u.String()
	}
	return map[string]any{
		"id":            p.ID,
		"title":         p.Title,
		"description":   p.Description,
		"author":        p.Author,
		"username":      p.Username,
		"password":      p.Password,
		"token":         p.Token,
		"feed_url":      feed,
		"subscribe_url": subscribe,
		"created_at":    p.CreatedAt.UTC().Format(time.RFC3339),
	}
}

func (h *Handler) listEpisodes(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	offset, _ := strconv.Atoi(q.Get("offset"))
	filter := episode.ListFilter{
		Status:    episode.Status(strings.ToUpper(q.Get("status"))),
		PodcastID: strings.ToLower(strings.TrimSpace(q.Get("podcast_id"))),
		Limit:     limit,
		Offset:    offset,
	}
	list, err := h.App.Store.List(r.Context(), filter)
	if err != nil {
		h.logger().Error("list episodes", "err", err)
		writeJSON(w, http.StatusInternalServerError, errorBody("failed to list episodes"))
		return
	}
	type item struct {
		EpisodeID       string  `json:"episode_id"`
		PodcastID       string  `json:"podcast_id,omitempty"`
		Title           string  `json:"title"`
		Status          string  `json:"status"`
		Category        string  `json:"category,omitempty"`
		DurationSeconds float64 `json:"duration_seconds,omitempty"`
		CreatedAt       string  `json:"created_at"`
	}
	out := make([]item, 0, len(list))
	for _, ep := range list {
		out = append(out, item{
			EpisodeID:       ep.ID,
			PodcastID:       ep.PodcastID,
			Title:           ep.Title,
			Status:          string(ep.PublicStatus()),
			Category:        ep.Category,
			DurationSeconds: ep.DurationSeconds,
			CreatedAt:       ep.CreatedAt.UTC().Format(time.RFC3339),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"episodes": out})
}

func (h *Handler) getEpisode(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ep, err := h.App.Store.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeJSON(w, http.StatusNotFound, errorBody("episode not found"))
			return
		}
		writeJSON(w, http.StatusInternalServerError, errorBody("failed to load episode"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"episode_id":       ep.ID,
		"podcast_id":       ep.PodcastID,
		"title":            ep.Title,
		"status":           ep.PublicStatus(),
		"category":         ep.Category,
		"voice_id":         ep.VoiceID,
		"duration_seconds": ep.DurationSeconds,
		"file_size_bytes":  ep.FileSizeBytes,
		"error_message":    ep.ErrorMessage,
		"created_at":       ep.CreatedAt.UTC().Format(time.RFC3339),
		"published_at":     formatTime(ep.PublishedAt),
	})
}

func (h *Handler) podcastXML(w http.ResponseWriter, r *http.Request) {
	filter := episode.ListFilter{Status: episode.StatusReady, Limit: 100}
	ch := rss.Channel{
		Title:       h.Cfg.PodcastTitle,
		Link:        h.Cfg.PublicBaseURL,
		Description: h.Cfg.PodcastDescription,
		Language:    h.Cfg.PodcastLanguage,
		Author:      h.Cfg.PodcastAuthor,
		OwnerEmail:  h.Cfg.PodcastOwnerEmail,
		Category:    h.Cfg.PodcastCategory,
		Explicit:    h.Cfg.PodcastExplicit,
		ImageURL:    h.Cfg.CoverURL(),
		FeedURL:     h.Cfg.PublicBaseURL + "/podcast.xml",
	}
	opt := rss.ItemOptions{
		BaseURL: h.Cfg.PublicBaseURL,
		Token:   h.feed().ExpectedToken(),
	}
	if show := podcastFrom(r); show != nil {
		filter.PodcastID = show.ID
		ch.Title = show.Title
		ch.Description = show.Description
		if show.Author != "" {
			ch.Author = show.Author
		}
		ch.Link = h.Cfg.PublicBaseURL + "/p/" + show.ID
		ch.FeedURL = h.Cfg.PublicBaseURL + show.FeedPath()
		ch.ImageURL = h.Cfg.PublicBaseURL + show.CoverPath()
		opt.Token = show.Token
		opt.AudioPath = show.AudioPath()
	} else {
		filter.OnlyDefault = true
	}
	list, err := h.App.Store.List(r.Context(), filter)
	if err != nil {
		h.logger().Error("feed list", "err", err)
		http.Error(w, "feed unavailable", http.StatusInternalServerError)
		return
	}
	body, err := rss.Build(ch, list, opt)
	if err != nil {
		http.Error(w, "feed render failed", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/rss+xml; charset=utf-8")
	w.Header().Set("Cache-Control", "private, max-age=60")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

func (h *Handler) audio(w http.ResponseWriter, r *http.Request) {
	file := r.PathValue("file")
	id, ok := parseAudioFile(file)
	if !ok {
		http.NotFound(w, r)
		return
	}
	ep, err := h.App.Store.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "storage error", http.StatusInternalServerError)
		return
	}
	if ep.Status != episode.StatusReady || ep.AudioURI == "" {
		http.NotFound(w, r)
		return
	}
	if show := podcastFrom(r); show != nil {
		if ep.PodcastID != show.ID {
			http.NotFound(w, r)
			return
		}
	} else if ep.PodcastID != "" {
		http.NotFound(w, r)
		return
	}

	// If storage backend supports signed URLs, redirect client directly to object storage (e.g. GCS).
	if signer, ok := h.App.Storage.(storage.URLSigner); ok {
		signedURL, err := signer.SignedURL(r.Context(), ep.AudioURI, storage.SignedURLOptions{
			Expiry: 30 * time.Minute,
			Method: http.MethodGet,
		})
		if err == nil && signedURL != "" {
			http.Redirect(w, r, signedURL, http.StatusTemporaryRedirect)
			return
		}
		if err != nil {
			h.logger().Debug("storage URL signing unavailable, falling back to proxy stream", "err", err)
		}
	}

	obj, err := h.App.Storage.Open(r.Context(), ep.AudioURI)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		h.logger().Error("open audio", "err", err, "key", ep.AudioURI)
		http.Error(w, "storage error", http.StatusInternalServerError)
		return
	}
	defer obj.Close()
	meta := obj.Stat()
	ct := ep.ContentType
	if ct == "" {
		ct = meta.ContentType
	}
	if ct == "" {
		ct = "audio/mpeg"
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Accept-Ranges", "bytes")
	mod := meta.LastModified
	if mod.IsZero() && ep.PublishedAt != nil {
		mod = *ep.PublishedAt
	}
	http.ServeContent(w, r, file, mod, obj)
}

func (h *Handler) cover(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	http.ServeContent(w, r, "cover.png", time.Unix(0, 0).UTC(), bytes.NewReader(h.Cover))
}

func parseAudioFile(file string) (id string, ok bool) {
	file = strings.TrimSpace(file)
	if file == "" || strings.Contains(file, "/") || strings.Contains(file, "..") {
		return "", false
	}
	switch {
	case strings.HasSuffix(file, ".mp3"):
		return strings.TrimSuffix(file, ".mp3"), true
	case strings.HasSuffix(file, ".wav"):
		return strings.TrimSuffix(file, ".wav"), true
	case strings.HasSuffix(file, ".aac"):
		return strings.TrimSuffix(file, ".aac"), true
	default:
		return "", false
	}
}

func (h *Handler) requireAgent(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth.APIKeyMatch(auth.Bearer(r), h.Cfg.AgentAPIKey) {
			writeJSON(w, http.StatusUnauthorized, errorBody("invalid or missing bearer token"))
			return
		}
		next(w, r)
	}
}

func (h *Handler) requireAgentHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !auth.APIKeyMatch(auth.Bearer(r), h.Cfg.AgentAPIKey) {
			writeJSON(w, http.StatusUnauthorized, errorBody("invalid or missing bearer token"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (h *Handler) requireFeed(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !h.feed().CheckFeed(r) {
			auth.UnauthorizedFeed(w)
			return
		}
		next(w, r)
	}
}

type ctxKey int

const podcastCtxKey ctxKey = 1

func withPodcast(r *http.Request, p *podcast.Podcast) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), podcastCtxKey, p))
}

func podcastFrom(r *http.Request) *podcast.Podcast {
	p, _ := r.Context().Value(podcastCtxKey).(*podcast.Podcast)
	return p
}

func (h *Handler) requirePodcastFeed(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := strings.ToLower(strings.TrimSpace(r.PathValue("id")))
		p, err := h.App.Store.GetPodcast(r.Context(), id)
		if err != nil {
			auth.UnauthorizedFeed(w)
			return
		}
		creds := auth.FeedCreds{Username: p.Username, Password: p.Password, Token: p.Token}
		if !creds.CheckFeed(r) {
			auth.UnauthorizedFeed(w)
			return
		}
		next(w, withPodcast(r, p))
	}
}

func decodeJSON(r *http.Request, dst any) error {
	defer r.Body.Close()
	dec := json.NewDecoder(io.LimitReader(r.Body, maxBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func errorBody(msg string) map[string]string {
	return map[string]string{"error": msg}
}

func formatTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UTC().Format(time.RFC3339)
}

func logging(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rw := &statusWriter{ResponseWriter: w, status: 200}
		next.ServeHTTP(rw, r)
		log.Info("http",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rw.status,
			"bytes", rw.bytes,
			"duration_ms", time.Since(start).Milliseconds(),
			"remote", r.RemoteAddr,
		)
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(p []byte) (int, error) {
	n, err := w.ResponseWriter.Write(p)
	w.bytes += n
	return n, err
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
