// Package store is the blob storage abstraction: local disk or any
// S3-compatible endpoint (Cloudflare R2, MinIO, AWS S3, Backblaze B2...).
package store

import (
	"context"
	"io"
)

// Store stores opaque blobs under a key (e.g. "files/ab/<sha256>.png").
type Store interface {
	Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) error
	Get(ctx context.Context, key string) (io.ReadCloser, error)
	Delete(ctx context.Context, key string) error
	Exists(ctx context.Context, key string) (bool, error)
	// URL returns a direct link to the blob ("" when the API must proxy it),
	// plus the seconds until it expires (0 = permanent).
	URL(ctx context.Context, key string) (string, int)
	Kind() string
}

// KeyFor builds the storage key for an original file.
func KeyFor(sha, ext string) string { return "files/" + shard(sha) + "/" + sha + "." + ext }

// ThumbKeyFor builds the storage key for a thumbnail (thumbExt is jpg or png).
func ThumbKeyFor(sha, thumbExt string) string {
	return "thumbs/" + shard(sha) + "/" + sha + "." + thumbExt
}

// shard spreads blobs over 256 subdirectories so no directory gets huge.
func shard(sha string) string {
	if len(sha) < 2 {
		return "00"
	}
	return sha[:2]
}
