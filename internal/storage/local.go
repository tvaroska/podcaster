package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

type Local struct {
	root string
}

func OpenLocal(root string) (*Local, error) {
	if root == "" {
		return nil, errors.New("local storage root is required")
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	return &Local{root: root}, nil
}

func (l *Local) resolve(key string) (string, error) {
	if key == "" || strings.Contains(key, "..") {
		return "", fmt.Errorf("invalid object key %q", key)
	}
	clean := path.Clean("/" + strings.ReplaceAll(key, "\\", "/"))
	clean = strings.TrimPrefix(clean, "/")
	if clean == "" || clean == "." {
		return "", fmt.Errorf("invalid object key %q", key)
	}
	full := filepath.Join(l.root, filepath.FromSlash(clean))
	rel, err := filepath.Rel(l.root, full)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("invalid object key %q", key)
	}
	return full, nil
}

func (l *Local) Put(_ context.Context, key string, r io.Reader, _ int64, _ string) error {
	path, err := l.resolve(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	_, copyErr := io.Copy(f, r)
	closeErr := f.Close()
	if copyErr != nil {
		_ = os.Remove(tmp)
		return copyErr
	}
	if closeErr != nil {
		_ = os.Remove(tmp)
		return closeErr
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func (l *Local) Open(_ context.Context, key string) (Object, error) {
	path, err := l.resolve(key)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	st, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	return &fileObject{
		File: f,
		meta: ObjectMeta{
			Key:          key,
			Size:         st.Size(),
			LastModified: st.ModTime(),
			ContentType:  sniffContentType(key),
		},
	}, nil
}

func (l *Local) Delete(_ context.Context, key string) error {
	path, err := l.resolve(key)
	if err != nil {
		return err
	}
	err = os.Remove(path)
	if os.IsNotExist(err) {
		return ErrNotFound
	}
	return err
}

func (l *Local) Stat(_ context.Context, key string) (ObjectMeta, error) {
	path, err := l.resolve(key)
	if err != nil {
		return ObjectMeta{}, err
	}
	st, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return ObjectMeta{}, ErrNotFound
		}
		return ObjectMeta{}, err
	}
	return ObjectMeta{
		Key:          key,
		Size:         st.Size(),
		LastModified: st.ModTime(),
		ContentType:  sniffContentType(key),
	}, nil
}

type fileObject struct {
	*os.File
	meta ObjectMeta
}

func (f *fileObject) Stat() ObjectMeta { return f.meta }

func sniffContentType(key string) string {
	switch strings.ToLower(filepath.Ext(key)) {
	case ".mp3":
		return "audio/mpeg"
	case ".wav":
		return "audio/wav"
	case ".aac", ".m4a":
		return "audio/aac"
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	default:
		return "application/octet-stream"
	}
}
