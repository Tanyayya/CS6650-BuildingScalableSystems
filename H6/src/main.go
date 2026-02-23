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
	"time"
)

// ─────────────────────────────────────────
// Domain types
// ─────────────────────────────────────────

// Product is the searchable product structure.
type Product struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	Category    string `json:"category"`
	Description string `json:"description"`
	Brand       string `json:"brand"`
}

// SearchResponse is returned by GET /products/search.
type SearchResponse struct {
	Products     []Product `json:"products"`
	TotalFound   int       `json:"total_found"`
	SearchTime   string    `json:"search_time"`
	ItemsChecked int       `json:"items_checked"`
}

type APIError struct {
	Error   string `json:"error"`
	Message string `json:"message"`
	Details string `json:"details,omitempty"`
}

// ─────────────────────────────────────────
// Store — sync.Map for lock-free reads
// ─────────────────────────────────────────

// Store wraps sync.Map and keeps an ordered slice of IDs so bounded
// iteration is deterministic (sync.Map.Range order is not guaranteed).
type Store struct {
	m   sync.Map // key: int (1-based ID) → Product
	ids []int    // insertion-order IDs, read-only after seed()
}

// seed generates exactly 100,000 products at startup.
func (s *Store) seed() {
	brands := []string{"Alpha", "Beta", "Gamma", "Delta", "Epsilon", "Zeta", "Eta", "Theta"}
	categories := []string{"Electronics", "Books", "Home", "Sports", "Clothing", "Toys", "Garden", "Automotive"}
	descriptions := []string{
		"High quality product for everyday use",
		"Premium grade item with extended warranty",
		"Budget-friendly option without compromising quality",
		"Professional-grade equipment for serious users",
		"Eco-friendly and sustainably sourced materials",
	}

	const total = 100_000
	s.ids = make([]int, 0, total)

	for i := 1; i <= total; i++ {
		brand := brands[(i-1)%len(brands)]
		p := Product{
			ID:          i,
			Name:        fmt.Sprintf("Product %s %d", brand, i),
			Category:    categories[(i-1)%len(categories)],
			Description: descriptions[(i-1)%len(descriptions)],
			Brand:       brand,
		}
		s.m.Store(i, p)
		s.ids = append(s.ids, i)
	}
}

// Get returns a single product by ID.
func (s *Store) Get(id int) (Product, bool) {
	v, ok := s.m.Load(id)
	if !ok {
		return Product{}, false
	}
	return v.(Product), true
}

// Search checks exactly checkLimit products (bounded iteration) and returns
// up to maxResults matches. Searches Name and Category case-insensitively.
// The counter increments for EVERY product examined, not just matches.
func (s *Store) Search(query string, checkLimit, maxResults int) (matches []Product, totalFound, checked int) {
	q := strings.ToLower(query)
	matches = make([]Product, 0, maxResults)

	for _, id := range s.ids {
		if checked >= checkLimit {
			break
		}
		checked++ // increment for EVERY product examined

		v, ok := s.m.Load(id)
		if !ok {
			continue
		}
		p := v.(Product)

		if strings.Contains(strings.ToLower(p.Name), q) ||
			strings.Contains(strings.ToLower(p.Category), q) {
			totalFound++
			if len(matches) < maxResults {
				matches = append(matches, p)
			}
		}
	}
	return
}

// ─────────────────────────────────────────
// HTTP helpers
// ─────────────────────────────────────────

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func parseProductID(raw string) (int, error) {
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, errors.New("productId must be an integer")
	}
	if n < 1 {
		return 0, errors.New("productId must be a positive integer")
	}
	return n, nil
}

// ─────────────────────────────────────────
// Handlers
// ─────────────────────────────────────────

// GET /products/search?q={query}
func handleSearch(w http.ResponseWriter, r *http.Request, store *Store) {
	query := r.URL.Query().Get("q")
	if strings.TrimSpace(query) == "" {
		writeJSON(w, http.StatusBadRequest, APIError{
			Error:   "INVALID_INPUT",
			Message: "The provided input data is invalid",
			Details: "query parameter 'q' is required",
		})
		return
	}

	start := time.Now()
	// Bounded: check exactly 100 products, return at most 20 results.
	products, totalFound, checked := store.Search(query, 100, 20)
	elapsed := time.Since(start)

	writeJSON(w, http.StatusOK, SearchResponse{
		Products:     products,
		TotalFound:   totalFound,
		SearchTime:   elapsed.String(),
		ItemsChecked: checked,
	})
}

// GET /v1/products/{productId}
func handleGetProduct(w http.ResponseWriter, r *http.Request, store *Store, rawID string) {
	id, err := parseProductID(rawID)
	if err != nil {
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
			Details: fmt.Sprintf("No product exists with id=%d", id),
		})
		return
	}

	writeJSON(w, http.StatusOK, p)
}

// GET /health
func handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// ─────────────────────────────────────────
// Main
// ─────────────────────────────────────────

func main() {
	store := &Store{}

	log.Println("Seeding 100,000 products...")
	start := time.Now()
	store.seed()
	log.Printf("Seeded %d products in %s", len(store.ids), time.Since(start))

	mux := http.NewServeMux()

	// Search endpoint
	mux.HandleFunc("/products/search", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, APIError{Error: "METHOD_NOT_ALLOWED", Message: "Method not allowed"})
			return
		}
		handleSearch(w, r, store)
	})

	// Health check
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, APIError{Error: "METHOD_NOT_ALLOWED", Message: "Method not allowed"})
			return
		}
		handleHealth(w, r)
	})

	// Single-product lookup (kept from original spec)
	mux.HandleFunc("/v1/products/", func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/v1/products/")
		path = strings.Trim(path, "/")
		if path == "" || strings.Contains(path, "/") {
			writeJSON(w, http.StatusNotFound, APIError{Error: "NOT_FOUND", Message: "Not found"})
			return
		}
		if r.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, APIError{Error: "METHOD_NOT_ALLOWED", Message: "Method not allowed"})
			return
		}
		handleGetProduct(w, r, store, path)
	})

	addr := ":8080"
	log.Printf("Listening on http://localhost%s", addr)
	log.Printf("Endpoints:")
	log.Printf("  GET /products/search?q={query}")
	log.Printf("  GET /v1/products/{id}")
	log.Printf("  GET /health")
	log.Fatal(http.ListenAndServe(addr, mux))
}
