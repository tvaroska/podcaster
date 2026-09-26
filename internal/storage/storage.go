package storage

import (
	"context"
	"errors"
	"io"
	"time"
)

var ErrNotFound = errors.New("object not found")

// ObjectMeta describes a stored audio (or image) object.
type ObjectMeta struct {
	Key          string
	Size         int64
	ContentType  string
	LastModified time.Time
}

// Object is a readable stored blob. Seek is optional; Range handling in the
// HTTP layer uses Size + ReadAt-style re-open when Seek is unavailable.
type Object interface {
	io.ReadCloser
	io.Seeker
	Stat() ObjectMeta
}

// Storage is the object store for synthesized audio enclosures.
type Storage interface {
	Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) error
	Open(ctx context.Context, key string) (Object, error)
	Delete(ctx context.Context, key string) error
	Stat(ctx context.Context, key string) (ObjectMeta, error)
}

// SignedURLOptions configures parameters for direct signed object URLs.
type SignedURLOptions struct {
	Expiry time.Duration
	Method string
}

// URLSigner is an optional interface implemented by storage backends that support
// generating direct, time-limited signed URLs (e.g. GCS Signed URLs).
type URLSigner interface {
	SignedURL(ctx context.Context, key string, opts SignedURLOptions) (string, error)
}
