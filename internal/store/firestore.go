package store

import (
	"context"
	"fmt"
	"time"

	"cloud.google.com/go/firestore"
	"google.golang.org/api/iterator"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/tvaroska/podcaster/internal/episode"
)

const firestoreCollection = "episodes"

type Firestore struct {
	client *firestore.Client
}

func OpenFirestore(ctx context.Context, project string) (*Firestore, error) {
	client, err := firestore.NewClient(ctx, project)
	if err != nil {
		return nil, fmt.Errorf("firestore client: %w", err)
	}
	return &Firestore{client: client}, nil
}

type fsDoc struct {
	ID              string     `firestore:"id"`
	Title           string     `firestore:"title"`
	ScriptText      string     `firestore:"script_text"`
	Category        string     `firestore:"category"`
	VoiceID         string     `firestore:"voice_id"`
	Status          string     `firestore:"status"`
	AudioURI        string     `firestore:"audio_gcs_uri"`
	DurationSeconds float64    `firestore:"duration_seconds"`
	FileSizeBytes   int64      `firestore:"file_size_bytes"`
	ContentType     string     `firestore:"content_type"`
	ErrorMessage    string     `firestore:"error_message"`
	CreatedAt       time.Time  `firestore:"created_at"`
	PublishedAt     *time.Time `firestore:"published_at"`
}

func toDoc(ep *episode.Episode) fsDoc {
	return fsDoc{
		ID:              ep.ID,
		Title:           ep.Title,
		ScriptText:      ep.ScriptText,
		Category:        ep.Category,
		VoiceID:         ep.VoiceID,
		Status:          string(ep.Status),
		AudioURI:        ep.AudioURI,
		DurationSeconds: ep.DurationSeconds,
		FileSizeBytes:   ep.FileSizeBytes,
		ContentType:     ep.ContentType,
		ErrorMessage:    ep.ErrorMessage,
		CreatedAt:       ep.CreatedAt.UTC(),
		PublishedAt:     ep.PublishedAt,
	}
}

func fromDoc(d fsDoc) *episode.Episode {
	return &episode.Episode{
		ID:              d.ID,
		Title:           d.Title,
		ScriptText:      d.ScriptText,
		Category:        d.Category,
		VoiceID:         d.VoiceID,
		Status:          episode.Status(d.Status),
		AudioURI:        d.AudioURI,
		DurationSeconds: d.DurationSeconds,
		FileSizeBytes:   d.FileSizeBytes,
		ContentType:     d.ContentType,
		ErrorMessage:    d.ErrorMessage,
		CreatedAt:       d.CreatedAt.UTC(),
		PublishedAt:     d.PublishedAt,
	}
}

func (s *Firestore) col() *firestore.CollectionRef {
	return s.client.Collection(firestoreCollection)
}

func (s *Firestore) Create(ctx context.Context, ep *episode.Episode) error {
	_, err := s.col().Doc(ep.ID).Create(ctx, toDoc(ep))
	if status.Code(err) == codes.AlreadyExists {
		return ErrAlreadyExists
	}
	return err
}

func (s *Firestore) Get(ctx context.Context, id string) (*episode.Episode, error) {
	snap, err := s.col().Doc(id).Get(ctx)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return nil, ErrNotFound
		}
		return nil, err
	}
	var d fsDoc
	if err := snap.DataTo(&d); err != nil {
		return nil, err
	}
	return fromDoc(d), nil
}

func (s *Firestore) List(ctx context.Context, f episode.ListFilter) ([]*episode.Episode, error) {
	limit := f.Limit
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	q := s.col().Query
	if f.Status != "" {
		status := f.Status
		if status == episode.StatusQueued {
			status = episode.StatusPending
		}
		q = q.Where("status", "==", string(status))
	}
	q = q.OrderBy("created_at", firestore.Desc).Limit(limit)
	if f.Offset > 0 {
		q = q.Offset(f.Offset)
	}
	iter := q.Documents(ctx)
	defer iter.Stop()
	var out []*episode.Episode
	for {
		snap, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, err
		}
		var d fsDoc
		if err := snap.DataTo(&d); err != nil {
			return nil, err
		}
		out = append(out, fromDoc(d))
	}
	return out, nil
}

func (s *Firestore) Update(ctx context.Context, ep *episode.Episode) error {
	_, err := s.col().Doc(ep.ID).Set(ctx, toDoc(ep))
	return err
}

func (s *Firestore) CompareAndSwapStatus(ctx context.Context, id string, from, to episode.Status) (*episode.Episode, error) {
	ref := s.col().Doc(id)
	var out *episode.Episode
	err := s.client.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		snap, err := tx.Get(ref)
		if err != nil {
			if status.Code(err) == codes.NotFound {
				return ErrNotFound
			}
			return err
		}
		var d fsDoc
		if err := snap.DataTo(&d); err != nil {
			return err
		}
		if episode.Status(d.Status) != from {
			out = fromDoc(d)
			return ErrConflict
		}
		d.Status = string(to)
		out = fromDoc(d)
		return tx.Set(ref, d)
	})
	return out, err
}

func (s *Firestore) Delete(ctx context.Context, id string) error {
	_, err := s.col().Doc(id).Delete(ctx)
	return err
}

func (s *Firestore) Ping(ctx context.Context) error {
	_, err := s.col().Limit(1).Documents(ctx).GetAll()
	return err
}

func (s *Firestore) Close() error {
	return s.client.Close()
}
