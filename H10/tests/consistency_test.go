package tests

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

var (
	leaderURL  string
	followerURLs  []string
	leaderlessURLs []string
)

func init() {
	leaderURL = getenv("LEADER", "http://localhost:8080")
	for _, f := range strings.Split(getenv("FOLLOWERS", ""), ",") {
		if f = strings.TrimSpace(f); f != "" {
			followerURLs = append(followerURLs, f)
		}
	}
	for _, n := range strings.Split(getenv("LEADERLESS", ""), ",") {
		if n = strings.TrimSpace(n); n != "" {
			leaderlessURLs = append(leaderlessURLs, n)
		}
	}
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

type getResp struct {
	Value   string `json:"value"`
	Version int64  `json:"version"`
}

func kvSet(base, key, value string) int {
	body, _ := json.Marshal(map[string]string{"key": key, "value": value})
	resp, err := http.Post(base+"/set", "application/json", bytes.NewReader(body))
	if err != nil {
		return 0
	}
	resp.Body.Close()
	return resp.StatusCode
}

func kvGet(base, key string) (getResp, int) {
	resp, err := http.Get(fmt.Sprintf("%s/get?key=%s", base, key))
	if err != nil {
		return getResp{}, 0
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return getResp{}, resp.StatusCode
	}
	var r getResp
	json.NewDecoder(resp.Body).Decode(&r)
	return r, http.StatusOK
}

func kvLocalRead(base, key string) (getResp, int) {
	resp, err := http.Get(fmt.Sprintf("%s/local_read?key=%s", base, key))
	if err != nil {
		return getResp{}, 0
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return getResp{}, resp.StatusCode
	}
	var r getResp
	json.NewDecoder(resp.Body).Decode(&r)
	return r, http.StatusOK
}

// Test 1: read from leader after leader ack must be consistent.
func TestLeaderReadAfterWrite(t *testing.T) {
	key, val := "lf-leader-read", "hello"
	if code := kvSet(leaderURL, key, val); code != http.StatusCreated {
		t.Fatalf("set returned %d", code)
	}
	r, code := kvGet(leaderURL, key)
	if code != http.StatusOK {
		t.Fatalf("get returned %d", code)
	}
	if r.Value != val {
		t.Errorf("want %q got %q", val, r.Value)
	}
}

// Test 2: read from every follower after leader ack must be consistent (W=5).
func TestFollowerReadAfterWrite(t *testing.T) {
	if len(followerURLs) == 0 {
		t.Skip("no FOLLOWERS configured")
	}
	key, val := "lf-follower-read", "world"
	if code := kvSet(leaderURL, key, val); code != http.StatusCreated {
		t.Fatalf("set returned %d", code)
	}
	for i, f := range followerURLs {
		r, code := kvGet(f, key)
		if code != http.StatusOK {
			t.Errorf("follower %d: get returned %d", i, code)
			continue
		}
		if r.Value != val {
			t.Errorf("follower %d: want %q got %q", i, val, r.Value)
		}
	}
}

// Test 3: local_read on followers during replication window should expose staleness.
func TestInconsistencyWindowLeaderFollower(t *testing.T) {
	if len(followerURLs) == 0 {
		t.Skip("no FOLLOWERS configured")
	}
	stale := 0
	for i := 0; i < 20; i++ {
		key := fmt.Sprintf("lf-incon-%d", i)
		val := fmt.Sprintf("v%d", i)

		done := make(chan int, 1)
		go func() { done <- kvSet(leaderURL, key, val) }()

		time.Sleep(10 * time.Millisecond)
		for _, f := range followerURLs {
			r, code := kvLocalRead(f, key)
			if code == http.StatusNotFound || (code == http.StatusOK && r.Value != val) {
				stale++
			}
		}
		<-done
	}
	t.Logf("stale local_reads observed: %d / %d", stale, 20*len(followerURLs))
}

// Test 4: read from coordinator after ack must be consistent.
func TestLeaderlessCoordinatorRead(t *testing.T) {
	if len(leaderlessURLs) == 0 {
		t.Skip("no LEADERLESS configured")
	}
	coord := leaderlessURLs[0]
	key, val := "ll-coord-read", "consistent"
	if code := kvSet(coord, key, val); code != http.StatusCreated {
		t.Fatalf("set returned %d", code)
	}
	r, code := kvGet(coord, key)
	if code != http.StatusOK {
		t.Fatalf("get returned %d", code)
	}
	if r.Value != val {
		t.Errorf("want %q got %q", val, r.Value)
	}
}

// Test 5: after coordinator ack (W=N) all nodes must be consistent.
func TestLeaderlessAllNodesConsistent(t *testing.T) {
	if len(leaderlessURLs) < 2 {
		t.Skip("need at least 2 LEADERLESS nodes")
	}
	coord := leaderlessURLs[0]
	key, val := "ll-all-nodes", "propagated"
	if code := kvSet(coord, key, val); code != http.StatusCreated {
		t.Fatalf("set returned %d", code)
	}
	for i, n := range leaderlessURLs[1:] {
		r, code := kvGet(n, key)
		if code != http.StatusOK {
			t.Errorf("node %d: get returned %d", i+1, code)
			continue
		}
		if r.Value != val {
			t.Errorf("node %d: want %q got %q", i+1, val, r.Value)
		}
	}
}

// Test 6: reads to non-coordinator nodes during replication window expose staleness.
func TestLeaderlessInconsistencyWindow(t *testing.T) {
	if len(leaderlessURLs) < 2 {
		t.Skip("need at least 2 LEADERLESS nodes")
	}
	stale := 0
	for i := 0; i < 20; i++ {
		key := fmt.Sprintf("ll-incon-%d", i)
		val := fmt.Sprintf("v%d", i)
		coord := leaderlessURLs[i%len(leaderlessURLs)]

		done := make(chan int, 1)
		go func() { done <- kvSet(coord, key, val) }()

		time.Sleep(50 * time.Millisecond)
		for _, n := range leaderlessURLs {
			if n == coord {
				continue
			}
			r, code := kvLocalRead(n, key)
			if code == http.StatusNotFound || (code == http.StatusOK && r.Value != val) {
				stale++
			}
		}
		<-done
	}
	t.Logf("stale reads in leaderless window: %d", stale)
}