// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: AGPL-3.0-only

package objects

import (
	"bytes"
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

// S3 stores objects in S3-compatible storage: AWS S3, MinIO, and any
// other service with the same API (SRV-023).
type S3 struct {
	client  *s3.Client
	presign *s3.PresignClient
	bucket  string
	cdnBase string
}

// S3Options configures NewS3.
type S3Options struct {
	// Endpoint is the service address; empty uses AWS with Region.
	Endpoint string
	Region   string
	Bucket   string
	// PathStyle addresses the bucket in the path, which self-hosted
	// services such as MinIO need.
	PathStyle bool
	// AccessKeyID and SecretAccessKey are used when set; otherwise the
	// ambient credentials of the environment are used.
	AccessKeyID     string
	SecretAccessKey string
	// CDNBaseURL is handed out instead of a signed URL when set.
	CDNBaseURL string
}

// NewS3 builds the client. It makes no network call, so a server starts
// even when the storage is briefly unreachable; /readyz reports that.
func NewS3(ctx context.Context, opts S3Options) (*S3, error) {
	if opts.Bucket == "" {
		return nil, errors.New("objects: the s3 backend needs a bucket")
	}
	loadOpts := []func(*awsconfig.LoadOptions) error{}
	if opts.Region != "" {
		loadOpts = append(loadOpts, awsconfig.WithRegion(opts.Region))
	} else {
		// A self-hosted service ignores the region, but the SDK requires
		// one to sign.
		loadOpts = append(loadOpts, awsconfig.WithRegion("us-east-1"))
	}
	if opts.AccessKeyID != "" {
		loadOpts = append(loadOpts, awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(opts.AccessKeyID, opts.SecretAccessKey, "")))
	}
	cfg, err := awsconfig.LoadDefaultConfig(ctx, loadOpts...)
	if err != nil {
		return nil, fmt.Errorf("objects: load the AWS configuration: %w", err)
	}
	client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		if opts.Endpoint != "" {
			o.BaseEndpoint = aws.String(opts.Endpoint)
		}
		o.UsePathStyle = opts.PathStyle
		// A stub or a self-hosted service may return no checksum; that
		// is not worth a log line on every read.
		o.DisableLogOutputChecksumValidationSkipped = true
	})
	return &S3{
		client:  client,
		presign: s3.NewPresignClient(client),
		bucket:  opts.Bucket,
		cdnBase: strings.TrimSuffix(opts.CDNBaseURL, "/"),
	}, nil
}

// Put stores the object unless it is already there. Objects are
// immutable, so an existing key of the same size is left alone.
func (s *S3) Put(ctx context.Context, key string, data []byte, mediaType string) (Info, error) {
	if err := ValidKey(key); err != nil {
		return Info{}, err
	}
	if info, err := s.Stat(ctx, key); err == nil {
		return Info{Key: key, Size: info.Size, MediaType: mediaType, ModTime: info.ModTime}, nil
	} else if !errors.Is(err, ErrNotFound) {
		return Info{}, err
	}
	in := &s3.PutObjectInput{
		Bucket:        aws.String(s.bucket),
		Key:           aws.String(key),
		Body:          bytes.NewReader(data),
		ContentLength: aws.Int64(int64(len(data))),
		// Artifacts are immutable and content-addressed, so they are
		// cacheable for ever (REL-024).
		CacheControl: aws.String("public, max-age=31536000, immutable"),
	}
	if mediaType != "" {
		in.ContentType = aws.String(mediaType)
	}
	if _, err := s.client.PutObject(ctx, in); err != nil {
		return Info{}, fmt.Errorf("objects: put %s: %w", key, err)
	}
	return Info{Key: key, Size: int64(len(data)), MediaType: mediaType, ModTime: time.Now().UTC()}, nil
}

// Get reads the whole object.
func (s *S3) Get(ctx context.Context, key string) ([]byte, Info, error) {
	r, info, err := s.Open(ctx, key, 0, -1)
	if err != nil {
		return nil, Info{}, err
	}
	defer func() { _ = r.Close() }()
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, Info{}, fmt.Errorf("objects: read %s: %w", key, err)
	}
	return data, info, nil
}

// Open returns a reader over a byte range, using an HTTP range request
// so that only the wanted bytes cross the network (REL-024).
func (s *S3) Open(ctx context.Context, key string, offset, n int64) (io.ReadCloser, Info, error) {
	if err := ValidKey(key); err != nil {
		return nil, Info{}, err
	}
	in := &s3.GetObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key)}
	if offset > 0 || n >= 0 {
		if n < 0 {
			in.Range = aws.String(fmt.Sprintf("bytes=%d-", offset))
		} else {
			in.Range = aws.String(fmt.Sprintf("bytes=%d-%d", offset, offset+n-1))
		}
	}
	out, err := s.client.GetObject(ctx, in)
	if err != nil {
		return nil, Info{}, wrapNotFound(key, err)
	}
	info := Info{Key: key}
	if out.ContentLength != nil {
		info.Size = *out.ContentLength
	}
	if out.ContentType != nil {
		info.MediaType = *out.ContentType
	}
	if out.LastModified != nil {
		info.ModTime = *out.LastModified
	}
	return out.Body, info, nil
}

// Stat returns the object's metadata.
func (s *S3) Stat(ctx context.Context, key string) (Info, error) {
	if err := ValidKey(key); err != nil {
		return Info{}, err
	}
	out, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(s.bucket), Key: aws.String(key),
	})
	if err != nil {
		return Info{}, wrapNotFound(key, err)
	}
	info := Info{Key: key}
	if out.ContentLength != nil {
		info.Size = *out.ContentLength
	}
	if out.ContentType != nil {
		info.MediaType = *out.ContentType
	}
	if out.LastModified != nil {
		info.ModTime = *out.LastModified
	}
	return info, nil
}

// Delete removes the object.
func (s *S3) Delete(ctx context.Context, key string) error {
	if err := ValidKey(key); err != nil {
		return err
	}
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(s.bucket), Key: aws.String(key),
	})
	if err != nil {
		return fmt.Errorf("objects: delete %s: %w", key, err)
	}
	return nil
}

// URL returns the CDN location when one is configured, and otherwise a
// signed URL valid for ttl (SRV-023).
func (s *S3) URL(ctx context.Context, key string, ttl time.Duration) (string, error) {
	if s.cdnBase != "" {
		return s.cdnBase + "/" + key, nil
	}
	req, err := s.presign.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket), Key: aws.String(key),
	}, s3.WithPresignExpires(ttl))
	if err != nil {
		return "", fmt.Errorf("objects: sign a URL for %s: %w", key, err)
	}
	return req.URL, nil
}

// wrapNotFound turns the service's "no such key" into ErrNotFound, so
// callers do not depend on the SDK's error types.
func wrapNotFound(key string, err error) error {
	var noKey *types.NoSuchKey
	var notFound *types.NotFound
	if errors.As(err, &noKey) || errors.As(err, &notFound) {
		return fmt.Errorf("%w: %s", ErrNotFound, key)
	}
	return fmt.Errorf("objects: %s: %w", key, err)
}
