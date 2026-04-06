// Load tester for the KV distributed database.
//
// Usage:
//   go run ./loadtest \
//     -leader=http://localhost:8080 \
//     -nodes=http://localhost:8080,http://localhost:8081,...  \
//     -write-ratio=0.1 \
//     -requests=2000 \
//     -workers=20 \
//     -keys=50 \
//     -out=results.csv
//
// The generator keeps a small pool of "hot keys" so that reads and writes
// to the same key cluster closely together in time, making stale reads
// likely to occur.

package main

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ---- flags -----------------------------------------------------------------

var (
	leaderAddr  = flag.String("leader", "http://localhost:8080", "Leader (or any node for leaderless) address")
	nodesRaw    = flag.String("nodes", "", "Comma-separated list of ALL nodes (for reads)")
	writeRatio  = flag.Float64("write-ratio", 0.1, "Fraction of requests that are writes (0.0–1.0)")
	totalReqs   = flag.Int("requests", 2000, "Total number of requests")
	workers     = flag.Int("workers", 20, "Concurrent workers")
	numKeys     = flag.Int("keys", 50, "Size of hot-key pool")
	outFile     = flag.String("out", "results.csv", "Output CSV file")
)

// ---- types -----------------------------------------------------------------

type result struct {
	op         string // "read" or "write"
	key        string
	latency    time.Duration
	statusCode int
	version    int64  // version returned (reads) or written (writes)
	stale      bool   // true if read returned older version than last known write
}

// keyState tracks the last written version for each key so we can detect stale reads.
type keyState struct {
	mu      sync.RWMutex
	version map[string]int64
}

func newKeyState() *keyState { return &keyState{version: make(map[string]int64)} }

func (ks *keyState) setVersion(key string, v int64) {
	ks.mu.Lock()
	defer ks.mu.Unlock()
	if v > ks.version[key] {
		ks.version[key] = v
	}
}

func (ks *keyState) getVersion(key string) int64 {
	ks.mu.RLock()
	defer ks.mu.RUnlock()
	return ks.version[key]
}

// ---- main ------------------------------------------------------------------

func main() {
	flag.Parse()

	var nodes []string
	for _, n := range strings.Split(*nodesRaw, ",") {
		n = strings.TrimSpace(n)
		if n != "" {
			nodes = append(nodes, n)
		}
	}
	if len(nodes) == 0 {
		nodes = []string{*leaderAddr}
	}

	keys := make([]string, *numKeys)
	for i := range keys {
		keys[i] = fmt.Sprintf("key%04d", i)
	}

	ks := newKeyState()
	results := make([]result, 0, *totalReqs)
	var mu sync.Mutex

	jobs := make(chan struct{}, *totalReqs)
	for i := 0; i < *totalReqs; i++ {
		jobs <- struct{}{}
	}
	close(jobs)

	var wg sync.WaitGroup
	client := &http.Client{Timeout: 10 * time.Second}

	for w := 0; w < *workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rng := rand.New(rand.NewSource(time.Now().UnixNano()))
			for range jobs {
				key := keys[rng.Intn(len(keys))]
				isWrite := rng.Float64() < *writeRatio

				var r result
				if isWrite {
					r = doWrite(client, *leaderAddr, key, ks)
				} else {
					// Pick a random node to read from (simulates load spread).
					node := nodes[rng.Intn(len(nodes))]
					r = doRead(client, node, key, ks)
				}

				mu.Lock()
				results = append(results, r)
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	writeCSV(*outFile, results)
	printSummary(results)
}

// ---- operations ------------------------------------------------------------

type GetResponse struct {
	Value   string `json:"value"`
	Version int64  `json:"version"`
}

func doWrite(client *http.Client, addr, key string, ks *keyState) result {
	val := fmt.Sprintf("val-%d", time.Now().UnixNano())
	body, _ := json.Marshal(map[string]string{"key": key, "value": val})

	start := time.Now()
	resp, err := client.Post(addr+"/set", "application/json", bytes.NewReader(body))
	lat := time.Since(start)

	if err != nil {
		return result{op: "write", key: key, latency: lat, statusCode: 0}
	}
	resp.Body.Close()

	// We don't know the exact version the leader assigned, but we bump a local
	// counter so we can detect stale reads on subsequent GETs.
	// A real implementation would have the /set endpoint return the version.
	// For now we use wall-clock nanos as a proxy (monotone per client).
	ks.setVersion(key, time.Now().UnixNano())

	return result{op: "write", key: key, latency: lat, statusCode: resp.StatusCode}
}

func doRead(client *http.Client, addr, key string, ks *keyState) result {
	start := time.Now()
	resp, err := client.Get(fmt.Sprintf("%s/get?key=%s", addr, key))
	lat := time.Since(start)

	if err != nil {
		return result{op: "read", key: key, latency: lat, statusCode: 0}
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return result{op: "read", key: key, latency: lat, statusCode: 404}
	}

	var gr GetResponse
	json.NewDecoder(resp.Body).Decode(&gr)

	lastWrite := ks.getVersion(key)
	stale := lastWrite > 0 && gr.Version < lastWrite

	return result{
		op:         "read",
		key:        key,
		latency:    lat,
		statusCode: resp.StatusCode,
		version:    gr.Version,
		stale:      stale,
	}
}

// ---- output ----------------------------------------------------------------

func writeCSV(path string, results []result) {
	f, err := os.Create(path)
	if err != nil {
		log.Printf("could not create %s: %v", path, err)
		return
	}
	defer f.Close()
	w := csv.NewWriter(f)
	w.Write([]string{"op", "key", "latency_ms", "status", "version", "stale"})
	for _, r := range results {
		w.Write([]string{
			r.op,
			r.key,
			strconv.FormatFloat(float64(r.latency.Microseconds())/1000.0, 'f', 3, 64),
			strconv.Itoa(r.statusCode),
			strconv.FormatInt(r.version, 10),
			strconv.FormatBool(r.stale),
		})
	}
	w.Flush()
	log.Printf("results written to %s", path)
}

func printSummary(results []result) {
	var reads, writes, stales int
	var rLat, wLat []float64
	for _, r := range results {
		ms := float64(r.latency.Microseconds()) / 1000.0
		if r.op == "read" {
			reads++
			rLat = append(rLat, ms)
			if r.stale {
				stales++
			}
		} else {
			writes++
			wLat = append(wLat, ms)
		}
	}
	fmt.Printf("\n=== Summary ===\n")
	fmt.Printf("Total:  %d  (reads=%d  writes=%d)\n", len(results), reads, writes)
	fmt.Printf("Stale reads: %d (%.1f%%)\n", stales, 100*float64(stales)/float64(max(reads, 1)))
	fmt.Printf("Read  avg latency: %.1f ms\n", avg(rLat))
	fmt.Printf("Write avg latency: %.1f ms\n", avg(wLat))
}

func avg(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	s := 0.0
	for _, x := range xs {
		s += x
	}
	return s / float64(len(xs))
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
