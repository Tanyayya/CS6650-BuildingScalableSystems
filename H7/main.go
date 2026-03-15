package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sns"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	ctx := context.Background()

	region := os.Getenv("AWS_REGION")
	if region == "" {
		region = "us-east-1"
	}

	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(region))
	if err != nil {
		log.Fatalf("[main] failed to load AWS config: %v", err)
	}

	endpointURL := os.Getenv("ENDPOINT_URL")
	topicARN    := os.Getenv("SNS_TOPIC_ARN")
	queueURL    := os.Getenv("SQS_QUEUE_URL")

	var snsClient *sns.Client
	if endpointURL != "" {
		log.Printf("[main] using LocalStack endpoint: %s", endpointURL)
		snsClient = sns.NewFromConfig(cfg, func(o *sns.Options) {
			o.BaseEndpoint = aws.String(endpointURL)
		})
	} else {
		snsClient = sns.NewFromConfig(cfg)
	}

	processor := NewPaymentProcessor()
	h := NewHandler(processor, snsClient, topicARN)

	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	srv := &http.Server{
		Addr:         ":" + port,
		Handler:      loggingMiddleware(mux),
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	workerCtx, workerCancel := context.WithCancel(ctx)
	if queueURL != "" {
		// StartPool reads NUM_WORKERS env var to determine goroutine count.
		// Phase 5 scaling tests — set NUM_WORKERS in docker-compose:
		//   NUM_WORKERS=1   → 0.33 orders/sec
		//   NUM_WORKERS=5   → 1.67 orders/sec
		//   NUM_WORKERS=20  → 6.67 orders/sec
		//   NUM_WORKERS=100 → 33.3 orders/sec
		//   NUM_WORKERS=182 → 60.6 orders/sec (matches flash sale)
		worker := NewSQSWorker(queueURL, processor)
		go worker.StartPool(workerCtx)
	} else {
		log.Println("[main] SQS_QUEUE_URL not set — receiver-only mode")
	}

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGTERM, syscall.SIGINT)

	go func() {
		log.Printf("[server] listening on :%s", port)
		log.Printf("[server] SNS topic: %s", topicARN)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("[server] fatal: %v", err)
		}
	}()

	<-quit
	log.Println("[server] shutting down...")
	workerCancel()

	shutdownCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Fatalf("[server] forced shutdown: %v", err)
	}
	log.Println("[server] stopped")
}

func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rw := &responseWriter{ResponseWriter: w, status: 200}
		next.ServeHTTP(rw, r)
		log.Printf("[http] %s %s %d %v",
			r.Method, r.URL.Path, rw.status, time.Since(start).Round(time.Millisecond))
	})
}

type responseWriter struct {
	http.ResponseWriter
	status int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.status = code
	rw.ResponseWriter.WriteHeader(code)
}