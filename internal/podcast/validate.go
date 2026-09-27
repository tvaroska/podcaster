package podcast

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/tvaroska/podcaster/internal/episode"
)

var slugRe = regexp.MustCompile(`^[a-z][a-z0-9-]{1,31}$`)

var reservedIDs = map[string]struct{}{
	"v1": {}, "api": {}, "mcp": {}, "healthz": {}, "readyz": {},
	"podcast": {}, "feed": {}, "audio": {}, "cover": {}, "p": {},
	"admin": {}, "static": {}, "default": {},
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
	if !utf8.ValidString(title) || !utf8.ValidString(in.Description) || !utf8.ValidString(in.Author) {
		return &episode.ValidationError{Field: "title", Message: "fields must be valid UTF-8"}
	}
	return nil
}
