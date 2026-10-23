package peer

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
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
	LastSeenMs  int64    `json:"lastSeenMs,omitempty"`
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

// UpdateExisting changes an existing record only; a concurrent forget stays forgotten.
func (s *Store) UpdateExisting(fingerprint string, update func(Record) Record) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	recs, err := s.load()
	if err != nil {
		return err
	}
	for i := range recs {
		if recs[i].Fingerprint == fingerprint {
			updated := update(recs[i])
			if reflect.DeepEqual(updated, recs[i]) {
				return nil
			}
			recs[i] = updated
			return s.save(recs)
		}
	}
	return nil
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

// Matches lists the targets a query could mean: the one whose fingerprint it
// is (hex or short form; two can share a short one), whatever the others are
// named; else the first whose name it is (case-insensitive); else those whose
// name, or any word in it, starts with the query, or whose fingerprint starts
// with it (at least four characters, hex or short form). An empty query
// matches every target.
func (s *Store) Matches(query string) ([]Record, error) {
	recs, err := s.All()
	if err != nil {
		return nil, err
	}
	return MatchesIn(recs, query), nil
}

// MatchesIn is Matches for the records recs, read once by a caller that asks
// many queries of them.
func MatchesIn(recs []Record, query string) []Record {
	q := strings.ToLower(strings.TrimSpace(query))
	compact := strings.ReplaceAll(q, "-", "")
	var exact, named, hits []Record
	for _, r := range recs {
		fp, err := r.ID()
		if err != nil {
			continue
		}
		short := strings.ToLower(strings.ReplaceAll(fp.Short(), "-", ""))
		switch {
		case q == "":
			hits = append(hits, r)
		case compact == fp.Hex() || compact == short:
			exact = append(exact, r)
		case strings.EqualFold(r.Name, q):
			named = append(named, r)
		case startsNameOrWord(r.Name, q),
			len(compact) >= 4 && (strings.HasPrefix(r.Fingerprint, compact) || strings.HasPrefix(short, compact)):
			hits = append(hits, r)
		}
	}
	switch {
	case len(exact) > 0:
		return exact
	case len(named) > 0:
		return named[:1]
	}
	return hits
}

func startsNameOrWord(name, q string) bool {
	name = strings.ToLower(name)
	if strings.HasPrefix(name, q) {
		return true
	}
	for i := 0; i < len(name); i++ {
		if c := name[i]; (c == ' ' || c == '-' || c == '_') && strings.HasPrefix(name[i+1:], q) {
			return true
		}
	}
	return false
}

// Find is Matches narrowed to exactly one target; when the query means
// several, the error is an *AmbiguousError.
func (s *Store) Find(query string) (Record, error) {
	recs, err := s.All()
	if err != nil {
		return Record{}, err
	}
	return FindIn(recs, query)
}

// FindIn is Find for the records recs, read once by a caller that asks many
// queries of them.
func FindIn(recs []Record, query string) (Record, error) {
	hits := MatchesIn(recs, query)
	q := strings.TrimSpace(query)
	switch {
	case len(hits) == 1:
		return hits[0], nil
	case len(hits) > 1:
		return Record{}, &AmbiguousError{Query: query, Matches: hits}
	case q == "":
		return Record{}, errors.New("no paired devices yet: pair one first")
	default:
		return Record{}, fmt.Errorf("no paired device matches %q", query)
	}
}

// AmbiguousError is Find's error when the query means more than one target:
// Matches holds them, and the message lists their names as stored.
type AmbiguousError struct {
	Query   string
	Matches []Record
}

func (e *AmbiguousError) Error() string {
	if strings.TrimSpace(e.Query) == "" {
		return fmt.Sprintf("%d devices are paired (%s); name one", len(e.Matches), Names(e.Matches))
	}
	return fmt.Sprintf("%q matches %s; give more of the name", e.Query, Names(e.Matches))
}

// Names lists records' names for messages: "Pixel, Tablet".
func Names(recs []Record) string {
	names := make([]string, len(recs))
	for i, r := range recs {
		names[i] = r.Name
	}
	return strings.Join(names, ", ")
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
