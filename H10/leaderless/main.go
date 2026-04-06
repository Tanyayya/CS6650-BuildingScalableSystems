package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"kvdb/store"
)

// ---- config ----------------------------------------------------------------

var (
	peers   []string // all OTHER nodes in the cluster
	port    string
	kv      = store.New()
	version int64 // per-node logical clock; coordinator picks max+1
)

func main() {
	port = getenv("PORT", "8080")
	raw := getenv("PEERS", "")
	for _, p := range strings.Split(raw, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			peers = append(peers, p)
		}
	}

	http.HandleFunc("/set", handleSet)
	http.HandleFunc("/get", handleGet)
	http.HandleFunc("/internal/set", handleInternalSet)
	http.HandleFunc("/local_read", handleLocalRead)

	log.Printf("[leaderless] starting on :%s  peers=%v", port, peers)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}

// ---- types -----------------------------------------------------------------

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

// handleSet: this node becomes the Write Coordinator.
// W=N: must propagate to ALL peers and wait for all acks before responding.
func handleSet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
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
	kv.Set(req.Key, req.Value, ver)

	// Propagate to all peers (W=N).
	type result struct{ ok bool }
	ch := make(chan result, len(peers))
	for _, p := range peers {
		go func(addr string) {
			ok := replicateTo(addr, req.Key, req.Value, ver)
			ch <- result{ok}
		}(p)
	}

	deadline := time.After(5 * time.Second)
	acks := 1 // self
	failures := 0
	for range peers {
		select {
		case res := <-ch:
			if res.ok {
				acks++
			} else {
				failures++
			}
		case <-deadline:
			log.Printf("SET %q: timeout – only got %d/%d acks", req.Key, acks, len(peers)+1)
			http.Error(w, "write quorum timeout", http.StatusServiceUnavailable)
			return
		}
	}
	if failures > 0 {
		log.Printf("SET %q: %d peer(s) failed to ack", req.Key, failures)
	}
	w.WriteHeader(http.StatusCreated)
}

// handleGet: R=1, just return local value.
// (The inconsistency window is visible here before a write propagates.)
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
	entry, ok := kv.Get(key)
	if !ok {
		http.NotFound(w, r)
		return
	}
	writeJSON(w, GetResponse{Value: entry.Value, Version: entry.Version})
}

// handleInternalSet receives a replicated write from the Write Coordinator.
func handleInternalSet(w http.ResponseWriter, r *http.Request) {
	var req SetRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	time.Sleep(100 * time.Millisecond) // simulate write latency
	kv.Set(req.Key, req.Value, req.Version)
	w.WriteHeader(http.StatusOK)
}

// handleLocalRead: returns raw local value (test/debug only).
func handleLocalRead(w http.ResponseWriter, r *http.Request) {
	key := r.URL.Query().Get("key")
	entry, ok := kv.Get(key)
	if !ok {
		http.NotFound(w, r)
		return
	}
	writeJSON(w, GetResponse{Value: entry.Value, Version: entry.Version})
}

// ---- helpers ---------------------------------------------------------------

func replicateTo(addr, key, value string, ver int64) bool {
	time.Sleep(200 * time.Millisecond) // simulate inter-node write latency
	body, _ := json.Marshal(SetRequest{Key: key, Value: value, Version: ver})
	resp, err := http.Post(addr+"/internal/set", "application/json", bytes.NewReader(body))
	if err != nil {
		log.Printf("replicateTo %s: %v", addr, err)
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

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

var _ = fmt.Sprintf // suppress unused