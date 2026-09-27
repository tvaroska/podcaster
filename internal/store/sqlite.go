package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tvaroska/podcaster/internal/episode"
	"github.com/tvaroska/podcaster/internal/podcast"

	_ "modernc.org/sqlite"
)

const sqliteSchema = `
CREATE TABLE IF NOT EXISTS episodes (
    id TEXT PRIMARY KEY,
    title TEXT NOT NULL,
    script_text TEXT NOT NULL,
    category TEXT NOT NULL DEFAULT '',
    voice_id TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL,
    audio_uri TEXT NOT NULL DEFAULT '',
    duration_seconds REAL NOT NULL DEFAULT 0,
    file_size_bytes INTEGER NOT NULL DEFAULT 0,
    content_type TEXT NOT NULL DEFAULT '',
    error_message TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    published_at TEXT
);
CREATE INDEX IF NOT EXISTS idx_episodes_created_at ON episodes(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_episodes_status ON episodes(status);
CREATE TABLE IF NOT EXISTS podcasts (
    id TEXT PRIMARY KEY,
    title TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    author TEXT NOT NULL DEFAULT '',
    username TEXT NOT NULL,
    password TEXT NOT NULL,
    token TEXT NOT NULL,
    created_at TEXT NOT NULL
);
`

type SQLite struct {
	db *sql.DB
}

func OpenSQLite(path string) (*SQLite, error) {
	if path != ":memory:" && !strings.HasPrefix(path, "file:") {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, fmt.Errorf("create sqlite dir: %w", err)
		}
	}
	dsn := path
	if path != ":memory:" && !strings.Contains(path, "?") {
		dsn = path + "?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(sqliteSchema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate sqlite: %w", err)
	}
	if err := ensureColumn(db, "episodes", "podcast_id", "TEXT NOT NULL DEFAULT ''"); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate sqlite podcast_id: %w", err)
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_episodes_podcast ON episodes(podcast_id, created_at DESC)`); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &SQLite{db: db}, nil
}

func (s *SQLite) Create(ctx context.Context, ep *episode.Episode) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO episodes (
    id, podcast_id, title, script_text, category, voice_id, status, audio_uri,
    duration_seconds, file_size_bytes, content_type, error_message, created_at, published_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		ep.ID, ep.PodcastID, ep.Title, ep.ScriptText, ep.Category, ep.VoiceID, string(ep.Status), ep.AudioURI,
		ep.DurationSeconds, ep.FileSizeBytes, ep.ContentType, ep.ErrorMessage,
		ep.CreatedAt.UTC().Format(time.RFC3339Nano), formatTimePtr(ep.PublishedAt),
	)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return ErrAlreadyExists
		}
		return err
	}
	return nil
}

func (s *SQLite) Get(ctx context.Context, id string) (*episode.Episode, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+episodeColumns+` FROM episodes WHERE id = ?`, id)
	ep, err := scanEpisode(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return ep, err
}

func (s *SQLite) List(ctx context.Context, f episode.ListFilter) ([]*episode.Episode, error) {
	limit := f.Limit
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	offset := f.Offset
	if offset < 0 {
		offset = 0
	}

	q := `SELECT ` + episodeColumns + ` FROM episodes`
	args := []any{}
	var wheres []string
	if f.Status != "" {
		status := f.Status
		if status == episode.StatusQueued {
			status = episode.StatusPending
		}
		wheres = append(wheres, `status = ?`)
		args = append(args, string(status))
	}
	if f.OnlyDefault {
		wheres = append(wheres, `podcast_id = ''`)
	} else if f.PodcastID != "" {
		wheres = append(wheres, `podcast_id = ?`)
		args = append(args, f.PodcastID)
	}
	if len(wheres) > 0 {
		q += ` WHERE ` + strings.Join(wheres, ` AND `)
	}
	q += ` ORDER BY created_at DESC LIMIT ? OFFSET ?`
	args = append(args, limit, offset)

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*episode.Episode
	for rows.Next() {
		ep, err := scanEpisode(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, ep)
	}
	return out, rows.Err()
}

func (s *SQLite) Update(ctx context.Context, ep *episode.Episode) error {
	res, err := s.db.ExecContext(ctx, `
UPDATE episodes SET
    podcast_id=?, title=?, script_text=?, category=?, voice_id=?, status=?, audio_uri=?,
    duration_seconds=?, file_size_bytes=?, content_type=?, error_message=?, published_at=?
WHERE id=?`,
		ep.PodcastID, ep.Title, ep.ScriptText, ep.Category, ep.VoiceID, string(ep.Status), ep.AudioURI,
		ep.DurationSeconds, ep.FileSizeBytes, ep.ContentType, ep.ErrorMessage,
		formatTimePtr(ep.PublishedAt), ep.ID,
	)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *SQLite) CompareAndSwapStatus(ctx context.Context, id string, from, to episode.Status) (*episode.Episode, error) {
	res, err := s.db.ExecContext(ctx, `UPDATE episodes SET status=? WHERE id=? AND status=?`, string(to), id, string(from))
	if err != nil {
		return nil, err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		ep, gerr := s.Get(ctx, id)
		if gerr != nil {
			return nil, gerr
		}
		return ep, ErrConflict
	}
	return s.Get(ctx, id)
}

func (s *SQLite) Delete(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM episodes WHERE id=?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *SQLite) Ping(ctx context.Context) error {
	return s.db.PingContext(ctx)
}

func (s *SQLite) Close() error {
	return s.db.Close()
}

const episodeColumns = `id, podcast_id, title, script_text, category, voice_id, status, audio_uri, duration_seconds, file_size_bytes, content_type, error_message, created_at, published_at`

type rowScanner interface {
	Scan(dest ...any) error
}

func scanEpisode(row rowScanner) (*episode.Episode, error) {
	var (
		ep        episode.Episode
		status    string
		created   string
		published sql.NullString
		duration  float64
		fileSize  int64
	)
	err := row.Scan(
		&ep.ID, &ep.PodcastID, &ep.Title, &ep.ScriptText, &ep.Category, &ep.VoiceID, &status, &ep.AudioURI,
		&duration, &fileSize, &ep.ContentType, &ep.ErrorMessage, &created, &published,
	)
	if err != nil {
		return nil, err
	}
	ep.Status = episode.Status(status)
	ep.DurationSeconds = duration
	ep.FileSizeBytes = fileSize
	t, err := time.Parse(time.RFC3339Nano, created)
	if err != nil {
		t, err = time.Parse(time.RFC3339, created)
		if err != nil {
			return nil, fmt.Errorf("parse created_at: %w", err)
		}
	}
	ep.CreatedAt = t.UTC()
	if published.Valid && published.String != "" {
		pt, err := time.Parse(time.RFC3339Nano, published.String)
		if err != nil {
			pt, err = time.Parse(time.RFC3339, published.String)
			if err != nil {
				return nil, fmt.Errorf("parse published_at: %w", err)
			}
		}
		pt = pt.UTC()
		ep.PublishedAt = &pt
	}
	return &ep, nil
}

func (s *SQLite) CreatePodcast(ctx context.Context, p *podcast.Podcast) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO podcasts (id, title, description, author, username, password, token, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		p.ID, p.Title, p.Description, p.Author, p.Username, p.Password, p.Token,
		p.CreatedAt.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return ErrAlreadyExists
		}
		return err
	}
	return nil
}

func (s *SQLite) GetPodcast(ctx context.Context, id string) (*podcast.Podcast, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT id, title, description, author, username, password, token, created_at
FROM podcasts WHERE id = ?`, id)
	p, err := scanPodcast(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return p, err
}

func (s *SQLite) ListPodcasts(ctx context.Context) ([]*podcast.Podcast, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, title, description, author, username, password, token, created_at
FROM podcasts ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*podcast.Podcast
	for rows.Next() {
		p, err := scanPodcast(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func scanPodcast(row rowScanner) (*podcast.Podcast, error) {
	var p podcast.Podcast
	var created string
	if err := row.Scan(&p.ID, &p.Title, &p.Description, &p.Author, &p.Username, &p.Password, &p.Token, &created); err != nil {
		return nil, err
	}
	t, err := time.Parse(time.RFC3339Nano, created)
	if err != nil {
		t, err = time.Parse(time.RFC3339, created)
		if err != nil {
			return nil, fmt.Errorf("parse podcast created_at: %w", err)
		}
	}
	p.CreatedAt = t.UTC()
	return &p, nil
}

func ensureColumn(db *sql.DB, table, col, decl string) error {
	rows, err := db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return err
		}
		if name == col {
			return nil
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	_, err = db.Exec(`ALTER TABLE ` + table + ` ADD COLUMN ` + col + ` ` + decl)
	return err
}

func formatTimePtr(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UTC().Format(time.RFC3339Nano)
}
