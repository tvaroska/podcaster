package episode

import (
	"errors"
	"fmt"
	"math"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/tvaroska/podcaster/internal/cover"
)

const (
	DefaultMinContentLength = 10
	DefaultMaxContentLength = 100_000
	DefaultMaxTitleLength   = 200
)

// Limits holds payload validation bounds. Zero values fall back to defaults.
type Limits struct {
	MinContentLength int
	MaxContentLength int
	MaxTitleLength   int
}

func (l Limits) withDefaults() Limits {
	if l.MinContentLength <= 0 {
		l.MinContentLength = DefaultMinContentLength
	}
	if l.MaxContentLength <= 0 {
		l.MaxContentLength = DefaultMaxContentLength
	}
	if l.MaxTitleLength <= 0 {
		l.MaxTitleLength = DefaultMaxTitleLength
	}
	return l
}

var (
	ErrInvalidTitle   = errors.New("invalid title")
	ErrInvalidContent = errors.New("invalid content")
	ErrInvalidVoice   = errors.New("invalid voice_id")
)

// ValidationError is a field-level input error suitable for 422 responses.
type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string {
	if e.Field == "" {
		return e.Message
	}
	return e.Field + ": " + e.Message
}

func (e *ValidationError) Unwrap() error {
	switch e.Field {
	case "title":
		return ErrInvalidTitle
	case "content":
		return ErrInvalidContent
	case "voice_id":
		return ErrInvalidVoice
	default:
		return nil
	}
}

// ValidateCreate checks a create payload. voiceAllow is an optional allow-list;
// an empty allow-list means any non-empty voice_id (or none) is accepted.
func ValidateCreate(in CreateInput, limits Limits, voiceAllow []string) error {
	limits = limits.withDefaults()
	in.Title = strings.TrimSpace(in.Title)
	in.Content = strings.TrimSpace(in.Content)
	in.Category = strings.TrimSpace(in.Category)
	in.VoiceID = strings.TrimSpace(in.VoiceID)

	if in.Title == "" {
		return &ValidationError{Field: "title", Message: "title is required"}
	}
	if utf8.RuneCountInString(in.Title) > limits.MaxTitleLength {
		return &ValidationError{Field: "title", Message: fmt.Sprintf("title must be at most %d characters", limits.MaxTitleLength)}
	}
	if !utf8.ValidString(in.Title) {
		return &ValidationError{Field: "title", Message: "title must be valid UTF-8"}
	}

	n := utf8.RuneCountInString(in.Content)
	if n < limits.MinContentLength {
		return &ValidationError{Field: "content", Message: fmt.Sprintf("content must be at least %d characters", limits.MinContentLength)}
	}
	if n > limits.MaxContentLength {
		return &ValidationError{Field: "content", Message: fmt.Sprintf("content must be at most %d characters", limits.MaxContentLength)}
	}
	if !utf8.ValidString(in.Content) {
		return &ValidationError{Field: "content", Message: "content must be valid UTF-8"}
	}

	if !utf8.ValidString(in.Category) {
		return &ValidationError{Field: "category", Message: "category must be valid UTF-8"}
	}
	if utf8.RuneCountInString(in.Category) > limits.MaxTitleLength {
		return &ValidationError{Field: "category", Message: fmt.Sprintf("category must be at most %d characters", limits.MaxTitleLength)}
	}

	if !utf8.ValidString(in.VoiceID) {
		return &ValidationError{Field: "voice_id", Message: "voice_id must be valid UTF-8"}
	}
	if utf8.RuneCountInString(in.VoiceID) > limits.MaxTitleLength {
		return &ValidationError{Field: "voice_id", Message: fmt.Sprintf("voice_id must be at most %d characters", limits.MaxTitleLength)}
	}
	if in.VoiceID != "" {
		if strings.Contains(in.VoiceID, "..") || strings.ContainsAny(in.VoiceID, `/\`) {
			return &ValidationError{Field: "voice_id", Message: "voice_id must not contain path separators or traversal"}
		}
		if len(voiceAllow) > 0 && !isVoiceAllowed(in.VoiceID, voiceAllow) {
			return &ValidationError{Field: "voice_id", Message: "voice_id is not in the configured allow-list"}
		}
	}

	if err := validateDescription(in.Description, limits); err != nil {
		return err
	}
	if in.ImageURL != "" {
		if err := validateHTTPURL("image_url", in.ImageURL); err != nil {
			return err
		}
	}
	if err := validateChapters(in.Chapters, limits); err != nil {
		return err
	}
	return nil
}

// ValidateUpdate checks an episode metadata update payload.
func ValidateUpdate(in UpdateInput, limits Limits) error {
	limits = limits.withDefaults()
	if in.Title != nil {
		title := strings.TrimSpace(*in.Title)
		if title == "" {
			return &ValidationError{Field: "title", Message: "title is required"}
		}
		if utf8.RuneCountInString(title) > limits.MaxTitleLength {
			return &ValidationError{Field: "title", Message: fmt.Sprintf("title must be at most %d characters", limits.MaxTitleLength)}
		}
		if !utf8.ValidString(title) {
			return &ValidationError{Field: "title", Message: "title must be valid UTF-8"}
		}
	}
	if in.Description != nil {
		if err := validateDescription(*in.Description, limits); err != nil {
			return err
		}
	}
	if in.Category != nil {
		cat := strings.TrimSpace(*in.Category)
		if !utf8.ValidString(cat) {
			return &ValidationError{Field: "category", Message: "category must be valid UTF-8"}
		}
		if utf8.RuneCountInString(cat) > limits.MaxTitleLength {
			return &ValidationError{Field: "category", Message: fmt.Sprintf("category must be at most %d characters", limits.MaxTitleLength)}
		}
	}
	if in.ImageURL != nil {
		if !utf8.ValidString(*in.ImageURL) {
			return &ValidationError{Field: "image_url", Message: "image_url must be valid UTF-8"}
		}
		if strings.TrimSpace(*in.ImageURL) != "" {
			if err := validateHTTPURL("image_url", *in.ImageURL); err != nil {
				return err
			}
		}
	}
	if in.Chapters != nil {
		if err := validateChapters(*in.Chapters, limits); err != nil {
			return err
		}
	}
	return nil
}

func validateDescription(desc string, limits Limits) error {
	if !utf8.ValidString(desc) {
		return &ValidationError{Field: "description", Message: "description must be valid UTF-8"}
	}
	if utf8.RuneCountInString(desc) > limits.MaxContentLength {
		return &ValidationError{Field: "description", Message: fmt.Sprintf("description must be at most %d characters", limits.MaxContentLength)}
	}
	return nil
}

func validateHTTPURL(field, raw string) error {
	if !utf8.ValidString(raw) {
		return &ValidationError{Field: field, Message: field + " must be valid UTF-8"}
	}
	s := strings.TrimSpace(raw)
	if field == "image_url" && cover.IsInlineDataURI(s) {
		if _, _, err := cover.DecodeInlineDataURI(s); err != nil {
			return &ValidationError{Field: "image_url", Message: err.Error()}
		}
		return nil
	}
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return &ValidationError{Field: field, Message: field + " must be a valid http:// or https:// URL"}
	}
	return nil
}

func validateChapters(chapters []Chapter, limits Limits) error {
	for _, ch := range chapters {
		if math.IsNaN(ch.StartSeconds) || math.IsInf(ch.StartSeconds, 0) || ch.StartSeconds < 0 {
			return &ValidationError{Field: "chapters", Message: "chapter start_seconds must be >= 0"}
		}
		title := strings.TrimSpace(ch.Title)
		if title == "" {
			return &ValidationError{Field: "chapters", Message: "chapter title is required"}
		}
		if !utf8.ValidString(title) {
			return &ValidationError{Field: "chapters", Message: "chapter title must be valid UTF-8"}
		}
		if utf8.RuneCountInString(title) > limits.MaxTitleLength {
			return &ValidationError{Field: "chapters", Message: fmt.Sprintf("chapter title must be at most %d characters", limits.MaxTitleLength)}
		}
		if !utf8.ValidString(ch.URL) {
			return &ValidationError{Field: "chapters", Message: "chapter url must be valid UTF-8"}
		}
		if strings.TrimSpace(ch.URL) != "" {
			if err := validateHTTPURL("chapters", ch.URL); err != nil {
				return &ValidationError{Field: "chapters", Message: "chapter url must be a valid http:// or https:// URL"}
			}
		}
		if !utf8.ValidString(ch.ImageURL) {
			return &ValidationError{Field: "chapters", Message: "chapter image_url must be valid UTF-8"}
		}
		if strings.TrimSpace(ch.ImageURL) != "" {
			if err := validateHTTPURL("chapters", ch.ImageURL); err != nil {
				return &ValidationError{Field: "chapters", Message: "chapter image_url must be a valid http:// or https:// URL"}
			}
		}
	}
	return nil
}

// isVoiceAllowed checks whether voiceID matches an entry in voiceAllow directly,
// or is a valid blend expression (e.g. "af_heart:0.7,af_bella:0.3" or "0.7*af_heart+0.3*af_bella")
// whose constituent voices are all present in voiceAllow.
func isVoiceAllowed(voiceID string, voiceAllow []string) bool {
	allowed := make(map[string]bool, len(voiceAllow))
	for _, v := range voiceAllow {
		allowed[strings.TrimSpace(v)] = true
	}
	if allowed[voiceID] {
		return true
	}
	if !strings.ContainsAny(voiceID, "+,:*") {
		return false
	}
	normalized := strings.ReplaceAll(voiceID, "+", ",")
	parts := strings.Split(normalized, ",")
	count := 0
	for _, rawPart := range parts {
		part := strings.TrimSpace(rawPart)
		if part == "" {
			return false
		}
		var name string
		switch {
		case strings.Contains(part, ":"):
			kv := strings.SplitN(part, ":", 2)
			name = strings.TrimSpace(kv[0])
			w, err := strconv.ParseFloat(strings.TrimSpace(kv[1]), 64)
			if err != nil || w <= 0 || math.IsNaN(w) || math.IsInf(w, 0) {
				return false
			}
		case strings.Contains(part, "*"):
			kv := strings.SplitN(part, "*", 2)
			left := strings.TrimSpace(kv[0])
			right := strings.TrimSpace(kv[1])
			if w, err := strconv.ParseFloat(left, 64); err == nil && w > 0 && !math.IsNaN(w) && !math.IsInf(w, 0) {
				name = right
			} else if w, err := strconv.ParseFloat(right, 64); err == nil && w > 0 && !math.IsNaN(w) && !math.IsInf(w, 0) {
				name = left
			} else {
				return false
			}
		default:
			name = part
		}
		if !allowed[name] {
			return false
		}
		count++
	}
	return count > 0
}
