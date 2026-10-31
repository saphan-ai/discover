// Package manifest records every file an analyser read and cites: relative path, size, mtime and SHA-256 at read
// time, so a citation stays verifiable after the target moves.
//
// The attestation's own reads (the two digest passes) are not recorded here: the tree digest covers them. The manifest
// of a scan that ran no analyser is therefore empty.
package manifest

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/saphan-ai/discover/internal/safepath"
)

// Schema names the serialised form.
const Schema = "discover-read-manifest/v1"

var (
	// ErrTooLarge means the file is over the size cap and was not recorded.
	ErrTooLarge = errors.New("file is over the size cap")
	// ErrChanged means a file read twice gave two different digests.
	ErrChanged = errors.New("file changed between two reads")
)

// Entry is one file that was read.
type Entry struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	Mtime  string `json:"mtime"`
	SHA256 string `json:"sha256"`
}

// Manifest is the set of files read under one root.
type Manifest struct {
	mu      sync.Mutex
	root    string
	entries map[string]Entry
}

// New returns an empty manifest for an absolute root.
func New(root string) *Manifest {
	return &Manifest{root: root, entries: map[string]Entry{}}
}

// Record reads and hashes a file once and returns its entry. A path already recorded returns the stored entry
// without a second read. maxBytes <= 0 means no cap. A file over the cap is not recorded.
func (m *Manifest) Record(rel string, maxBytes int64) (Entry, error) {
	rel = clean(rel)
	m.mu.Lock()
	e, ok := m.entries[rel]
	m.mu.Unlock()
	if ok {
		return e, nil
	}
	_, e, err := m.read(rel, maxBytes, false)
	return e, err
}

// Read is Record that also returns the bytes it read. Reading a recorded file again checks the digest still matches.
func (m *Manifest) Read(rel string, maxBytes int64) ([]byte, Entry, error) {
	return m.read(clean(rel), maxBytes, true)
}

func clean(rel string) string {
	rel = filepath.ToSlash(rel)
	if rel == "" {
		return rel
	}
	return path.Clean(rel)
}

func (m *Manifest) read(rel string, maxBytes int64, keep bool) ([]byte, Entry, error) {
	root, err := safepath.OpenRoot(m.root)
	if err != nil {
		return nil, Entry{}, err
	}
	defer root.Close()
	f, fi, err := root.OpenRegular(rel)
	if err != nil {
		return nil, Entry{}, fmt.Errorf("%s: %w", rel, err)
	}
	defer f.Close()
	if maxBytes > 0 && fi.Size() > maxBytes {
		return nil, Entry{}, fmt.Errorf("%s: %w", rel, ErrTooLarge)
	}
	var r io.Reader = f
	if maxBytes > 0 {
		r = io.LimitReader(f, maxBytes+1)
	}
	h := sha256.New()
	var buf []byte
	var n int64
	if keep {
		buf, err = io.ReadAll(io.TeeReader(r, h))
		n = int64(len(buf))
	} else {
		n, err = io.Copy(h, r)
	}
	if err != nil {
		return nil, Entry{}, fmt.Errorf("%s: %w", rel, err)
	}
	if maxBytes > 0 && n > maxBytes {
		return nil, Entry{}, fmt.Errorf("%s: %w", rel, ErrTooLarge)
	}
	e := Entry{
		Path:   rel,
		Size:   n,
		Mtime:  fi.ModTime().UTC().Format(time.RFC3339Nano),
		SHA256: hex.EncodeToString(h.Sum(nil)),
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if old, ok := m.entries[rel]; ok {
		if old.SHA256 != e.SHA256 {
			return nil, Entry{}, fmt.Errorf("%s: %w", rel, ErrChanged)
		}
		return buf, old, nil
	}
	m.entries[rel] = e
	return buf, e, nil
}

// Entries returns the recorded entries sorted by path.
func (m *Manifest) Entries() []Entry {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Entry, 0, len(m.entries))
	for _, e := range m.entries {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

type document struct {
	Schema  string  `json:"schema"`
	Entries []Entry `json:"entries"`
}

// JSON serialises the manifest as READ_MANIFEST.json: entries sorted by path, ending in a newline.
func (m *Manifest) JSON() ([]byte, error) {
	b, err := json.MarshalIndent(document{Schema: Schema, Entries: m.Entries()}, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// Decode parses a serialised manifest and checks its schema.
func Decode(b []byte) ([]Entry, error) {
	var d document
	if err := json.Unmarshal(b, &d); err != nil {
		return nil, err
	}
	if d.Schema != Schema {
		return nil, fmt.Errorf("unknown manifest schema %q", d.Schema)
	}
	return d.Entries, nil
}
