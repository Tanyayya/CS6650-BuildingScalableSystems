package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sns"
	snsTypes "github.com/aws/aws-sdk-go-v2/service/sns/types"
	"github.com/google/uuid"
)

// Handler holds dependencies for all HTTP endpoints.
type Handler struct {
	processor   *PaymentProcessor
	sns         *sns.Client
	snsTopicARN string
}

// NewHandler creates a Handler with the given payment processor and optional SNS client.
func NewHandler(processor *PaymentProcessor, snsClient *sns.Client, topicARN string) *Handler {
	return &Handler{
		processor:   processor,
		sns:         snsClient,
		snsTopicARN: topicARN,
	}
}

// RegisterRoutes wires all endpoints onto mux.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /health",          h.Health)
	mux.HandleFunc("POST /orders/sync",    h.SyncOrder)
	mux.HandleFunc("POST /orders/async",   h.AsyncOrder)
	mux.HandleFunc("GET /metrics/payment", h.PaymentMetrics)
}

// -----------------------------------------------------------------------
// Types
// -----------------------------------------------------------------------

type Order struct {
	OrderID    string    `json:"order_id"`
	CustomerID int       `json:"customer_id"`
	Status     string    `json:"status"`
	Items      []Item    `json:"items"`
	CreatedAt  time.Time `json:"created_at"`
}

type Item struct {
	ProductID string  `json:"product_id"`
	Name      string  `json:"name"`
	Quantity  int     `json:"quantity"`
	Price     float64 `json:"price"`
}

type OrderResponse struct {
	OrderID     string    `json:"order_id"`
	Status      string    `json:"status"`
	Message     string    `json:"message"`
	ProcessedAt time.Time `json:"processed_at,omitempty"`
	Latency     string    `json:"latency_ms,omitempty"`
}

type ErrorResponse struct {
	Error   string `json:"error"`
	Code    int    `json:"code"`
	OrderID string `json:"order_id,omitempty"`
}

// -----------------------------------------------------------------------
// Health check
// -----------------------------------------------------------------------

func (h *Handler) Health(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// -----------------------------------------------------------------------
// Phase 1: Synchronous order processing (kept for comparison)
// -----------------------------------------------------------------------

func (h *Handler) SyncOrder(w http.ResponseWriter, r *http.Request) {
	start := time.Now()

	var order Order
	if err := json.NewDecoder(r.Body).Decode(&order); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "invalid request body", Code: 400})
		return
	}

	if order.OrderID == "" {
		order.OrderID = uuid.New().String()
	}
	order.Status = "processing"
	order.CreatedAt = time.Now()

	log.Printf("[sync] order=%s customer=%d received", order.OrderID, order.CustomerID)

	if err := h.processor.VerifyPayment(order.OrderID, order.CustomerID); err != nil {
		log.Printf("[sync] order=%s payment FAILED: %v", order.OrderID, err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusPaymentRequired)
		json.NewEncoder(w).Encode(ErrorResponse{
			Error:   err.Error(),
			Code:    http.StatusPaymentRequired,
			OrderID: order.OrderID,
		})
		return
	}

	latency := time.Since(start)
	log.Printf("[sync] order=%s completed in %v", order.OrderID, latency.Round(time.Millisecond))

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(OrderResponse{
		OrderID:     order.OrderID,
		Status:      "completed",
		Message:     "Order processed and payment verified",
		ProcessedAt: time.Now(),
		Latency:     latency.Round(time.Millisecond).String(),
	})
}

// -----------------------------------------------------------------------
// Phase 2: Async order processing via SNS → SQS
// -----------------------------------------------------------------------

// AsyncOrder accepts the order immediately and publishes it to SNS.
//
// Flow:  POST /orders/async
//          → publish to SNS (~5ms)
//          → return 202 Accepted  ← customer gets this immediately
//
//                     ↓ decoupled
//          SQS queue buffers the spike
//                     ↓
//          Worker polls SQS → VerifyPayment (3s) — nobody is waiting
//
// Result: 100% acceptance rate under any load. Queue grows during the
// flash sale spike and drains at 1.67 orders/sec afterward.
func (h *Handler) AsyncOrder(w http.ResponseWriter, r *http.Request) {
	start := time.Now()

	var order Order
	if err := json.NewDecoder(r.Body).Decode(&order); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "invalid request body", Code: 400})
		return
	}

	if order.OrderID == "" {
		order.OrderID = uuid.New().String()
	}
	order.Status = "pending"
	order.CreatedAt = time.Now()

	log.Printf("[async] order=%s customer=%d publishing to SNS", order.OrderID, order.CustomerID)

	msgBody, err := json.Marshal(order)
	if err != nil {
		log.Printf("[async] order=%s marshal failed: %v", order.OrderID, err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	// Publish to SNS — fans out to all subscribed SQS queues.
	_, err = h.sns.Publish(context.Background(), &sns.PublishInput{
		TopicArn: aws.String(h.snsTopicARN),
		Message:  aws.String(string(msgBody)),
		MessageAttributes: map[string]snsTypes.MessageAttributeValue{
			"event_type": {
				DataType:    aws.String("String"),
				StringValue: aws.String("order.created"),
			},
		},
	})
	if err != nil {
		log.Printf("[async] order=%s SNS publish FAILED: %v", order.OrderID, err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(ErrorResponse{
			Error:   "failed to queue order",
			Code:    500,
			OrderID: order.OrderID,
		})
		return
	}

	latency := time.Since(start)
	log.Printf("[async] order=%s queued in %v", order.OrderID, latency.Round(time.Millisecond))

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted) // 202 — not yet processed, but guaranteed
	json.NewEncoder(w).Encode(OrderResponse{
		OrderID: order.OrderID,
		Status:  "pending",
		Message: "Order accepted and queued for processing",
		Latency: latency.Round(time.Millisecond).String(),
	})
}

// -----------------------------------------------------------------------
// Observability
// -----------------------------------------------------------------------

func (h *Handler) PaymentMetrics(w http.ResponseWriter, r *http.Request) {
	m := h.processor.Metrics()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"active_slots":     m.Active,
		"max_slots":        MaxConcurrentPayments,
		"total_processed":  m.Processed,
		"total_failed":     m.Failed,
		"slot_utilization": float64(m.Active) / float64(MaxConcurrentPayments),
	})
}
