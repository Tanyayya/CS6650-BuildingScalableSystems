package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// SQSWorker polls SQS using raw HTTP requests (no SDK).
type SQSWorker struct {
	queueURL  string
	processor *PaymentProcessor
	client    *http.Client
	// metrics
	totalProcessed atomic.Int64
	totalFailed    atomic.Int64
}

func NewSQSWorker(queueURL string, processor *PaymentProcessor) *SQSWorker {
	return &SQSWorker{
		queueURL:  queueURL,
		processor: processor,
		client:    &http.Client{Timeout: 30 * time.Second},
	}
}

// StartPool launches n goroutines all polling the same queue.
// This is the Phase 5 scaling lever — set NUM_WORKERS env var.
//
// Processing rate math:
//   1 worker  × (1/3s) = 0.33 orders/sec
//   5 workers × (1/3s) = 1.67 orders/sec
//  20 workers × (1/3s) = 6.67 orders/sec
// 100 workers × (1/3s) = 33.3 orders/sec
// 182 workers × (1/3s) = 60.6 orders/sec  ← matches 60/sec flash sale
func (w *SQSWorker) StartPool(ctx context.Context) {
	n := workerCount()
	log.Printf("[worker] starting pool of %d goroutines", n)
	log.Printf("[worker] theoretical throughput: %.2f orders/sec", float64(n)/3.0)
	log.Printf("[worker] queue URL: %s", w.queueURL)

	for i := 0; i < n; i++ {
		go func(id int) {
			log.Printf("[worker-%d] started", id)
			for {
				select {
				case <-ctx.Done():
					return
				default:
					w.poll(ctx, id)
				}
			}
		}(i + 1)
	}

	// Stats reporter — logs throughput every 10s
	go w.reportStats(ctx, n)
}

// workerCount reads NUM_WORKERS env var, defaults to 1.
func workerCount() int {
	n, err := strconv.Atoi(os.Getenv("NUM_WORKERS"))
	if err != nil || n < 1 {
		return 1
	}
	return n
}

// reportStats logs processing rate every 10 seconds.
func (w *SQSWorker) reportStats(ctx context.Context, numWorkers int) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	var lastProcessed int64
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			current := w.totalProcessed.Load()
			delta := current - lastProcessed
			lastProcessed = current
			rate := float64(delta) / 10.0
			log.Printf("[worker-stats] workers=%d processed_last_10s=%d rate=%.2f/sec total=%d failed=%d",
				numWorkers, delta, rate, current, w.totalFailed.Load())
		}
	}
}

// poll does one ReceiveMessage call then spawns a goroutine per message.
func (w *SQSWorker) poll(ctx context.Context, workerID int) {
	params := url.Values{}
	params.Set("Action", "ReceiveMessage")
	params.Set("MaxNumberOfMessages", "10")
	params.Set("WaitTimeSeconds", "5")
	params.Set("VisibilityTimeout", "30")
	params.Set("Version", "2012-11-05")

	req, err := http.NewRequestWithContext(ctx, "POST", w.queueURL,
		strings.NewReader(params.Encode()))
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		time.Sleep(5 * time.Second)
		return
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := w.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		log.Printf("[worker-%d] ReceiveMessage error: %v (retrying in 5s)", workerID, err)
		time.Sleep(5 * time.Second)
		return
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if len(body) == 0 {
		return
	}

	msgs := w.parseXMLMessages(string(body))
	if len(msgs) == 0 {
		return
	}

	log.Printf("[worker-%d] received %d message(s)", workerID, len(msgs))
	for _, msg := range msgs {
		go w.processMessage(ctx, workerID, msg.body, msg.receipt)
	}
}

type xmlMsg struct {
	body    string
	receipt string
}

func (w *SQSWorker) parseXMLMessages(xml string) []xmlMsg {
	var msgs []xmlMsg
	for {
		start := strings.Index(xml, "<Message>")
		end := strings.Index(xml, "</Message>")
		if start == -1 || end == -1 {
			break
		}
		block := xml[start+9 : end]
		xml = xml[end+10:]
		body := extractXMLTag(block, "Body")
		receipt := extractXMLTag(block, "ReceiptHandle")
		if body != "" {
			msgs = append(msgs, xmlMsg{body: body, receipt: receipt})
		}
	}
	return msgs
}

func extractXMLTag(s, tag string) string {
	open := fmt.Sprintf("<%s>", tag)
	close := fmt.Sprintf("</%s>", tag)
	start := strings.Index(s, open)
	end := strings.Index(s, close)
	if start == -1 || end == -1 {
		return ""
	}
	return s[start+len(open) : end]
}

type SNSEnvelope struct {
	Message string `json:"Message"`
}

func (w *SQSWorker) processMessage(ctx context.Context, workerID int, body, receipt string) {
	body = strings.ReplaceAll(body, "&quot;", `"`)
	body = strings.ReplaceAll(body, "&amp;", "&")
	body = strings.ReplaceAll(body, "&lt;", "<")
	body = strings.ReplaceAll(body, "&gt;", ">")

	var envelope SNSEnvelope
	orderJSON := body
	if err := json.Unmarshal([]byte(body), &envelope); err == nil && envelope.Message != "" {
		orderJSON = envelope.Message
	}

	var order Order
	if err := json.Unmarshal([]byte(orderJSON), &order); err != nil {
		log.Printf("[worker-%d] bad message, discarding: %v", workerID, err)
		w.deleteMessage(ctx, receipt)
		return
	}

	log.Printf("[worker-%d] processing order=%s customer=%d", workerID, order.OrderID, order.CustomerID)

	if err := w.processor.VerifyPayment(order.OrderID, order.CustomerID); err != nil {
		log.Printf("[worker-%d] order=%s payment FAILED: %v", workerID, order.OrderID, err)
		w.totalFailed.Add(1)
		return // leave in queue for retry
	}

	w.totalProcessed.Add(1)
	log.Printf("[worker-%d] order=%s verified ✓ (total=%d)", workerID, order.OrderID, w.totalProcessed.Load())
	w.deleteMessage(ctx, receipt)
}

func (w *SQSWorker) deleteMessage(ctx context.Context, receipt string) {
	params := url.Values{}
	params.Set("Action", "DeleteMessage")
	params.Set("ReceiptHandle", receipt)
	params.Set("Version", "2012-11-05")

	req, err := http.NewRequestWithContext(ctx, "POST", w.queueURL,
		strings.NewReader(params.Encode()))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := w.client.Do(req)
	if err != nil {
		return
	}
	resp.Body.Close()
}