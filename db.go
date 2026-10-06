package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Mapping holds the data stored for each UUID reply token.
type Mapping struct {
	UUID        string `json:"uuid"`
	SenderEmail string `json:"sender_email"`
	SenderName  string `json:"sender_name"`
	Subject     string `json:"subject"`
	MessageID   string `json:"message_id"` // original Message-ID for In-Reply-To threading
}

type storeData struct {
	Mappings       map[string]*Mapping `json:"mappings"`
	PostmarkCounts map[string]int      `json:"postmark_counts"` // key: "2024-01"
}

// Store is a thread-safe, JSON-backed persistence store.
// At the user's expected volume (< 200 emails/month) this is perfectly adequate.
type Store struct {
	mu   sync.RWMutex
	path string
	data storeData
}

// OpenStore loads (or creates) the JSON store at the given path.
func OpenStore(path string) (*Store, error) {
	s := &Store{
		path: path,
		data: storeData{
			Mappings:       make(map[string]*Mapping),
			PostmarkCounts: make(map[string]int),
		},
	}

	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return s, nil // fresh start
	}
	if err != nil {
		return nil, fmt.Errorf("open store: %w", err)
	}
	defer f.Close()

	if err := json.NewDecoder(f).Decode(&s.data); err != nil {
		return nil, fmt.Errorf("decode store: %w", err)
	}
	if s.data.Mappings == nil {
		s.data.Mappings = make(map[string]*Mapping)
	}
	if s.data.PostmarkCounts == nil {
		s.data.PostmarkCounts = make(map[string]int)
	}
	return s, nil
}

// save persists the store to disk atomically (write temp → rename).
// Caller must hold s.mu.Lock().
func (s *Store) save() error {
	dir := filepath.Dir(s.path)
	tmp, err := os.CreateTemp(dir, ".mailbridge-*.tmp")
	if err != nil {
		return err
	}
	if err := json.NewEncoder(tmp).Encode(s.data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	tmp.Close()
	return os.Rename(tmp.Name(), s.path)
}

// SaveMapping stores a UUID → sender mapping.
func (s *Store) SaveMapping(uuid, senderEmail, senderName, subject, messageID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.Mappings[uuid] = &Mapping{
		UUID:        uuid,
		SenderEmail: senderEmail,
		SenderName:  senderName,
		Subject:     subject,
		MessageID:   messageID,
	}
	return s.save()
}

// LookupMapping retrieves a mapping by UUID.
func (s *Store) LookupMapping(uuid string) (*Mapping, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	m, ok := s.data.Mappings[uuid]
	if !ok {
		return nil, fmt.Errorf("uuid not found: %s", uuid)
	}
	return m, nil
}

// --- Postmark quota ---

func currentMonth() string {
	return time.Now().Format("2006-01")
}

// PostmarkMonthCount returns how many emails were sent via Postmark this month.
func (s *Store) PostmarkMonthCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.data.PostmarkCounts[currentMonth()]
}

// IncrPostmarkCount increments the Postmark counter for the current month.
func (s *Store) IncrPostmarkCount() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.PostmarkCounts[currentMonth()]++
	return s.save()
}

// Close is a no-op (data is flushed on every write).
func (s *Store) Close() error { return nil }
