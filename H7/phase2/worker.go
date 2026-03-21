package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// SQSWorker polls SQS using raw HTTP requests (no SDK).
// This avoids EndpointResolverV2 issues with LocalStack entirely.
type SQSWorker struct {
	queueURL  string
	processor *PaymentProcessor
	client    *http.Client     // the tool used to make HTTP requests to AWS

	awsRegion string
}

func NewSQSWorker(queueURL string, processor *PaymentProcessor) *SQSWorker {
	return &SQSWorker{
		queueURL:  queueURL,
		processor: processor,
		client:    &http.Client{Timeout: 30 * time.Second},  //  if AWS doesn't respond in 30 seconds, stop waiting and try again

	}
}

func (w *SQSWorker) Start(ctx context.Context) {
	log.Printf("[worker] starting — polling %s", w.queueURL)
	for {
		select {
		case <-ctx.Done():
			log.Println("[worker] stopped")
			return
		default:
			w.poll(ctx)
		}
	}
}

// sqsMessage is one entry in a ReceiveMessage response.
type sqsMessage struct {
	MessageId     string `json:"MessageId"`
	ReceiptHandle string `json:"ReceiptHandle"`
	Body          string `json:"Body"`
}

// sqsReceiveResponse is the JSON body from LocalStack ReceiveMessage.
type sqsReceiveResponse struct {
	ReceiveMessageResponse struct {
		ReceiveMessageResult struct {
			Messages []sqsMessage `json:"messages"`
		} `json:"ReceiveMessageResult"`
	} `json:"ReceiveMessageResponse"`
}

func (w *SQSWorker) poll(ctx context.Context) {      // poll — checking the queue
	// Build ReceiveMessage query
	params := url.Values{}
	params.Set("Action", "ReceiveMessage")
	params.Set("MaxNumberOfMessages", "10")
	params.Set("WaitTimeSeconds", "5")
	params.Set("VisibilityTimeout", "30")
	params.Set("Version", "2012-11-05")

	// Build the HTTP request to send to SQS. If building it fails 
	// check if we're shutting down first, otherwise log the error and wait 5 seconds before trying again.
	req, err := http.NewRequestWithContext(ctx, "POST", w.queueURL,      
		strings.NewReader(params.Encode()))
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		log.Printf("[worker] request build error: %v", err)
		time.Sleep(5 * time.Second)
		return
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := w.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		log.Printf("[worker] ReceiveMessage error: %v (retrying in 5s)", err)
		time.Sleep(5 * time.Second)
		return
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	// LocalStack returns XML by default, request JSON explicitly
	// by adding Accept header. If we still get XML, just wait and retry.
	if len(body) > 0 && body[0] == '<' {
		// Got XML — parse it simply
		msgs := w.parseXMLMessages(string(body))
		for _, msg := range msgs {
			go w.processMessage(ctx, msg.body, msg.receipt)
		}
		return
	}

	var result sqsReceiveResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return // empty response, loop back
	}

	msgs := result.ReceiveMessageResponse.ReceiveMessageResult.Messages
	if len(msgs) == 0 {
		return
	}
	log.Printf("[worker] received %d message(s)", len(msgs))
	for _, msg := range msgs {
		go w.processMessage(ctx, msg.Body, msg.ReceiptHandle)
	}
}

type xmlMsg struct {
	body    string
	receipt string
}

// parseXMLMessages extracts Body and ReceiptHandle from SQS XML responses.
func (w *SQSWorker) parseXMLMessages(xml string) []xmlMsg {
	var msgs []xmlMsg
	// Find all <Message> blocks
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

// SNSEnvelope unwraps SNS→SQS delivery.
type SNSEnvelope struct {
	Message string `json:"Message"`
}

func (w *SQSWorker) processMessage(ctx context.Context, body, receipt string) {
	// Unescape HTML entities LocalStack puts in XML responses
	body = strings.ReplaceAll(body, "&quot;", `"`)
	body = strings.ReplaceAll(body, "&amp;", "&")
	body = strings.ReplaceAll(body, "&lt;", "<")
	body = strings.ReplaceAll(body, "&gt;", ">")

	// SNS wraps your order in an outer envelope before putting it in SQS. Try to unwrap it. 
	// If unwrapping works and there's a message inside, use that as the actual order. Otherwise just use the raw body.
	var envelope SNSEnvelope
	orderJSON := body
	if err := json.Unmarshal([]byte(body), &envelope); err == nil && envelope.Message != "" {
		orderJSON = envelope.Message
	}

	// Parse the order JSON into an Order struct.
	var order Order
	if err := json.Unmarshal([]byte(orderJSON), &order); err != nil {
		log.Printf("[worker] bad message, discarding: %v", err)
		w.deleteMessage(ctx, receipt)
		return
	}

	log.Printf("[worker] processing order=%s customer=%d", order.OrderID, order.CustomerID)

	// Run the payment, same 3 second process from Phase 1. If it fails, do not delete the message. 
	// Just return. SQS will make the message visible again after 30 seconds and we'll retry automatically.
	if err := w.processor.VerifyPayment(order.OrderID, order.CustomerID); err != nil {
		log.Printf("[worker] order=%s payment FAILED: %v — will retry", order.OrderID, err)
		return
	}
	// Payment succeeded! Log it and delete the message from the queue

	log.Printf("[worker] order=%s payment verified ✓", order.OrderID)
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
		log.Printf("[worker] delete error: %v", err)
		return
	}
	resp.Body.Close()
}