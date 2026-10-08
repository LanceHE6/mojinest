package store

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// S3 stores blobs in any S3-compatible bucket (Cloudflare R2, MinIO, ...).
type S3 struct {
	client     *s3.Client
	presigner  *s3.PresignClient
	bucket     string
	publicBase string
}

// S3Options configures the S3-compatible backend.
type S3Options struct {
	Endpoint   string
	Region     string
	Bucket     string
	AccessKey  string
	SecretKey  string
	PathStyle  bool
	PublicBase string
}

// NewS3 builds an S3-compatible store.
func NewS3(ctx context.Context, o S3Options) (*S3, error) {
	if o.Bucket == "" {
		return nil, errors.New("s3: bucket is required")
	}
	if o.Region == "" {
		o.Region = "auto"
	}
	cfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion(o.Region),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(o.AccessKey, o.SecretKey, "")),
	)
	if err != nil {
		return nil, err
	}
	client := s3.NewFromConfig(cfg, func(opt *s3.Options) {
		if o.Endpoint != "" {
			opt.BaseEndpoint = aws.String(o.Endpoint)
		}
		opt.UsePathStyle = o.PathStyle
	})
	return &S3{
		client:     client,
		presigner:  s3.NewPresignClient(client),
		bucket:     o.Bucket,
		publicBase: strings.TrimRight(o.PublicBase, "/"),
	}, nil
}

func (s *S3) Kind() string { return "s3" }

func (s *S3) Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) error {
	in := &s3.PutObjectInput{
		Bucket:      aws.String(s.bucket),
		Key:         aws.String(key),
		Body:        r,
		ContentType: aws.String(contentType),
	}
	if size > 0 {
		in.ContentLength = aws.Int64(size)
	}
	_, err := s.client.PutObject(ctx, in)
	return err
}

func (s *S3) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		var nsk *types.NoSuchKey
		if errors.As(err, &nsk) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return out.Body, nil
}

func (s *S3) Delete(ctx context.Context, key string) error {
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	return err
}

func (s *S3) Exists(ctx context.Context, key string) (bool, error) {
	_, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err == nil {
		return true, nil
	}
	var nf *types.NotFound
	if errors.As(err, &nf) {
		return false, nil
	}
	return false, fmt.Errorf("s3 head %s: %w", key, err)
}

// URL returns a public link when configured, otherwise a 1-hour presigned URL.
func (s *S3) URL(ctx context.Context, key string) (string, int) {
	if s.publicBase != "" {
		return s.publicBase + "/" + key, 0
	}
	out, err := s.presigner.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	}, func(o *s3.PresignOptions) { o.Expires = time.Hour })
	if err != nil {
		return "", 0
	}
	return out.URL, 3600
}
