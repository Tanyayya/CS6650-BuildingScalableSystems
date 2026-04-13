package main

import (
	"context"
	"log"
	"os"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
)

var ddb *dynamodb.Client
var albumsTable string
var photosTable string

func initDB() {
	albumsTable = envOr("ALBUMS_TABLE", "albums")
	photosTable = envOr("PHOTOS_TABLE", "photos")

	cfg, err := config.LoadDefaultConfig(context.Background())
	if err != nil {
		log.Fatalf("AWS config error: %v", err)
	}
	ddb = dynamodb.NewFromConfig(cfg)
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
