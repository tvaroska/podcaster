package episode

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
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

	if in.VoiceID != "" && len(voiceAllow) > 0 {
		ok := false
		for _, v := range voiceAllow {
			if v == in.VoiceID {
				ok = true
				break
			}
		}
		if !ok {
			return &ValidationError{Field: "voice_id", Message: "voice_id is not in the configured allow-list"}
		}
	}
	return nil
}
