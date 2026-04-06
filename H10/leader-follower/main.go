package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"kvdb/store"
)

// ---- config ----------------------------------------------------------------

var (
	role      string   // "leader" or "follower"
	followers []string // list of follower base URLs, only used by leader
	port      string
	kv        = store.New()
	version   int64 // monotonic counter, only incremented by leader
)

// N, W, R from environment (leader only)
var (
	paramW int
	paramR int
)

// ---- main ------------------------------------------------------------------

func main() {
	role = getenv("ROLE", "follower")
	port = getenv("PORT", "8080")
	paramW, _ = strconv.Atoi(getenv("W", "5"))
	paramR, _ = strconv.Atoi(getenv("R", "1"))

	raw := getenv("FOLLOWERS", "")
	if raw != "" {
		for _, f := range strings.Split(raw, ",") {
			f = strings.TrimSpace(f)
			if f != "" {
				followers = append(followers, f)
			}
		}
	}

	http.HandleFunc("/set", handleSet)
	http.HandleFunc("/get", handleGet)
	http.HandleFunc("/internal/set", handleInternalSet) // leader → follower
	http.HandleFunc("/internal/get", handleInternalGet) // leader → follower (for R>1)
	http.HandleFunc("/local_read", handleLocalRead)     // test/debug endpoint

	log.Printf("[%s] starting on :%s  W=%d R=%d followers=%v", role, port, paramW, paramR, followers)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}

// ---- request / response types ----------------------------------------------

type SetRequest struct {
	Key     string `json:"key"`
	Value   string `json:"value"`
	Version int64  `json:"version"`
}

type GetResponse struct {
	Value   string `json:"value"`
	Version int64  `json:"version"`
}

// ---- handlers --------------------------------------------------------------

// handleSet is the public write endpoint.
// If this node is the leader it runs the replication protocol.
// Followers reject direct public writes (optional strictness).
func handleSet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	if role != "leader" {
		http.Error(w, "not the leader", http.StatusForbidden)
		return
	}

	var req struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Key == "" {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	ver := atomic.AddInt64(&version, 1)

	// Write locally first (counts as 1 write acknowledgment).
	kv.Set(req.Key, req.Value, ver)
	acks := 1

	// Replicate to followers asynchronously; collect acks up to W.
	type result struct{ ok bool }
	ch := make(chan result, len(followers))

	for _, f := range followers {
		go func(addr string) {
			ok := replicateTo(addr, req.Key, req.Value, ver)
			ch <- result{ok}
		}(f)
	}

	// Wait until we have W acks (leader counts as 1).
	needed := paramW - 1 // already have leader ack
	deadline := time.After(5 * time.Second)
	for needed > 0 {
		select {
		case res := <-ch:
			if res.ok {
				acks++
				needed--
			}
		case <-deadline:
			log.Printf("SET %q: timeout waiting for W=%d acks (got %d)", req.Key, paramW, acks)
			http.Error(w, "write quorum timeout", http.StatusServiceUnavailable)
			return
		}
	}

	w.WriteHeader(http.StatusCreated)
}

// handleGet is the public read endpoint.
func handleGet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	key := r.URL.Query().Get("key")
	if key == "" {
		http.Error(w, "missing key", http.StatusBadRequest)
		return
	}

	if role == "follower" {
		// Follower direct read path: simulate storage latency then return local value.
		time.Sleep(50 * time.Millisecond)
		entry, ok := kv.Get(key)
		if !ok {
			http.NotFound(w, r)
			return
		}
		writeJSON(w, GetResponse{Value: entry.Value, Version: entry.Version})
		return
	}

	// Leader read path: gather R responses and return the most recent.
	if paramR == 1 {
		// R=1: just return local value, no follower contact needed.
		entry, ok := kv.Get(key)
		if !ok {
			http.NotFound(w, r)
			return
		}
		writeJSON(w, GetResponse{Value: entry.Value, Version: entry.Version})
		return
	}

	// R > 1: contact followers.
	type fresult struct {
		entry store.Entry
		ok    bool
	}
	ch := make(chan fresult, len(followers))
	for _, f := range followers {
		go func(addr string) {
			e, ok := remoteGet(addr, key)
			ch <- fresult{e, ok}
		}(f)
	}

	// Seed with leader's own value.
	best, _ := kv.Get(key)
	got := 1
	needed := paramR - 1
	deadline := time.After(5 * time.Second)
	for needed > 0 {
		select {
		case res := <-ch:
			got++
			if res.ok && res.entry.Version > best.Version {
				best = res.entry
			}
			needed--
		case <-deadline:
			log.Printf("GET %q: timeout waiting for R=%d responses (got %d)", key, paramR, got)
			http.Error(w, "read quorum timeout", http.StatusServiceUnavailable)
			return
		}
	}

	if best.Version == 0 {
		http.NotFound(w, r)
		return
	}
	writeJSON(w, GetResponse{Value: best.Value, Version: best.Version})
}

// handleInternalSet is called leader→follower during replication.
func handleInternalSet(w http.ResponseWriter, r *http.Request) {
	var req SetRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	// Simulate storage write latency.
	time.Sleep(100 * time.Millisecond)
	kv.Set(req.Key, req.Value, req.Version)
	w.WriteHeader(http.StatusOK)
}

// handleInternalGet is called leader→follower when R>1.
func handleInternalGet(w http.ResponseWriter, r *http.Request) {
	key := r.URL.Query().Get("key")
	time.Sleep(50 * time.Millisecond)
	entry, ok := kv.Get(key)
	if !ok {
		http.NotFound(w, r)
		return
	}
	writeJSON(w, GetResponse{Value: entry.Value, Version: entry.Version})
}

// handleLocalRead returns the raw local value without any coordination.
// Used only in tests to observe inconsistency windows.
func handleLocalRead(w http.ResponseWriter, r *http.Request) {
	key := r.URL.Query().Get("key")
	entry, ok := kv.Get(key)
	if !ok {
		http.NotFound(w, r)
		return
	}
	writeJSON(w, GetResponse{Value: entry.Value, Version: entry.Version})
}

// ---- replication helpers ---------------------------------------------------

// replicateTo sends a replicated write to one follower.
// The leader sleeps 200ms between each follower message (per spec).
func replicateTo(addr, key, value string, ver int64) bool {
	time.Sleep(200 * time.Millisecond) // simulate inter-node write delay

	body, _ := json.Marshal(SetRequest{Key: key, Value: value, Version: ver})
	resp, err := http.Post(addr+"/internal/set", "application/json", bytes.NewReader(body))
	if err != nil {
		log.Printf("replicateTo %s: %v", addr, err)
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// remoteGet fetches a value from a follower's internal endpoint.
func remoteGet(addr, key string) (store.Entry, bool) {
	resp, err := http.Get(fmt.Sprintf("%s/internal/get?key=%s", addr, key))
	if err != nil || resp.StatusCode == http.StatusNotFound {
		return store.Entry{}, false
	}
	defer resp.Body.Close()
	var gr GetResponse
	json.NewDecoder(resp.Body).Decode(&gr)
	return store.Entry{Value: gr.Value, Version: gr.Version}, true
}

// ---- quorum read helper (used by R=3 case to pick freshest) ----------------

// bestOfN contacts n followers + self and returns freshest value.
// Already handled generically in handleGet above via paramR.
var _ = sort.Ints // suppress unused import if linter complains

// ---- utilities -------------------------------------------------------------

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// suppress unused
var _ sync.Mutex
var _ = strconv.Itoa