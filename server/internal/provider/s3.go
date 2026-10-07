package provider

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// Compile-time interface guard.
var _ BuildStorage = (*S3)(nil)

// S3 implements BuildStorage using AWS S3.
type S3 struct {
	client  *s3.Client
	presign *s3.PresignClient
	bucket  string
}

func NewS3(ctx context.Context, bucket, region string) (*S3, error) {
	cfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(region))
	if err != nil {
		return nil, err
	}
	client := s3.NewFromConfig(cfg)
	return &S3{
		client:  client,
		presign: s3.NewPresignClient(client),
		bucket:  bucket,
	}, nil
}

func (s *S3) GenerateUploadURL(ctx context.Context, objectKey string, expiry time.Duration) (string, error) {
	result, err := s.presign.PresignPutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(s.bucket),
		Key:         aws.String(objectKey),
		ContentType: aws.String("application/gzip"),
	}, s3.WithPresignExpires(expiry))
	if err != nil {
		return "", err
	}
	return result.URL, nil
}

func (s *S3) Download(ctx context.Context, objectKey string) (io.ReadCloser, error) {
	result, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(objectKey),
	})
	if err != nil {
		return nil, err
	}
	return result.Body, nil
}

func (s *S3) Upload(ctx context.Context, objectKey string, r io.Reader) error {
	_, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(s.bucket),
		Key:         aws.String(objectKey),
		Body:        r,
		ContentType: aws.String("application/gzip"),
	})
	return err
}

func (s *S3) Exists(ctx context.Context, objectKey string) (bool, error) {
	_, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(objectKey),
	})
	if err != nil {
		var notFound *types.NotFound
		if errors.As(err, &notFound) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}
