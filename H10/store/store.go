package store

import "sync"

// Entry holds a value and a logical version number.
type Entry struct {
	Value   string `json:"value"`
	Version int64  `json:"version"`
}

// Store is a thread-safe in-memory key-value store.
type Store struct {
	mu   sync.RWMutex
	data map[string]Entry
}

func New() *Store {
	return &Store{data: make(map[string]Entry)}
}

// Set stores a value. If the incoming version is higher than what we have,
// or the key doesn't exist, we accept the write. Returns false if rejected.
func (s *Store) Set(key, value string, version int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	existing, ok := s.data[key]
	if ok && existing.Version >= version {
		return false // stale write, reject
	}
	s.data[key] = Entry{Value: value, Version: version}
	return true
}

// Get retrieves a value and its version. Returns ("", 0, false) if not found.
func (s *Store) Get(key string) (Entry, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	e, ok := s.data[key]
	return e, ok
}
