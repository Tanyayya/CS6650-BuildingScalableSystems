package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Product struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	Category    string `json:"category"`
	Description string `json:"description"`
	Brand       string `json:"brand"`
}

type SearchResponse struct {
	Products        []Product `json:"products"`
	TotalFound      int       `json:"total_found"`
	SearchTime      string    `json:"search_time"`
	ItemsChecked    int       `json:"items_checked"`
	Recommendations []string  `json:"recommendations"`
	RecSource       string    `json:"rec_source"` // "live" or "timeout"
}

type APIError struct {
	Error   string `json:"error"`
	Message string `json:"message"`
	Details string `json:"details,omitempty"`
}

type Store struct {
	m   sync.Map
	ids []int
}

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

func (s *Store) Get(id int) (Product, bool) {
	v, ok := s.m.Load(id)
	if !ok {
		return Product{}, false
	}
	return v.(Product), true
}

func (s *Store) Search(query string, checkLimit, maxResults int) (matches []Product, totalFound, checked int) {
	q := strings.ToLower(query)
	matches = make([]Product, 0, maxResults)
	for _, id := range s.ids {
		if checked >= checkLimit {
			break
		}
		checked++
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

// Same slow service — still broken
func slowRecommendationService(category string) []string {
	delay := time.Duration(10000+rand.Intn(20000)) * time.Millisecond
	time.Sleep(delay)
	return []string{
		"Recommended: " + category + " item A",
		"Recommended: " + category + " item B",
	}
}

// ─────────────────────────────────────────
// FIX 1: FAIL FAST
// Hard 200ms timeout on every call.
// If service doesn't respond → skip it → return instantly.
// Goroutines never hang longer than 200ms.
// ─────────────────────────────────────────
func getRecsFailFast(category string) ([]string, string) {
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	type result struct{ recs []string }
	ch := make(chan result, 1)

	go func() {
		ch <- result{slowRecommendationService(category)}
	}()

	select {
	case r := <-ch:
		return r.recs, "live"
	case <-ctx.Done():
		log.Printf("⚡ Fail fast: timeout for category=%s", category)
		return []string{}, "timeout"
	}
}

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
	products, totalFound, checked := store.Search(query, 100, 20)
	var recs []string
	var recSource string
	if len(products) > 0 {
		recs, recSource = getRecsFailFast(products[0].Category)
	} else {
		recSource = "no_products"
	}
	elapsed := time.Since(start)
	writeJSON(w, http.StatusOK, SearchResponse{
		Products:        products,
		TotalFound:      totalFound,
		SearchTime:      elapsed.String(),
		ItemsChecked:    checked,
		Recommendations: recs,
		RecSource:       recSource,
	})
}

func handleGetProduct(w http.ResponseWriter, r *http.Request, store *Store, rawID string) {
	id, err := parseProductID(rawID)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, APIError{Error: "INVALID_INPUT", Message: "The provided input data is invalid", Details: err.Error()})
		return
	}
	p, ok := store.Get(id)
	if !ok {
		writeJSON(w, http.StatusNotFound, APIError{Error: "NOT_FOUND", Message: "Product not found", Details: fmt.Sprintf("No product exists with id=%d", id)})
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"status": "ok",
		"fix":    "fail-fast-200ms",
	})
}

func main() {
	store := &Store{}
	log.Println("Seeding 100,000 products...")
	start := time.Now()
	store.seed()
	log.Printf("Seeded %d products in %s", len(store.ids), time.Since(start))
	mux := http.NewServeMux()
	mux.HandleFunc("/products/search", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, APIError{Error: "METHOD_NOT_ALLOWED", Message: "Method not allowed"})
			return
		}
		handleSearch(w, r, store)
	})
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, APIError{Error: "METHOD_NOT_ALLOWED", Message: "Method not allowed"})
			return
		}
		handleHealth(w, r)
	})
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
	log.Println("✅ FIX 1: FAIL FAST — 200ms timeout on recommendation service")
	log.Fatal(http.ListenAndServe(":8080", mux))
}
