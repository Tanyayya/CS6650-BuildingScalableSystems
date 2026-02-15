package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
)

type Product struct {
	ProductID    int32  `json:"product_id"`
	SKU          string `json:"sku"`
	Manufacturer string `json:"manufacturer"`
	CategoryID   int32  `json:"category_id"`
	Weight       int32  `json:"weight"`
	SomeOtherID  int32  `json:"some_other_id"`
}

type APIError struct {
	Error   string `json:"error"`
	Message string `json:"message"`
	Details string `json:"details,omitempty"`
}

type Store struct {
	mu       sync.RWMutex
	products map[int32]Product
}

func NewStore() *Store {
	s := &Store{products: make(map[int32]Product)}
	// Seed: needed because spec has no "create product" endpoint but POST /details expects 404 if missing
	s.products[1] = Product{
		ProductID:    1,
		SKU:          "ABC-123-XYZ",
		Manufacturer: "Acme Corporation",
		CategoryID:   456,
		Weight:       1250,
		SomeOtherID:  789,
	}
	s.products[2] = Product{
		ProductID:    2,
		SKU:          "SKU-002",
		Manufacturer: "Globex",
		CategoryID:   10,
		Weight:       0,
		SomeOtherID:  999,
	}
	return s
}

func (s *Store) Get(id int32) (Product, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.products[id]
	return p, ok
}

func (s *Store) Upsert(p Product) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.products[p.ProductID] = p
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func parseProductID(raw string) (int32, error) {
	// OpenAPI: int32, minimum 1
	n64, err := strconv.ParseInt(raw, 10, 32)
	if err != nil {
		return 0, errors.New("productId must be an integer")
	}
	if n64 < 1 {
		return 0, errors.New("productId must be a positive integer")
	}
	return int32(n64), nil
}

func validateProduct(p Product) error {
	// Required fields in schema:
	// product_id, sku, manufacturer, category_id, weight, some_other_id
	if p.ProductID < 1 {
		return errors.New("product_id must be a positive integer")
	}
	if strings.TrimSpace(p.SKU) == "" {
		return errors.New("sku must be a non-empty string")
	}
	if len(p.SKU) > 100 {
		return errors.New("sku must be at most 100 characters")
	}
	if strings.TrimSpace(p.Manufacturer) == "" {
		return errors.New("manufacturer must be a non-empty string")
	}
	if len(p.Manufacturer) > 200 {
		return errors.New("manufacturer must be at most 200 characters")
	}
	if p.CategoryID < 1 {
		return errors.New("category_id must be a positive integer")
	}
	if p.Weight < 0 {
		return errors.New("weight must be >= 0")
	}
	if p.SomeOtherID < 1 {
		return errors.New("some_other_id must be a positive integer")
	}
	return nil
}

// Routes we need:
// GET  /v1/products/{productId}
// POST /v1/products/{productId}/details
//
// We'll do a tiny router using ServeMux + path parsing.
func main() {
	store := NewStore()

	mux := http.NewServeMux()

	mux.HandleFunc("/v1/products/", func(w http.ResponseWriter, r *http.Request) {
		// Expected patterns:
		// /v1/products/{id}
		// /v1/products/{id}/details
		path := strings.TrimPrefix(r.URL.Path, "/v1/products/")
		path = strings.Trim(path, "/")
		if path == "" {
			// Not in spec; return 404
			writeJSON(w, http.StatusNotFound, APIError{Error: "NOT_FOUND", Message: "Not found"})
			return
		}

		parts := strings.Split(path, "/")
		if len(parts) == 1 && r.Method == http.MethodGet {
			handleGetProduct(w, r, store, parts[0])
			return
		}

		if len(parts) == 2 && parts[1] == "details" && r.Method == http.MethodPost {
			handlePostProductDetails(w, r, store, parts[0])
			return
		}

		// Method/path not defined in spec
		writeJSON(w, http.StatusNotFound, APIError{Error: "NOT_FOUND", Message: "Not found"})
	})

	addr := ":8080"
	log.Printf("Listening on http://localhost%s/v1", addr)
	log.Fatal(http.ListenAndServe(addr, mux))
}

func handleGetProduct(w http.ResponseWriter, r *http.Request, store *Store, rawID string) {
	id, err := parseProductID(rawID)
	if err != nil {
		// Spec doesn't list 400 for GET, but we should validate input.
		// Returning 400 is reasonable for invalid path parameter.
		writeJSON(w, http.StatusBadRequest, APIError{
			Error:   "INVALID_INPUT",
			Message: "The provided input data is invalid",
			Details: err.Error(),
		})
		return
	}

	p, ok := store.Get(id)
	if !ok {
		writeJSON(w, http.StatusNotFound, APIError{
			Error:   "NOT_FOUND",
			Message: "Product not found",
			Details: fmt.Sprintf("No product exists with product_id=%d", id),
		})
		return
	}

	writeJSON(w, http.StatusOK, p)
}

func handlePostProductDetails(w http.ResponseWriter, r *http.Request, store *Store, rawID string) {
	id, err := parseProductID(rawID)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, APIError{
			Error:   "INVALID_INPUT",
			Message: "The provided input data is invalid",
			Details: err.Error(),
		})
		return
	}

	// Must exist, otherwise 404 per spec
	if _, ok := store.Get(id); !ok {
		writeJSON(w, http.StatusNotFound, APIError{
			Error:   "NOT_FOUND",
			Message: "Product not found",
			Details: fmt.Sprintf("No product exists with product_id=%d", id),
		})
		return
	}

	var p Product
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		writeJSON(w, http.StatusBadRequest, APIError{
			Error:   "INVALID_INPUT",
			Message: "The provided input data is invalid",
			Details: "Request body must be valid JSON matching Product schema",
		})
		return
	}
	// Ensure no trailing JSON tokens
	if dec.More() {
		writeJSON(w, http.StatusBadRequest, APIError{
			Error:   "INVALID_INPUT",
			Message: "The provided input data is invalid",
			Details: "Request body must contain a single JSON object",
		})
		return
	}

	if err := validateProduct(p); err != nil {
		writeJSON(w, http.StatusBadRequest, APIError{
			Error:   "INVALID_INPUT",
			Message: "The provided input data is invalid",
			Details: err.Error(),
		})
		return
	}

	// Contract sanity: body.product_id should match path productId
	if p.ProductID != id {
		writeJSON(w, http.StatusBadRequest, APIError{
			Error:   "INVALID_INPUT",
			Message: "The provided input data is invalid",
			Details: "Body product_id must match path productId",
		})
		return
	}

	store.Upsert(p)
	w.WriteHeader(http.StatusNoContent) // 204 no body
}
