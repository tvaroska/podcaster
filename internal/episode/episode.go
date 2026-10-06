package episode

import "time"

// Status is the processing lifecycle of an episode.
type Status string

const (
	StatusPending    Status = "PENDING"
	StatusQueued     Status = "QUEUED" // alias presented to API clients for PENDING
	StatusProcessing Status = "PROCESSING"
	StatusReady      Status = "READY"
	StatusFailed     Status = "FAILED"
)

// Chapter is a single timestamped section marker in an episode.
type Chapter struct {
	StartSeconds float64 `json:"start_seconds" firestore:"start_seconds"`
	Title        string  `json:"title" firestore:"title"`
	URL          string  `json:"url,omitempty" firestore:"url,omitempty"`
	ImageURL     string  `json:"image_url,omitempty" firestore:"image_url,omitempty"`
}

// Episode is a single podcast item: source script plus synthesized audio metadata.
type Episode struct {
	ID              string     `json:"episode_id"`
	PodcastID       string     `json:"podcast_id,omitempty"`
	Title           string     `json:"title"`
	Description     string     `json:"description,omitempty"`
	ScriptText      string     `json:"script_text,omitempty"`
	Category        string     `json:"category,omitempty"`
	VoiceID         string     `json:"voice_id,omitempty"`
	ImageURL        string     `json:"image_url,omitempty"`
	Chapters        []Chapter  `json:"chapters,omitempty"`
	Status          Status     `json:"status"`
	AudioURI        string     `json:"audio_uri,omitempty"`
	DurationSeconds float64    `json:"duration_seconds,omitempty"`
	FileSizeBytes   int64      `json:"file_size_bytes,omitempty"`
	ContentType     string     `json:"content_type,omitempty"`
	ErrorMessage    string     `json:"error_message,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	PublishedAt     *time.Time `json:"published_at,omitempty"`
}

// CreateInput is the agent-facing payload used to enqueue a new episode.
type CreateInput struct {
	Title       string    `json:"title"`
	Description string    `json:"description,omitempty"`
	Content     string    `json:"content"`
	Category    string    `json:"category,omitempty"`
	VoiceID     string    `json:"voice_id,omitempty"`
	PodcastID   string    `json:"podcast_id,omitempty"`
	ImageURL    string    `json:"image_url,omitempty"`
	Chapters    []Chapter `json:"chapters,omitempty"`
}

// UpdateInput is the agent-facing payload used to update episode metadata.
type UpdateInput struct {
	Title       *string    `json:"title,omitempty"`
	Description *string    `json:"description,omitempty"`
	Category    *string    `json:"category,omitempty"`
	ImageURL    *string    `json:"image_url,omitempty"`
	Chapters    *[]Chapter `json:"chapters,omitempty"`
}

// ListFilter controls listing and pagination.
type ListFilter struct {
	Status      Status
	PodcastID   string
	OnlyDefault bool
	Limit       int
	Offset      int
}

// PublicStatus maps internal PENDING onto the documented QUEUED value.
func (e *Episode) PublicStatus() Status {
	if e.Status == StatusPending {
		return StatusQueued
	}
	return e.Status
}

// CoverPath is the artwork path for this episode.
func (e *Episode) CoverPath() string {
	if e.PodcastID != "" {
		return "/p/" + e.PodcastID + "/episodes/" + e.ID + "/cover.png"
	}
	return "/episodes/" + e.ID + "/cover.png"
}

// AudioObjectKey is the canonical object-storage path for an episode enclosure.
func AudioObjectKey(id, ext string) string {
	if ext == "" {
		ext = "mp3"
	}
	return "audio/" + id + "." + ext
}
