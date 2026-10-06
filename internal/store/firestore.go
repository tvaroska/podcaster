package store

import (
	"context"
	"fmt"
	"strings"
	"time"

	"cloud.google.com/go/firestore"
	"google.golang.org/api/iterator"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/tvaroska/podcaster/internal/auth"
	"github.com/tvaroska/podcaster/internal/episode"
	"github.com/tvaroska/podcaster/internal/podcast"
)

const (
	firestoreCollection        = "episodes"
	firestorePodcastCollection = "podcasts"
)

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
	ID              string            `firestore:"id"`
	PodcastID       string            `firestore:"podcast_id"`
	Title           string            `firestore:"title"`
	Description     string            `firestore:"description"`
	ScriptText      string            `firestore:"script_text"`
	Category        string            `firestore:"category"`
	VoiceID         string            `firestore:"voice_id"`
	ImageURL        string            `firestore:"image_url"`
	Chapters        []episode.Chapter `firestore:"chapters,omitempty"`
	Status          string            `firestore:"status"`
	AudioURI        string            `firestore:"audio_gcs_uri"`
	DurationSeconds float64           `firestore:"duration_seconds"`
	FileSizeBytes   int64             `firestore:"file_size_bytes"`
	ContentType     string            `firestore:"content_type"`
	ErrorMessage    string            `firestore:"error_message"`
	CreatedAt       time.Time         `firestore:"created_at"`
	PublishedAt     *time.Time        `firestore:"published_at"`
}

func toDoc(ep *episode.Episode) fsDoc {
	var pub *time.Time
	if ep.PublishedAt != nil {
		t := ep.PublishedAt.UTC()
		pub = &t
	}
	var chapters []episode.Chapter
	if len(ep.Chapters) > 0 {
		chapters = append([]episode.Chapter(nil), ep.Chapters...)
	}
	return fsDoc{
		ID:              ep.ID,
		PodcastID:       ep.PodcastID,
		Title:           ep.Title,
		Description:     ep.Description,
		ScriptText:      ep.ScriptText,
		Category:        ep.Category,
		VoiceID:         ep.VoiceID,
		ImageURL:        ep.ImageURL,
		Chapters:        chapters,
		Status:          string(ep.Status),
		AudioURI:        ep.AudioURI,
		DurationSeconds: ep.DurationSeconds,
		FileSizeBytes:   ep.FileSizeBytes,
		ContentType:     ep.ContentType,
		ErrorMessage:    ep.ErrorMessage,
		CreatedAt:       ep.CreatedAt.UTC(),
		PublishedAt:     pub,
	}
}

func fromDoc(d fsDoc) *episode.Episode {
	var pub *time.Time
	if d.PublishedAt != nil {
		t := d.PublishedAt.UTC()
		pub = &t
	}
	var chapters []episode.Chapter
	if len(d.Chapters) > 0 {
		chapters = append([]episode.Chapter(nil), d.Chapters...)
	}
	return &episode.Episode{
		ID:              d.ID,
		PodcastID:       d.PodcastID,
		Title:           d.Title,
		Description:     d.Description,
		ScriptText:      d.ScriptText,
		Category:        d.Category,
		VoiceID:         d.VoiceID,
		ImageURL:        d.ImageURL,
		Chapters:        chapters,
		Status:          episode.Status(d.Status),
		AudioURI:        d.AudioURI,
		DurationSeconds: d.DurationSeconds,
		FileSizeBytes:   d.FileSizeBytes,
		ContentType:     d.ContentType,
		ErrorMessage:    d.ErrorMessage,
		CreatedAt:       d.CreatedAt.UTC(),
		PublishedAt:     pub,
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
	if limit <= 0 {
		limit = 50
	} else if limit > 100 {
		limit = 100
	}
	q := s.col().Query
	if f.OnlyDefault {
		q = q.Where("podcast_id", "==", "")
	} else if f.PodcastID != "" {
		q = q.Where("podcast_id", "==", f.PodcastID)
	}
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
		ep := fromDoc(d)
		if f.OnlyDefault && ep.PodcastID != "" {
			continue
		}
		out = append(out, ep)
	}
	return out, nil
}

func (s *Firestore) Update(ctx context.Context, ep *episode.Episode) error {
	ref := s.col().Doc(ep.ID)
	return s.client.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		snap, err := tx.Get(ref)
		if err != nil {
			if status.Code(err) == codes.NotFound {
				return ErrNotFound
			}
			return err
		}
		var existing fsDoc
		if err := snap.DataTo(&existing); err != nil {
			return err
		}
		doc := toDoc(ep)
		if !existing.CreatedAt.IsZero() {
			doc.CreatedAt = existing.CreatedAt.UTC()
		}
		return tx.Set(ref, doc)
	})
}

func (s *Firestore) CompareAndSwapStatus(ctx context.Context, id string, from, to episode.Status) (*episode.Episode, error) {
	ref := s.col().Doc(id)
	var out *episode.Episode
	err := s.client.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		out = nil
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
	_, err := s.col().Doc(id).Delete(ctx, firestore.Exists)
	if status.Code(err) == codes.NotFound {
		return ErrNotFound
	}
	return err
}

func (s *Firestore) Ping(ctx context.Context) error {
	_, err := s.col().Limit(1).Documents(ctx).GetAll()
	return err
}

type fsPodcastDoc struct {
	ID          string    `firestore:"id"`
	Title       string    `firestore:"title"`
	Description string    `firestore:"description"`
	Author      string    `firestore:"author"`
	ImageURL    string    `firestore:"image_url"`
	Username    string    `firestore:"username"`
	Password    string    `firestore:"password"`
	Token       string    `firestore:"token"`
	SubmitKey   string    `firestore:"submit_key"`
	CreatedAt   time.Time `firestore:"created_at"`
}

func toPodcastDoc(p *podcast.Podcast) fsPodcastDoc {
	return fsPodcastDoc{
		ID: p.ID, Title: p.Title, Description: p.Description, Author: p.Author,
		ImageURL: p.ImageURL, Username: p.Username, Password: p.Password, Token: p.Token,
		SubmitKey: p.SubmitKey, CreatedAt: p.CreatedAt.UTC(),
	}
}

func (s *Firestore) podcasts() *firestore.CollectionRef {
	return s.client.Collection(firestorePodcastCollection)
}

func (s *Firestore) CreatePodcast(ctx context.Context, p *podcast.Podcast) error {
	_, err := s.podcasts().Doc(p.ID).Create(ctx, toPodcastDoc(p))
	if status.Code(err) == codes.AlreadyExists {
		return ErrAlreadyExists
	}
	return err
}

func (s *Firestore) GetPodcast(ctx context.Context, id string) (*podcast.Podcast, error) {
	snap, err := s.podcasts().Doc(id).Get(ctx)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return nil, ErrNotFound
		}
		return nil, err
	}
	var d fsPodcastDoc
	if err := snap.DataTo(&d); err != nil {
		return nil, err
	}
	return podcastFromDoc(d), nil
}

func (s *Firestore) ListPodcasts(ctx context.Context) ([]*podcast.Podcast, error) {
	iter := s.podcasts().OrderBy("created_at", firestore.Desc).Documents(ctx)
	defer iter.Stop()
	var out []*podcast.Podcast
	for {
		snap, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, err
		}
		var d fsPodcastDoc
		if err := snap.DataTo(&d); err != nil {
			return nil, err
		}
		out = append(out, podcastFromDoc(d))
	}
	return out, nil
}

func (s *Firestore) UpdatePodcast(ctx context.Context, p *podcast.Podcast) error {
	ref := s.podcasts().Doc(p.ID)
	return s.client.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		snap, err := tx.Get(ref)
		if err != nil {
			if status.Code(err) == codes.NotFound {
				return ErrNotFound
			}
			return err
		}
		var existing fsPodcastDoc
		if err := snap.DataTo(&existing); err != nil {
			return err
		}
		doc := toPodcastDoc(p)
		if !existing.CreatedAt.IsZero() {
			doc.CreatedAt = existing.CreatedAt.UTC()
		}
		return tx.Set(ref, doc)
	})
}

func (s *Firestore) GetPodcastBySubmitKey(ctx context.Context, submitKey string) (*podcast.Podcast, error) {
	submitKey = strings.TrimSpace(submitKey)
	if submitKey == "" {
		return nil, ErrNotFound
	}
	iter := s.podcasts().Where("submit_key", "==", submitKey).Limit(1).Documents(ctx)
	defer iter.Stop()
	snap, err := iter.Next()
	if err == iterator.Done {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	var d fsPodcastDoc
	if err := snap.DataTo(&d); err != nil {
		return nil, err
	}
	p := podcastFromDoc(d)
	if !auth.APIKeyMatch(submitKey, p.SubmitKey) {
		return nil, ErrNotFound
	}
	return p, nil
}

func podcastFromDoc(d fsPodcastDoc) *podcast.Podcast {
	return &podcast.Podcast{
		ID: d.ID, Title: d.Title, Description: d.Description, Author: d.Author,
		ImageURL: d.ImageURL, Username: d.Username, Password: d.Password, Token: d.Token,
		SubmitKey: d.SubmitKey, CreatedAt: d.CreatedAt.UTC(),
	}
}

func (s *Firestore) Close() error {
	return s.client.Close()
}
