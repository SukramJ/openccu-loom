// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package remoteproxy

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// pairedCredential is what one box pairing leaves behind for one instance:
// the box token, the certificate it was bound to, and the instance URL it
// was paired against. A token is only ever used for the URL it was minted
// for, so a changed instance URL invalidates it.
type pairedCredential struct {
	URL         string    `json:"url"`
	Token       string    `json:"token"`
	Fingerprint string    `json:"fingerprint,omitempty"`
	TokenName   string    `json:"token_name,omitempty"`
	PairedAt    time.Time `json:"paired_at"`
}

// pairStateFile is the on-disk shape of the state file.
type pairStateFile struct {
	Version   int                         `json:"version"`
	Instances map[string]pairedCredential `json:"instances"`
}

const pairStateVersion = 1

// pairStore keeps the paired credentials, keyed by instance name, in a
// 0600 file the add-on owns (/data/paired.json). Writes go through a temp
// file and a rename, so a crash never leaves half a token on disk. An
// empty path keeps the credentials in memory only.
type pairStore struct {
	path string
	log  *slog.Logger

	mu      sync.Mutex
	entries map[string]pairedCredential
}

// openPairStore loads the state file and prunes every entry that no longer
// matches a configured instance by name and normalised URL. A missing file
// is an empty store; an unreadable one is logged and replaced on the next
// write, because a lost pairing only costs a new one.
func openPairStore(path string, instances []Instance, log *slog.Logger) (*pairStore, error) {
	s := &pairStore{path: path, log: log, entries: map[string]pairedCredential{}}
	if path == "" {
		return s, nil
	}
	raw, err := os.ReadFile(path) //nolint:gosec // G304: the path is the operator-chosen state file (CLI flag), not request input.
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return s, nil
	case err != nil:
		return nil, fmt.Errorf("read pairing state: %w", err)
	}
	var f pairStateFile
	if err := json.Unmarshal(raw, &f); err != nil || f.Version != pairStateVersion {
		log.Warn("pairing state unreadable, starting without paired tokens", "path", path, "error", err)
		return s, nil
	}
	want := make(map[string]string, len(instances))
	for _, inst := range instances {
		want[inst.Name] = normaliseInstanceURL(inst.URL)
	}
	pruned := false
	for name, cred := range f.Instances {
		if u, ok := want[name]; !ok || u == "" || u != cred.URL || cred.Token == "" {
			log.Info("paired token dropped: the instance or its URL changed", "instance", name)
			pruned = true
			continue
		}
		s.entries[name] = cred
	}
	if pruned {
		if err := s.persistLocked(); err != nil {
			return nil, err
		}
	}
	return s, nil
}

// get answers the stored credential of an instance.
func (s *pairStore) get(name string) (pairedCredential, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.entries[name]
	return c, ok
}

// put stores the credential of an instance and writes the file.
func (s *pairStore) put(name string, cred pairedCredential) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	prev, had := s.entries[name]
	s.entries[name] = cred
	if err := s.persistLocked(); err != nil {
		if had {
			s.entries[name] = prev
		} else {
			delete(s.entries, name)
		}
		return err
	}
	return nil
}

// drop forgets the credential of an instance and writes the file.
func (s *pairStore) drop(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.entries[name]; !ok {
		return nil
	}
	delete(s.entries, name)
	return s.persistLocked()
}

// persistLocked writes the entries atomically with mode 0600.
func (s *pairStore) persistLocked() error {
	if s.path == "" {
		return nil
	}
	raw, err := json.MarshalIndent(pairStateFile{Version: pairStateVersion, Instances: s.entries}, "", "  ")
	if err != nil {
		return fmt.Errorf("encode pairing state: %w", err)
	}
	dir := filepath.Dir(s.path)
	tmp, err := os.CreateTemp(dir, ".paired-*.json")
	if err != nil {
		return fmt.Errorf("write pairing state: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write pairing state: %w", err)
	}
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write pairing state: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write pairing state: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write pairing state: %w", err)
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		return fmt.Errorf("write pairing state: %w", err)
	}
	return nil
}

// normaliseInstanceURL is the key a paired token is bound to: the
// validated instance URL with a lower-case host. An unparsable URL yields
// "", which never matches a stored entry.
func normaliseInstanceURL(raw string) string {
	u, err := parseInstanceURL(raw)
	if err != nil {
		return ""
	}
	u.Host = strings.ToLower(u.Host)
	return u.String()
}
