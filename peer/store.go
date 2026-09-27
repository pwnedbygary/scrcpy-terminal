package peer

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Record is one paired target as this controller remembers it.
type Record struct {
	Fingerprint string   `json:"fingerprint"`
	Name        string   `json:"name"`
	Host        string   `json:"host"`
	Port        int      `json:"port"`
	Grants      []string `json:"grants"`
	PairedAtMs  int64    `json:"pairedAtMs"`
}

// ID is the record's parsed fingerprint.
func (r Record) ID() (Fingerprint, error) { return ParseFingerprint(r.Fingerprint) }

// Store is the list of paired targets: one JSON file, replaced atomically.
type Store struct {
	path string
	mu   sync.Mutex
}

// OpenStore uses dir/peers.json; the file appears on the first Put.
func OpenStore(dir string) *Store { return &Store{path: filepath.Join(dir, "peers.json")} }

func (s *Store) All() ([]Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.load()
}

// Put adds a record or replaces the one with the same fingerprint.
func (s *Store) Put(r Record) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	recs, err := s.load()
	if err != nil {
		return err
	}
	for i := range recs {
		if recs[i].Fingerprint == r.Fingerprint {
			recs[i] = r
			return s.save(recs)
		}
	}
	return s.save(append(recs, r))
}

// Remove forgets a target; it reports whether one was removed.
func (s *Store) Remove(fingerprint string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	recs, err := s.load()
	if err != nil {
		return false, err
	}
	for i := range recs {
		if recs[i].Fingerprint == fingerprint {
			return true, s.save(append(recs[:i], recs[i+1:]...))
		}
	}
	return false, nil
}

// Find picks a target by exact name (case-insensitive), or by a prefix of at
// least four characters of its fingerprint in hex or short form. An empty
// query matches when exactly one target is paired.
func (s *Store) Find(query string) (Record, error) {
	recs, err := s.All()
	if err != nil {
		return Record{}, err
	}
	q := strings.ToLower(strings.TrimSpace(query))
	compact := strings.ReplaceAll(q, "-", "")
	var hits []Record
	for _, r := range recs {
		fp, err := r.ID()
		if err != nil {
			continue
		}
		short := strings.ToLower(strings.ReplaceAll(fp.Short(), "-", ""))
		switch {
		case q == "":
			hits = append(hits, r)
		case strings.EqualFold(r.Name, q):
			return r, nil
		case len(compact) >= 4 && (strings.HasPrefix(r.Fingerprint, compact) || strings.HasPrefix(short, compact)):
			hits = append(hits, r)
		}
	}
	switch {
	case len(hits) == 1:
		return hits[0], nil
	case len(hits) > 1 && q == "":
		return Record{}, fmt.Errorf("%d devices are paired; name one", len(hits))
	case len(hits) > 1:
		return Record{}, fmt.Errorf("%q matches %d paired devices; give more of the identity", query, len(hits))
	case q == "":
		return Record{}, errors.New("no paired devices yet: pair one first")
	default:
		return Record{}, fmt.Errorf("no paired device matches %q", query)
	}
}

func (s *Store) load() ([]Record, error) {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var recs []Record
	if err := json.Unmarshal(data, &recs); err != nil {
		return nil, fmt.Errorf("%s: %w", s.path, err)
	}
	return recs, nil
}

func (s *Store) save(recs []Record) error {
	data, err := json.MarshalIndent(recs, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	return writeFileAtomic(s.path, append(data, '\n'), 0o600)
}
