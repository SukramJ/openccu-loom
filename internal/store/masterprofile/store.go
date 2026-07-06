// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package masterprofile

import (
	"compress/gzip"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"sort"
	"strings"
	"sync"
)

//go:embed data/*.json.gz
var embedded embed.FS

// Profile is one master-profile entry: a numeric id, localised name
// and description, and the MASTER-paramset values to apply.
type Profile struct {
	ID          int                        `json:"id"`
	Name        map[string]string          `json:"name"`
	Description map[string]string          `json:"description"`
	Params      map[string]ParamConstraint `json:"params"`
}

// ParamConstraint is the shape every parameter value carries in the
// upstream JSON. Only "fixed" is widely used today; "range" /
// "value_list" / "min" / "max" appear sporadically. The raw JSON
// decoded value is preserved so callers may inspect non-trivial
// constraints without re-decoding.
type ParamConstraint struct {
	ConstraintType string `json:"constraint_type"`
	Value          any    `json:"value"`
}

// LocalisedName returns the locale-keyed name, falling back to "en"
// then to the first available entry. Returns "" only when the profile
// has no name at all.
func (p Profile) LocalisedName(locale string) string {
	return localised(p.Name, locale)
}

// LocalisedDescription returns the locale-keyed description, falling
// back to "en" then to the first available entry.
func (p Profile) LocalisedDescription(locale string) string {
	return localised(p.Description, locale)
}

// cloneProfile returns a copy of p whose Name, Description and Params maps
// are cloned rather than shared with the store's cached copy — otherwise a
// caller mutating a returned Profile would corrupt every future lookup for
// the same device/channel type.
func cloneProfile(p Profile) Profile {
	p.Name = maps.Clone(p.Name)
	p.Description = maps.Clone(p.Description)
	p.Params = maps.Clone(p.Params)
	return p
}

func localised(m map[string]string, locale string) string {
	if v, ok := m[locale]; ok && v != "" {
		return v
	}
	if v, ok := m["en"]; ok && v != "" {
		return v
	}
	for _, v := range m {
		if v != "" {
			return v
		}
	}
	return ""
}

// Store is the lookup surface for master profiles. The zero value is
// not usable — call [New] to construct one bound to the embedded
// data.
type Store struct {
	mu       sync.Mutex
	files    fs.FS
	prefix   string
	cache    map[string]map[string][]Profile // deviceType -> channelType -> profiles
	indexed  []string                        // device types observable via List
	indexErr error
}

// New constructs a [Store] backed by the package's embedded asset FS.
func New() *Store {
	return &Store{files: embedded, prefix: "data", cache: make(map[string]map[string][]Profile)}
}

// ErrNotFound is returned when no profile matches the requested
// (deviceType, channelType) pair.
var ErrNotFound = errors.New("masterprofile: no profile for device/channel-type")

// DeviceTypes returns every device-type identifier the embedded
// catalogue advertises (the basenames of the .json.gz files), sorted
// alphabetically.
func (s *Store) DeviceTypes() ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.indexed == nil && s.indexErr == nil {
		entries, err := fs.ReadDir(s.files, s.prefix)
		if err != nil {
			s.indexErr = fmt.Errorf("masterprofile: read dir: %w", err)
			return nil, s.indexErr
		}
		out := make([]string, 0, len(entries))
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			name := e.Name()
			if !strings.HasSuffix(name, ".json.gz") {
				continue
			}
			out = append(out, strings.TrimSuffix(name, ".json.gz"))
		}
		sort.Strings(out)
		s.indexed = out
	}
	if s.indexErr != nil {
		return nil, s.indexErr
	}
	out := make([]string, len(s.indexed))
	copy(out, s.indexed)
	return out, nil
}

// Profiles returns every profile registered for the given
// (deviceType, channelType) pair, sorted by Profile.ID. ChannelType
// "" is treated as a request for the catch-all "KEY" entry.
func (s *Store) Profiles(deviceType, channelType string) ([]Profile, error) {
	bucket, err := s.load(deviceType)
	if err != nil {
		return nil, err
	}
	key := channelType
	if key == "" {
		key = "KEY"
	}
	profiles, ok := bucket[key]
	if !ok {
		// Fall back to the global "KEY" bucket when channel-specific is missing.
		if key != "KEY" {
			profiles, ok = bucket["KEY"]
		}
	}
	if !ok || len(profiles) == 0 {
		return nil, fmt.Errorf("%w: %s/%s", ErrNotFound, deviceType, channelType)
	}
	out := make([]Profile, len(profiles))
	for i, p := range profiles {
		out[i] = cloneProfile(p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// Profile fetches a single profile by id. Returns [ErrNotFound] when
// either the device-type is unknown or the id is missing.
func (s *Store) Profile(deviceType, channelType string, id int) (Profile, error) {
	profiles, err := s.Profiles(deviceType, channelType)
	if err != nil {
		return Profile{}, err
	}
	for _, p := range profiles {
		if p.ID == id {
			return p, nil
		}
	}
	return Profile{}, fmt.Errorf("%w: %s/%s id=%d", ErrNotFound, deviceType, channelType, id)
}

// ChannelTypes lists every channel-type bucket present for the given
// device-type. Useful for diagnostics endpoints.
func (s *Store) ChannelTypes(deviceType string) ([]string, error) {
	bucket, err := s.load(deviceType)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(bucket))
	for k := range bucket {
		out = append(out, k)
	}
	sort.Strings(out)
	return out, nil
}

// load reads (and caches) the profile file for one device type. The
// file format is `{ "<channelType>": { "profiles": [...] }, ... }`
// where one of the keys is the catch-all "KEY".
func (s *Store) load(deviceType string) (map[string][]Profile, error) {
	if deviceType == "" {
		return nil, errors.New("masterprofile: empty device type")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if cached, ok := s.cache[deviceType]; ok {
		return cached, nil
	}
	path := s.prefix + "/" + deviceType + ".json.gz"
	f, err := s.files.Open(path)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, deviceType)
	}
	defer func() { _ = f.Close() }()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return nil, fmt.Errorf("masterprofile: gunzip %s: %w", deviceType, err)
	}
	defer func() { _ = gz.Close() }()

	var raw map[string]struct {
		Profiles []Profile `json:"profiles"`
	}
	if err := json.NewDecoder(gz).Decode(&raw); err != nil {
		return nil, fmt.Errorf("masterprofile: decode %s: %w", deviceType, err)
	}
	bucket := make(map[string][]Profile, len(raw))
	for k, v := range raw {
		bucket[k] = v.Profiles
	}
	s.cache[deviceType] = bucket
	return bucket, nil
}
