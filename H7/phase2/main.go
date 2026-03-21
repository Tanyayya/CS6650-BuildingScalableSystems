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

	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(region))  //telling your app "hey, we're connecting to AWS in us-east-1
	if err != nil {
		log.Fatalf("[main] failed to load AWS config: %v", err)
	}

	endpointURL := os.Getenv("ENDPOINT_URL")
	topicARN    := os.Getenv("SNS_TOPIC_ARN")
	queueURL    := os.Getenv("SQS_QUEUE_URL")

	//  if ENDPOINT_URL is set, talk to LocalStack. If not, talk to real AWS
	var snsClient *sns.Client
	if endpointURL != "" {
		log.Printf("[main] using LocalStack endpoint: %s", endpointURL)
		snsClient = sns.NewFromConfig(cfg, func(o *sns.Options) {
			o.BaseEndpoint = aws.String(endpointURL)
		})
	} else {
		snsClient = sns.NewFromConfig(cfg)
	}

	// SQS is handled via raw HTTP in worker.go — no SDK client needed.

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

	// SQS worker, only starts when SQS_QUEUE_URL is set
	workerCtx, workerCancel := context.WithCancel(ctx)
	if queueURL != "" {
		worker := NewSQSWorker(queueURL, processor)
		go worker.Start(workerCtx)
		log.Printf("[main] SQS worker started — queue=%s", queueURL)
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
// It's a logger. Every time any request hits  server, this runs and writes a log line like

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