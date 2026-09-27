package audit

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// Store is an append-only JSONL log. Writes are O_APPEND; a run_id may appear once.
type Store struct {
	path string
	mu   sync.Mutex
}

func NewStore(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	return &Store{path: path}, nil
}

func (s *Store) Path() string { return s.path }

// Append seals the record (if needed) and writes one JSON line.
func (s *Store) Append(r *Record) error {
	if r.DecisionHash == "" {
		if err := r.Seal(); err != nil {
			return err
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	seen, err := s.hasRunIDLocked(r.RunID)
	if err != nil {
		return err
	}
	if seen {
		return fmt.Errorf("audit: run_id %q already recorded", r.RunID)
	}

	f, err := os.OpenFile(s.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()

	enc := json.NewEncoder(f)
	enc.SetEscapeHTML(false)
	return enc.Encode(r)
}

func (s *Store) hasRunIDLocked(runID string) (bool, error) {
	f, err := os.Open(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		var rec Record
		if err := json.Unmarshal(sc.Bytes(), &rec); err != nil {
			continue
		}
		if rec.RunID == runID {
			return true, nil
		}
	}
	return false, sc.Err()
}

// Lookup returns the record for run_id, or nil.
func (s *Store) Lookup(runID string) (*Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := os.Open(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		var rec Record
		if err := json.Unmarshal(sc.Bytes(), &rec); err != nil {
			continue
		}
		if rec.RunID == runID {
			return &rec, nil
		}
	}
	return nil, sc.Err()
}
