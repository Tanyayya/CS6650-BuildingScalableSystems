package main

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"os"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

var s3Client *s3.Client
var s3Bucket string

func initStorage() {
	s3Bucket = os.Getenv("S3_BUCKET")
	if s3Bucket == "" {
		log.Println("S3_BUCKET not set")
		return
	}
	cfg, err := config.LoadDefaultConfig(context.Background())
	if err != nil {
		log.Printf("AWS config error: %v", err)
		return
	}
	s3Client = s3.NewFromConfig(cfg)
}

func uploadToS3(ctx context.Context, key, contentType string, data []byte) (string, error) {
	if s3Client == nil {
		return "", fmt.Errorf("S3 not configured")
	}
	_, err := s3Client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(s3Bucket),
		Key:         aws.String(key),
		Body:        bytes.NewReader(data),
		ContentType: aws.String(contentType),
	})
	if err != nil {
		return "", err
	}
	region := os.Getenv("AWS_REGION")
	return fmt.Sprintf("https://%s.s3.%s.amazonaws.com/%s", s3Bucket, region, key), nil
}

func deleteFile(ctx context.Context, key string) error {
	if s3Client == nil {
		return nil
	}
	_, err := s3Client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(s3Bucket),
		Key:    aws.String(key),
	})
	return err
}
