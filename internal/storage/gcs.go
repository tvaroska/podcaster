package storage

import (
	"context"
	"fmt"
	"io"
	"time"

	gcs "cloud.google.com/go/storage"
)

type GCS struct {
	client *gcs.Client
	bucket string
}

func OpenGCS(ctx context.Context, bucket string) (*GCS, error) {
	if bucket == "" {
		return nil, fmt.Errorf("GCS bucket is required")
	}
	client, err := gcs.NewClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("gcs client: %w", err)
	}
	return &GCS{client: client, bucket: bucket}, nil
}

func (s *GCS) obj(key string) *gcs.ObjectHandle {
	return s.client.Bucket(s.bucket).Object(key)
}

func (s *GCS) Put(ctx context.Context, key string, r io.Reader, _ int64, contentType string) error {
	w := s.obj(key).NewWriter(ctx)
	if contentType != "" {
		w.ContentType = contentType
	}
	if _, err := io.Copy(w, r); err != nil {
		_ = w.Close()
		return err
	}
	return w.Close()
}

func (s *GCS) Open(ctx context.Context, key string) (Object, error) {
	h := s.obj(key)
	attrs, err := h.Attrs(ctx)
	if err != nil {
		if err == gcs.ErrObjectNotExist {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &gcsObject{
		ctx:    ctx,
		handle: h,
		meta: ObjectMeta{
			Key:          key,
			Size:         attrs.Size,
			ContentType:  attrs.ContentType,
			LastModified: attrs.Updated,
		},
	}, nil
}

func (s *GCS) Delete(ctx context.Context, key string) error {
	err := s.obj(key).Delete(ctx)
	if err == gcs.ErrObjectNotExist {
		return ErrNotFound
	}
	return err
}

func (s *GCS) Stat(ctx context.Context, key string) (ObjectMeta, error) {
	attrs, err := s.obj(key).Attrs(ctx)
	if err != nil {
		if err == gcs.ErrObjectNotExist {
			return ObjectMeta{}, ErrNotFound
		}
		return ObjectMeta{}, err
	}
	return ObjectMeta{
		Key:          key,
		Size:         attrs.Size,
		ContentType:  attrs.ContentType,
		LastModified: attrs.Updated,
	}, nil
}

func (s *GCS) Close() error {
	return s.client.Close()
}

// SignedURL generates a direct, expiring download URL using GCS credentials.
func (s *GCS) SignedURL(ctx context.Context, key string, opts SignedURLOptions) (string, error) {
	expiry := opts.Expiry
	if expiry <= 0 {
		expiry = 15 * time.Minute
	}
	method := opts.Method
	if method == "" {
		method = "GET"
	}
	return s.client.Bucket(s.bucket).SignedURL(key, &gcs.SignedURLOptions{
		Method:  method,
		Expires: time.Now().Add(expiry),
	})
}

// gcsObject implements Object so http.ServeContent can Seek and issue Range reads.
type gcsObject struct {
	ctx    context.Context
	handle *gcs.ObjectHandle
	meta   ObjectMeta
	offset int64
	r      io.ReadCloser
}

func (o *gcsObject) Stat() ObjectMeta { return o.meta }

func (o *gcsObject) Read(p []byte) (int, error) {
	if o.r == nil {
		r, err := o.handle.NewRangeReader(o.ctx, o.offset, -1)
		if err != nil {
			return 0, err
		}
		o.r = r
	}
	n, err := o.r.Read(p)
	o.offset += int64(n)
	return n, err
}

func (o *gcsObject) Seek(offset int64, whence int) (int64, error) {
	var abs int64
	switch whence {
	case io.SeekStart:
		abs = offset
	case io.SeekCurrent:
		abs = o.offset + offset
	case io.SeekEnd:
		abs = o.meta.Size + offset
	default:
		return 0, fmt.Errorf("invalid whence")
	}
	if abs < 0 {
		return 0, fmt.Errorf("negative seek")
	}
	if o.r != nil {
		_ = o.r.Close()
		o.r = nil
	}
	o.offset = abs
	return abs, nil
}

func (o *gcsObject) Close() error {
	if o.r != nil {
		err := o.r.Close()
		o.r = nil
		return err
	}
	return nil
}
