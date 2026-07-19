// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package ccudata

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"strings"

	openccudata "github.com/SukramJ/go-openccu-data"
)

// ProfileStore holds the receiver-profile catalogue extracted from the
// OCCU easymode TCL files by Every
// receiver type has its own `.json.gz` artifact; `_receiver_type_aliases.json`
// maps alternative receiver names onto canonical ones.
//
// The payload per receiver type is kept as raw JSON so the UI can
// Consume the exact shape
// schema mirror that would drift on every upstream refresh.
type ProfileStore struct {
	// Receivers maps `<RECEIVER_TYPE>` → raw JSON of that receiver's
	// profile document (one entry per `.json.gz` file in the embedded
	// archive).
	Receivers map[string]json.RawMessage
	// Aliases maps a receiver type to the canonical type whose
	// profile definition should be used. Sourced from
	// `_receiver_type_aliases.json`.
	Aliases map[string]string
}

// Resolve returns the raw profile document for the given receiver type,
// following the alias table first. `ok` is false when neither the
// direct key nor an alias target is known.
func (s *ProfileStore) Resolve(receiverType string) (json.RawMessage, bool) {
	if s == nil {
		return nil, false
	}
	target := receiverType
	if alias, ok := s.Aliases[target]; ok {
		target = alias
	}
	raw, ok := s.Receivers[target]
	return raw, ok
}

// ResolvedProfile holds the locale-resolved name and description for a single
// profile entry.
type ResolvedProfile struct {
	ID          int
	Name        string
	Description string
}

// ResolvedProfile returns the profile at id for the given receiver type,
// resolved against locale. Falls back to "en" when the locale has no
// translation, and to a generic name ("Profile <id>") when neither
// locale nor "en" provides a value.
func (s *ProfileStore) ResolvedProfile(receiverType string, id int, locale string) (ResolvedProfile, bool) {
	raw, ok := s.Resolve(receiverType)
	if !ok {
		return ResolvedProfile{}, false
	}

	// The profile JSON is a map of receiver-type → {profiles: [...]}
	// with each entry carrying id, name{locale:str}, description{locale:str}.
	var outer map[string]struct {
		Profiles []struct {
			ID          int               `json:"id"`
			Name        map[string]string `json:"name"`
			Description map[string]string `json:"description"`
		} `json:"profiles"`
	}
	if err := json.Unmarshal(raw, &outer); err != nil {
		return ResolvedProfile{}, false
	}

	for _, doc := range outer {
		for _, p := range doc.Profiles {
			if p.ID != id {
				continue
			}
			name := p.Name[locale]
			if name == "" {
				name = p.Name["en"]
			}
			if name == "" {
				name = fmt.Sprintf("Profile %d", id)
			}
			desc := p.Description[locale]
			if desc == "" {
				desc = p.Description["en"]
			}
			return ResolvedProfile{ID: id, Name: name, Description: desc}, true
		}
	}
	return ResolvedProfile{}, false
}

// LoadProfilesEmbedded walks the embedded `profiles/` directory and
// decodes every `<name>.json.gz` into the store. The sibling
// `_receiver_type_aliases.json` is parsed into the alias table. A
// missing or corrupt file logs at the error boundary (returned as
// fmt error) but never aborts — the store ships whatever loaded
// successfully so the UI degrades gracefully.
func LoadProfilesEmbedded() (*ProfileStore, error) {
	store := emptyProfileStore()

	aliasesRaw, err := openccudata.ReadFile("profiles/_receiver_type_aliases.json")
	if err == nil {
		if err := json.Unmarshal(aliasesRaw, &store.Aliases); err != nil {
			return store, fmt.Errorf("ccudata: decode aliases: %w", err)
		}
	}

	names, err := openccudata.ReadDir("profiles")
	if err != nil {
		return store, fmt.Errorf("ccudata: list embedded profiles: %w", err)
	}
	for _, name := range names {
		if !strings.HasSuffix(name, ".json.gz") {
			continue
		}
		raw, err := decodeEmbeddedGzip("profiles/" + name)
		if err != nil {
			return store, fmt.Errorf("ccudata: decode profile %s: %w", name, err)
		}
		key := strings.TrimSuffix(name, ".json.gz")
		store.Receivers[key] = raw
	}
	return store, nil
}

// decodeEmbeddedGzip reads a gzipped JSON file from the embed and
// returns the raw JSON body. Shared by the profile and (future)
// translation loaders.
func decodeEmbeddedGzip(name string) (json.RawMessage, error) {
	compressed, err := openccudata.ReadFile(name)
	if err != nil {
		return nil, err
	}
	gz, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		return nil, err
	}
	defer func() { _ = gz.Close() }()
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(gz); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func emptyProfileStore() *ProfileStore {
	return &ProfileStore{
		Receivers: map[string]json.RawMessage{},
		Aliases:   map[string]string{},
	}
}
