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

// Episode is a single podcast item: source script plus synthesized audio metadata.
type Episode struct {
	ID              string     `json:"episode_id"`
	PodcastID       string     `json:"podcast_id,omitempty"`
	Title           string     `json:"title"`
	ScriptText      string     `json:"script_text,omitempty"`
	Category        string     `json:"category,omitempty"`
	VoiceID         string     `json:"voice_id,omitempty"`
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
	Title     string `json:"title"`
	Content   string `json:"content"`
	Category  string `json:"category,omitempty"`
	VoiceID   string `json:"voice_id,omitempty"`
	PodcastID string `json:"podcast_id,omitempty"`
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

// AudioObjectKey is the canonical object-storage path for an episode enclosure.
func AudioObjectKey(id, ext string) string {
	if ext == "" {
		ext = "mp3"
	}
	return "audio/" + id + "." + ext
}
