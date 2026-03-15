package main

import (
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/google/uuid"
)

// Handler holds dependencies for all HTTP endpoints.
type Handler struct {
	processor *PaymentProcessor
}

// NewHandler creates a Handler with the given payment processor.
func NewHandler(processor *PaymentProcessor) *Handler {
	return &Handler{processor: processor}
}

// RegisterRoutes wires all endpoints onto mux.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /health", h.Health)
	mux.HandleFunc("POST /orders/sync", h.SyncOrder)
	mux.HandleFunc("GET /metrics/payment", h.PaymentMetrics)
}

// -----------------------------------------------------------------------
// Types (kept here since we're a flat single-package project)
// -----------------------------------------------------------------------

type Order struct {
	OrderID    string    `json:"order_id"`
	CustomerID int       `json:"customer_id"`
	Status     string    `json:"status"` // pending, processing, completed, failed
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
// Phase 1: Synchronous order processing
// -----------------------------------------------------------------------

// SyncOrder is the Phase 1 endpoint.
//
// Flow:  POST /orders/sync
//
//	→ decode order
//	→ VerifyPayment (BLOCKS for ~3s, hard cap of 5 concurrent)
//	→ return 200 OK
//
// The problem: HTTP connections are held open the entire time.
// Under flash-sale load (20+ users) goroutines pile up waiting for a
// payment slot. Customers see multi-second hangs then hard timeouts.
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

	// *** THE BOTTLENECK ***
	// Blocks until a payment slot is free AND the 3s verification completes.
	// With 5 slots and 20+ req/s incoming, goroutines queue here indefinitely.
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
