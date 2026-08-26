// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Package linkprofile provides a read-only store for easymode link profiles.
//
// Link profiles are predefined parameter-set templates for direct device
// links (LINK paramsets). They are the "direct link" equivalent of
// master profiles — a user picks a named preset (e.g. "Short-press → dim
// up") instead of setting individual parameters by hand.
//
// Data source: the
// by receiver channel type (filename) and sender channel type (top-level JSON
// key). The embedded data/ directory contains 66 .json.gz files and one
// _receiver_type_aliases.json.
//
// Receiver-type aliases (e.g. OPTICAL_SIGNAL_RECEIVER →
// DIMMER_VIRTUAL_RECEIVER) are applied before every lookup so that CCU types
// that do not appear verbatim in the archive are still resolved.
//
// File format (per .json.gz):
//
//	{
//	  "<senderChannelType>": {
//	    "profiles": [
//	      { "id": 0, "name": {"en": "Expert", "de": "Experte"}, "description": {…}, "params": {…} },
//	      { "id": 1, … }
//	    ]
//	  },
//	  …
//	}
//
// MatchActiveProfile computes the most-specific matching profile given the
// current live LINK-paramset values — specificity = fixed_count −
// Loose_count×100, porting 's match_active_profile logic
// exactly.
package linkprofile

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"math"
	"slices"
	"sync"

	"github.com/SukramJ/openccu-loom/internal/ccudata"
)

// profileFS serves the archives from the shared metadata module. The
// files used to be duplicated into this package (and into masterprofile),
// which meant a data refresh reached the module but not these copies: they
// still carried the CCU's HTML references and the pre-3.89.5 constraint set
// long after the module had moved on.
var profileFS = ccudata.ProfilesFS()

// aliasFile is the receiver-type alias table, alongside the archives.
const aliasFile = "_receiver_type_aliases.json"

// ErrUnsupported is returned when the link-profile store has no data for
// the requested (receiverChannelType, senderChannelType) pair. This
// Occurs either because
// combination or because the embedded data has not yet been generated.
//
// Callers that display the link-edit form should treat ErrUnsupported
// as "no profiles available" — show the raw parameter editor.
var ErrUnsupported = errors.New("linkprofile: no profiles available for this channel-type pair")

// ParamConstraint mirrors the
// parameter within a profile definition.
type ParamConstraint struct {
	// ConstraintType is one of "fixed", "list", or "range".
	ConstraintType string    `json:"constraint_type"`
	Value          *float64  `json:"value,omitempty"`
	Values         []float64 `json:"values,omitempty"`
	Default        *float64  `json:"default,omitempty"`
	MinValue       *float64  `json:"min_value,omitempty"`
	MaxValue       *float64  `json:"max_value,omitempty"`
}

// Profile is one link-profile entry: a numeric id, a localised name,
// and the LINK-paramset values to apply. The shape mirrors
// masterprofile.Profile so the UI can use a shared rendering path.
type Profile struct {
	// ID is the profile number as defined in the
	ID int `json:"id"`
	// Name is the localised human-readable name keyed by locale ("en",
	// "de", …).
	Name map[string]string `json:"name"`
	// Description is an optional localised description.
	Description map[string]string `json:"description,omitempty"`
	// Params contains the LINK-paramset constraints for this profile.
	Params map[string]ParamConstraint `json:"params,omitempty"`
}

// LocalisedName returns the locale-keyed name, falling back to "en"
// then the first available entry.
func (p Profile) LocalisedName(locale string) string {
	return localised(p.Name, locale)
}

// LocalisedDescription returns the locale-keyed description.
func (p Profile) LocalisedDescription(locale string) string {
	return localised(p.Description, locale)
}

// FixedParams returns a map of parameter names to their fixed values.
// Only constraints with ConstraintType "fixed" and a non-nil Value are
// included. This is what gets applied to the LINK paramset when the
// user picks a profile.
func (p Profile) FixedParams() map[string]float64 {
	out := make(map[string]float64, len(p.Params))
	for name, c := range p.Params {
		if c.ConstraintType == "fixed" && c.Value != nil {
			out[name] = *c.Value
		}
	}
	return out
}

// cloneProfile returns a deep-enough copy of p: the Name, Description and
// Params maps are cloned so a caller mutating the returned Profile cannot
// reach into the store's cached copy (the cache holds the only slice
// backing these profiles, shared across every lookup).
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

// Store is the lookup surface for link profiles, backed by embedded
// .json.gz archives. The cache is populated lazily per receiver
// channel type (one file = one receiver type).
//
// Use [New] to construct a store. The zero value is safe for reads but
// returns empty results and ErrUnsupported for all lookups.
type Store struct {
	mu      sync.Mutex
	aliases map[string]string               // receiverType → canonical receiverType
	cache   map[string]map[string][]Profile // effectiveReceiverType → senderType → []Profile
}

// New constructs a [Store] backed by the package's embedded
// archives and receiver-type aliases.
func New() *Store {
	s := &Store{
		aliases: make(map[string]string),
		cache:   make(map[string]map[string][]Profile),
	}
	// Load aliases once at construction time. Failure is non-fatal —
	// aliasing is best-effort; the raw type will still be tried.
	raw, err := fs.ReadFile(profileFS, aliasFile)
	if err != nil {
		s.aliases = make(map[string]string)
	} else if err := json.Unmarshal(raw, &s.aliases); err != nil {
		s.aliases = make(map[string]string)
	}
	return s
}

// effectiveReceiver resolves receiver-type aliases and returns the
// canonical type to use for archive lookup.
func (s *Store) effectiveReceiver(receiverChannelType string) string {
	if alias, ok := s.aliases[receiverChannelType]; ok {
		return alias
	}
	return receiverChannelType
}

// GetLinkProfiles returns the available easymode profiles for the given
// (receiverChannelType, senderChannelType) pair in the requested locale.
//
// Returns an empty slice (and nil error) when no profiles are registered
// for the pair. Callers should treat an empty list as "use the raw
// parameter editor".
//
// The ctx argument is accepted for interface compatibility; the current
// implementation is synchronous and in-memory.
func (s *Store) GetLinkProfiles(_ context.Context, receiverChannelType, senderChannelType, locale string) ([]Profile, error) {
	if s == nil {
		return nil, nil
	}
	bucket, err := s.load(receiverChannelType)
	if err != nil {
		// Unknown receiver type: not an error for callers.
		return nil, nil //nolint:nilerr // absence of a profile bucket is a legitimate "no profiles" answer
	}
	profs, ok := bucket[senderChannelType]
	if !ok || len(profs) == 0 {
		return nil, nil
	}
	_ = locale // names are stored in their locale maps; callers use LocalisedName
	out := make([]Profile, len(profs))
	for i, p := range profs {
		out[i] = cloneProfile(p)
	}
	return out, nil
}

// GetProfileByID returns a single profile by receiver type, sender type, and
// numeric ID. Returns (Profile{}, false) when the combination is not found.
func (s *Store) GetProfileByID(receiverChannelType, senderChannelType string, id int) (Profile, bool) {
	if s == nil {
		return Profile{}, false
	}
	bucket, err := s.load(receiverChannelType)
	if err != nil {
		return Profile{}, false
	}
	profs, ok := bucket[senderChannelType]
	if !ok {
		return Profile{}, false
	}
	for _, p := range profs {
		if p.ID == id {
			return cloneProfile(p), true
		}
	}
	return Profile{}, false
}

// MatchActiveProfile returns the ID of the currently active profile (0 =
// Expert / no match) given the live LINK-paramset values.
//
// Specificity is fixed_count − loose_count×100, porting
// 's match_active_profile exactly. When multiple
// profiles match, the most specific one wins.
func (s *Store) MatchActiveProfile(receiverChannelType, senderChannelType string, currentValues map[string]any) int {
	if s == nil {
		return 0
	}
	bucket, err := s.load(receiverChannelType)
	if err != nil {
		return 0
	}
	profs, ok := bucket[senderChannelType]
	if !ok {
		return 0
	}
	bestID := 0
	bestScore := math.Inf(-1)
	for _, p := range profs {
		if p.ID == 0 || len(p.Params) == 0 {
			continue
		}
		if !profileMatches(p.Params, currentValues) {
			continue
		}
		score := profileSpecificity(p.Params)
		if score > bestScore {
			bestScore = score
			bestID = p.ID
		}
	}
	return bestID
}

// TestLinkProfile returns the fixed parameter values for the profile
// identified by (receiverChannelType, senderChannelType, id). The
// caller (the adapter) is responsible for looking up the channel types
// from the addresses before calling here.
//
// Returns ErrUnsupported when no profile data exists for the pair,
// and a descriptive error when the id is not found within the set.
func (s *Store) TestLinkProfile(_ context.Context, receiverChannelType, senderChannelType string, id int) (map[string]any, error) {
	if s == nil {
		return nil, ErrUnsupported
	}
	bucket, err := s.load(receiverChannelType)
	if err != nil || bucket == nil {
		return nil, ErrUnsupported
	}
	profs, ok := bucket[senderChannelType]
	if !ok || len(profs) == 0 {
		return nil, ErrUnsupported
	}
	for _, p := range profs {
		if p.ID == id {
			fixed := p.FixedParams()
			out := make(map[string]any, len(fixed))
			for k, v := range fixed {
				out[k] = v
			}
			return out, nil
		}
	}
	return nil, fmt.Errorf("linkprofile: profile id=%d not found for %s/%s: %w",
		id, receiverChannelType, senderChannelType, ErrUnsupported)
}

// Register pre-loads profiles for a (receiverChannelType, senderChannelType)
// pair. Primarily used in tests and by the profile-generator script
// when populating the store from embedded data.
func (s *Store) Register(receiverChannelType, senderChannelType string, profs []Profile) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cache == nil {
		s.cache = make(map[string]map[string][]Profile)
	}
	if _, ok := s.cache[receiverChannelType]; !ok {
		s.cache[receiverChannelType] = make(map[string][]Profile)
	}
	s.cache[receiverChannelType][senderChannelType] = profs
}

// ReceiverTypes returns all receiver channel types for which the embedded
// archive contains profiles (the basenames of the .json.gz files).
func (s *Store) ReceiverTypes() ([]string, error) {
	entries, err := fs.ReadDir(profileFS, ".")
	if err != nil {
		return nil, fmt.Errorf("linkprofile: read dir: %w", err)
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		name := e.Name()
		if len(name) > 8 && name[len(name)-8:] == ".json.gz" {
			out = append(out, name[:len(name)-8])
		}
	}
	return out, nil
}

// load reads (and caches) the profile archive for one receiver channel type.
// Receiver-type aliases are applied before the lookup.
func (s *Store) load(receiverChannelType string) (map[string][]Profile, error) {
	if receiverChannelType == "" {
		return nil, errors.New("linkprofile: empty receiver channel type")
	}
	effective := s.effectiveReceiver(receiverChannelType)

	s.mu.Lock()
	defer s.mu.Unlock()
	if cached, ok := s.cache[effective]; ok {
		return cached, nil
	}
	f, err := profileFS.Open(effective + ".json.gz")
	if err != nil {
		// Cache a nil sentinel so we don't retry on every call.
		s.cache[effective] = nil
		return nil, fmt.Errorf("%w: %s", ErrUnsupported, effective)
	}
	defer func() { _ = f.Close() }()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return nil, fmt.Errorf("linkprofile: gunzip %s: %w", effective, err)
	}
	defer func() { _ = gz.Close() }()

	var raw map[string]struct {
		Profiles []Profile `json:"profiles"`
	}
	if err := json.NewDecoder(gz).Decode(&raw); err != nil {
		return nil, fmt.Errorf("linkprofile: decode %s: %w", effective, err)
	}
	bucket := make(map[string][]Profile, len(raw))
	for senderType, v := range raw {
		// The archive stores display strings as the CCU WebUI's HTML
		// fragments ("Bew&auml;sserungsaktor"); consumers render them as
		// plain text, so they are decoded once here rather than in each
		// consumer. Constraint parameters are identifiers and stay untouched.
		for i := range v.Profiles {
			ccudata.UnescapeUITextMap(v.Profiles[i].Name)
			ccudata.UnescapeUITextMap(v.Profiles[i].Description)
		}
		bucket[senderType] = v.Profiles
	}
	s.cache[effective] = bucket
	return bucket, nil
}

// profileMatches reports whether every constraint in params is satisfied by
// the corresponding value in current. Missing keys in current are ignored
// (no decision either way), mirroring the Python reference behaviour.
func profileMatches(params map[string]ParamConstraint, current map[string]any) bool {
	for name, c := range params {
		raw, ok := current[name]
		if !ok {
			continue
		}
		num, ok := toFloat64(raw)
		if !ok {
			return false
		}
		switch c.ConstraintType {
		case "fixed":
			if c.Value != nil && num != *c.Value {
				return false
			}
		case "list":
			if len(c.Values) == 0 {
				continue
			}
			found := slices.Contains(c.Values, num)
			if !found {
				return false
			}
		case "range":
			if c.MinValue != nil && c.MaxValue != nil {
				if num < *c.MinValue || num > *c.MaxValue {
					return false
				}
			}
		}
	}
	return true
}

// profileSpecificity scores a profile: fixed constraints gain one point,
// non-fixed (list / range) subtract 100. All-fixed profiles always beat
// profiles with loose constraints regardless of total parameter count.
func profileSpecificity(params map[string]ParamConstraint) float64 {
	fixed, loose := 0, 0
	for _, c := range params {
		if c.ConstraintType == "fixed" {
			fixed++
		} else {
			loose++
		}
	}
	return float64(fixed) - float64(loose)*100
}

// toFloat64 narrows whatever the CCU returned (int / float / string)
// to a float64 for numeric comparison.
func toFloat64(v any) (float64, bool) {
	switch t := v.(type) {
	case float64:
		return t, true
	case float32:
		return float64(t), true
	case int:
		return float64(t), true
	case int32:
		return float64(t), true
	case int64:
		return float64(t), true
	case bool:
		if t {
			return 1, true
		}
		return 0, true
	case string:
		// Not trying strconv here — numeric strings are rare in LINK paramsets
		// and misidentifying would cause silent mismatches.
	}
	return 0, false
}
