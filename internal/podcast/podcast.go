package podcast

import (
	"crypto/rand"
	"encoding/hex"
	"time"
)

// Podcast is a private show with its own RSS feed and listener credentials.
type Podcast struct {
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	Description string    `json:"description,omitempty"`
	Author      string    `json:"author,omitempty"`
	ImageURL    string    `json:"image_url,omitempty"`
	Username    string    `json:"username"`
	Password    string    `json:"password"`
	Token       string    `json:"token"`
	SubmitKey   string    `json:"submit_key,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

// CreateInput is the agent-facing payload used to create a show.
type CreateInput struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	Author      string `json:"author,omitempty"`
	ImageURL    string `json:"image_url,omitempty"`
	Password    string `json:"password,omitempty"`
	Token       string `json:"token,omitempty"`
}

// UpdateInput is the agent-facing payload used to update show metadata.
type UpdateInput struct {
	Title       *string `json:"title,omitempty"`
	Description *string `json:"description,omitempty"`
	Author      *string `json:"author,omitempty"`
	ImageURL    *string `json:"image_url,omitempty"`
}

// RotateOptions controls which credentials to rotate or update on a podcast.
type RotateOptions struct {
	RotateListener  bool   `json:"rotate_listener"`
	RotateSubmitKey bool   `json:"rotate_submit_key"`
	Password        string `json:"password,omitempty"`
	Token           string `json:"token,omitempty"`
}

// NewSecrets returns a random Basic password and enclosure token.
func NewSecrets() (password, token string, err error) {
	var buf [32]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", "", err
	}
	return hex.EncodeToString(buf[:16]), hex.EncodeToString(buf[16:]), nil
}

// NewSubmitKey returns a random per-podcast submit key.
func NewSubmitKey() (string, error) {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf[:]), nil
}

// FeedPath is the RSS path for this show.
func (p *Podcast) FeedPath() string {
	return "/p/" + p.ID + "/podcast.xml"
}

// AudioPath is the enclosure directory for this show.
func (p *Podcast) AudioPath() string {
	return "/p/" + p.ID + "/audio"
}

// CoverPath is the artwork path for this show.
func (p *Podcast) CoverPath() string {
	return "/p/" + p.ID + "/cover.png"
}
