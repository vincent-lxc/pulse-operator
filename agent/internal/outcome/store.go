package outcome

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Store is an append-only JSONL of outcomes, one row per run_id.
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

// Write labels and appends. Duplicate run_id is rejected.
func (s *Store) Write(o *Outcome) error {
	label, err := Classify(o.Kind, o.PnL)
	if err != nil {
		return err
	}
	o.Label = label
	if o.RecordedAt == 0 {
		o.RecordedAt = time.Now().UTC().Unix()
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, err := s.lookupLocked(o.RunID); err != nil {
		return err
	} else if existing != nil {
		return fmt.Errorf("outcome: run_id %q already recorded", o.RunID)
	}

	f, err := os.OpenFile(s.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	enc.SetEscapeHTML(false)
	return enc.Encode(o)
}

func (s *Store) Lookup(runID string) (*Outcome, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lookupLocked(runID)
}

func (s *Store) lookupLocked(runID string) (*Outcome, error) {
	f, err := os.Open(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var o Outcome
		if err := json.Unmarshal(sc.Bytes(), &o); err != nil {
			continue
		}
		if o.RunID == runID {
			return &o, nil
		}
	}
	return nil, sc.Err()
}

// Recent returns the last n outcomes (oldest first among the window).
func (s *Store) Recent(n int) ([]Outcome, error) {
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
	var all []Outcome
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var o Outcome
		if err := json.Unmarshal(sc.Bytes(), &o); err != nil {
			continue
		}
		all = append(all, o)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if n <= 0 || n >= len(all) {
		return all, nil
	}
	return all[len(all)-n:], nil
}
