package provider

import (
	"context"
	"fmt"
	"io"
	"time"

	"cloud.google.com/go/storage"
)

// Compile-time interface guard.
var _ BuildStorage = (*GCS)(nil)

// GCS implements BuildStorage using Google Cloud Storage.
type GCS struct {
	client *storage.Client
	bucket string
}

func NewGCS(ctx context.Context, bucket string) (*GCS, error) {
	client, err := storage.NewClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("create GCS client: %w", err)
	}
	return &GCS{client: client, bucket: bucket}, nil
}

func (g *GCS) GenerateUploadURL(ctx context.Context, objectKey string, expiry time.Duration) (string, error) {
	url, err := g.client.Bucket(g.bucket).SignedURL(objectKey, &storage.SignedURLOptions{
		Method:      "PUT",
		Expires:     time.Now().Add(expiry),
		ContentType: "application/gzip",
	})
	if err != nil {
		return "", fmt.Errorf("generate signed URL: %w", err)
	}
	return url, nil
}

func (g *GCS) Download(ctx context.Context, objectKey string) (io.ReadCloser, error) {
	r, err := g.client.Bucket(g.bucket).Object(objectKey).NewReader(ctx)
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", objectKey, err)
	}
	return r, nil
}

func (g *GCS) Upload(ctx context.Context, objectKey string, r io.Reader) error {
	w := g.client.Bucket(g.bucket).Object(objectKey).NewWriter(ctx)
	w.ContentType = "application/gzip"
	if _, err := io.Copy(w, r); err != nil {
		w.Close()
		return fmt.Errorf("upload %s: %w", objectKey, err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("close writer for %s: %w", objectKey, err)
	}
	return nil
}

func (g *GCS) Exists(ctx context.Context, objectKey string) (bool, error) {
	_, err := g.client.Bucket(g.bucket).Object(objectKey).Attrs(ctx)
	if err == storage.ErrObjectNotExist {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("check existence %s: %w", objectKey, err)
	}
	return true, nil
}

func (g *GCS) Close() error {
	return g.client.Close()
}
