package podcast

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/tvaroska/podcaster/internal/cover"
	"github.com/tvaroska/podcaster/internal/episode"
)

var slugRe = regexp.MustCompile(`^[a-z][a-z0-9-]{1,31}$`)

var reservedIDs = map[string]struct{}{
	"v1": {}, "api": {}, "mcp": {}, "healthz": {}, "readyz": {},
	"podcast": {}, "feed": {}, "audio": {}, "cover": {}, "p": {},
	"admin": {}, "static": {}, "default": {},
}

func validateImageURL(raw string) error {
	if !utf8.ValidString(raw) {
		return &episode.ValidationError{Field: "image_url", Message: "image_url must be valid UTF-8"}
	}
	s := strings.TrimSpace(raw)
	if s == "" {
		return nil
	}
	if cover.IsInlineDataURI(s) {
		if _, _, err := cover.DecodeInlineDataURI(s); err != nil {
			return &episode.ValidationError{Field: "image_url", Message: err.Error()}
		}
		return nil
	}
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return &episode.ValidationError{Field: "image_url", Message: "image_url must be a valid http:// or https:// URL"}
	}
	return nil
}

// ValidateCreate checks a create-podcast payload.
func ValidateCreate(in CreateInput) error {
	id := strings.ToLower(strings.TrimSpace(in.ID))
	title := strings.TrimSpace(in.Title)
	if id == "" {
		return &episode.ValidationError{Field: "id", Message: "id is required"}
	}
	if !slugRe.MatchString(id) {
		return &episode.ValidationError{Field: "id", Message: "id must be 2–32 chars, start with a letter, and use only lowercase letters, digits, and hyphens"}
	}
	if strings.Contains(id, "--") || strings.HasSuffix(id, "-") {
		return &episode.ValidationError{Field: "id", Message: "id must not end with a hyphen or contain consecutive hyphens"}
	}
	if _, ok := reservedIDs[id]; ok {
		return &episode.ValidationError{Field: "id", Message: "id is reserved"}
	}
	if title == "" {
		return &episode.ValidationError{Field: "title", Message: "title is required"}
	}
	if utf8.RuneCountInString(title) > episode.DefaultMaxTitleLength {
		return &episode.ValidationError{Field: "title", Message: fmt.Sprintf("title must be at most %d characters", episode.DefaultMaxTitleLength)}
	}
	if !utf8.ValidString(title) {
		return &episode.ValidationError{Field: "title", Message: "title must be valid UTF-8"}
	}
	if !utf8.ValidString(in.Description) {
		return &episode.ValidationError{Field: "description", Message: "description must be valid UTF-8"}
	}
	if !utf8.ValidString(in.Author) {
		return &episode.ValidationError{Field: "author", Message: "author must be valid UTF-8"}
	}
	if in.ImageURL != "" {
		if err := validateImageURL(in.ImageURL); err != nil {
			return err
		}
	}
	return nil
}

// ValidateUpdate checks a podcast metadata update payload.
func ValidateUpdate(in UpdateInput) error {
	if in.Title != nil {
		title := strings.TrimSpace(*in.Title)
		if title == "" {
			return &episode.ValidationError{Field: "title", Message: "title is required"}
		}
		if utf8.RuneCountInString(title) > episode.DefaultMaxTitleLength {
			return &episode.ValidationError{Field: "title", Message: fmt.Sprintf("title must be at most %d characters", episode.DefaultMaxTitleLength)}
		}
		if !utf8.ValidString(title) {
			return &episode.ValidationError{Field: "title", Message: "title must be valid UTF-8"}
		}
	}
	if in.Description != nil {
		if !utf8.ValidString(*in.Description) {
			return &episode.ValidationError{Field: "description", Message: "description must be valid UTF-8"}
		}
	}
	if in.Author != nil {
		if !utf8.ValidString(*in.Author) {
			return &episode.ValidationError{Field: "author", Message: "author must be valid UTF-8"}
		}
	}
	if in.ImageURL != nil {
		if err := validateImageURL(*in.ImageURL); err != nil {
			return err
		}
	}
	return nil
}
