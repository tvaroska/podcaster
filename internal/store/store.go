package store

import (
	"context"
	"errors"

	"github.com/tvaroska/podcaster/internal/episode"
)

var (
	ErrNotFound      = errors.New("episode not found")
	ErrAlreadyExists = errors.New("episode already exists")
	ErrConflict      = errors.New("episode status conflict")
)

// Store persists episode metadata.
type Store interface {
	Create(ctx context.Context, ep *episode.Episode) error
	Get(ctx context.Context, id string) (*episode.Episode, error)
	List(ctx context.Context, f episode.ListFilter) ([]*episode.Episode, error)
	Update(ctx context.Context, ep *episode.Episode) error
	CompareAndSwapStatus(ctx context.Context, id string, from, to episode.Status) (*episode.Episode, error)
	Delete(ctx context.Context, id string) error
	Ping(ctx context.Context) error
	Close() error
}
