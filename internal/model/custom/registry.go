// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package custom is the device-profile registry plus the custom data-
// point types each profile composes.
//
// A [Profile] is a description of which channels of a physical device
// carry which semantic roles. The [Registry] maps a device's CCU type
// (e.g. "HmIP-BROLL") to a [Profile]; the domain layer reads the
// profile at device-creation time to lay out the generic/custom data-
// point graph.
//
// 0.1.0 ships a small hand-written subset as reference; the full
// Catalogue is regenerated from
// script/generate_profiles.py and landed in.
package custom

import (
	"errors"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// ExtendedDeviceConfig mirrors
// It carries device-specific overrides that complement a standard ProfileConfig.
type ExtendedDeviceConfig struct {
	// FixedChannelFields are device-specific overrides on absolute channel
	// numbers (keyed by channel number, then by Field → Parameter). These
	// fields replace or add to the generic field mapping at runtime,
	// independent of the channel group_no offset.
	FixedChannelFields map[int]map[hmenum.Field]hmenum.Parameter

	// AdditionalDataPoints lists generic-DP parameters that should be
	// created in addition to those covered by the profile, per absolute
	// channel number (or a tuple of channel numbers encoded as a single int
	// — use negative values as a sentinel for tuple keys when needed in Go).
	AdditionalDataPoints map[int][]hmenum.Parameter
}

// RequiredParameters collects every Parameter referenced by this
// ExtendedDeviceConfig — both from FixedChannelFields and from
// AdditionalDataPoints — and returns a deduplicated, sorted slice.
//
// This mirrors the Python ExtendedDeviceConfig.required_parameters property
//
// A nil receiver returns an empty (non-nil) slice.
func (e *ExtendedDeviceConfig) RequiredParameters() []hmenum.Parameter {
	if e == nil {
		return []hmenum.Parameter{}
	}

	seen := make(map[hmenum.Parameter]struct{})

	for _, fieldMap := range e.FixedChannelFields {
		for _, param := range fieldMap {
			seen[param] = struct{}{}
		}
	}

	for _, params := range e.AdditionalDataPoints {
		for _, param := range params {
			seen[param] = struct{}{}
		}
	}

	if len(seen) == 0 {
		return []hmenum.Parameter{}
	}

	out := make([]hmenum.Parameter, 0, len(seen))
	for param := range seen {
		out = append(out, param)
	}
	slices.Sort(out)
	return out
}

// ErrProfileConflict is returned when two profiles try to register the
// same device type.
var ErrProfileConflict = errors.New("custom: device type already registered")

// ErrProfileMissing is returned when a device type has no profile.
var ErrProfileMissing = errors.New("custom: profile missing for device type")

// ChannelRole captures the role a physical channel plays inside a
// custom device profile.
type ChannelRole string

// ChannelRole values.
const (
	ChannelRolePrimary   ChannelRole = "primary"
	ChannelRoleSecondary ChannelRole = "secondary"
	ChannelRoleState     ChannelRole = "state"
	ChannelRoleSensor    ChannelRole = "sensor"
	ChannelRoleConfig    ChannelRole = "config"
)

// ChannelRoleAssignment assigns a CCU channel number to a [ChannelRole].
type ChannelRoleAssignment struct {
	Channel int
	Role    ChannelRole
}

// Profile describes a single custom device profile.
//
// The three optional pointer fields (ScheduleChannelNo, Extended, Profile)
// Were added in Phase A.3 to mirror the full
// shape.
// All three are opt-in: existing Profile literals that omit them stay valid.
type Profile struct {
	// Name is the profile identifier ("IPSwitch", "RfCover", …).
	Name hmenum.DeviceProfile

	// DeviceType is the CCU-reported TYPE this profile applies to
	// ("HmIP-BROLL", "HM-LC-Dim1TPBU-FM", …).
	DeviceType string

	// ProductGroup categorises the family (HmIP, HM, …).
	ProductGroup hmenum.ProductGroup

	// Category is the fine-grained routing class (cover, switch, …).
	Category hmenum.DataPointCategory

	// Channels names the channels relevant to the profile, with their
	// semantic roles. Kept for backward compatibility; the Materializer
	// (WX-D.10) uses Profile when non-nil and falls back to Channels.
	Channels []ChannelRoleAssignment

	// ScheduleChannelNo is the absolute channel number on which the weekly
	// schedule data point is created, if the device supports scheduling.
	// nil means no schedule support.
	ScheduleChannelNo *int

	// Extended carries optional device-specific field overrides and
	// additional data-point declarations that complement the standard
	// ProfileConfig. nil means no extended config.
	Extended *ExtendedDeviceConfig

	// Config is the channel-group schema produced by the profile generator
	// ( Phase A.2, profile_schema.go). When non-nil the Materializer
	// prefers this over the legacy Channels list. Constructor injection is
	// deferred to WX-D.12.
	Config *ProfileConfig
}

// Rebase applies a group_no offset to the embedded ProfileConfig and
// returns the absolute-channel-numbered schema the Materializer consumes.
// Returns the zero RebasedChannelGroupConfig when Config is nil.
func (p Profile) Rebase(groupNo int) RebasedChannelGroupConfig {
	if p.Config == nil {
		return RebasedChannelGroupConfig{}
	}
	return RebaseChannelGroup(*p.Config, groupNo)
}

// registryKey is the composite key: a device model can carry more than
// One profile.
// lock that also exposes a button-lock) *and* within the same category
// when the device has multiple independent sub-channels (e.g. an
// RGBW light that has both a white dimmer profile and a fixed-colour
// profile on the same physical device, registered via
// `register_multiple`). The key uses profile name plus a deterministic
// signature of the channel set as the tiebreaker so two profiles for
// the same (category, deviceType, name) but different channels can
// Co-exist — mirroring
type registryKey struct {
	Category   hmenum.DataPointCategory
	DeviceType string
	Name       hmenum.DeviceProfile
	Channels   string
}

// channelSignature builds a stable string fingerprint of the profile's
// Channels assignment list, used as a tiebreaker in [registryKey].
// Profiles whose channel assignments differ — even when category,
// device type and profile name match — must keep separate registry
// entries.
func channelSignature(channels []ChannelRoleAssignment) string {
	if len(channels) == 0 {
		return ""
	}
	var sb strings.Builder
	for i, c := range channels {
		if i > 0 {
			sb.WriteByte(',')
		}
		// strconv-free: small ints, role is short.
		sb.WriteString(string(c.Role))
		sb.WriteByte(':')
		// itoa for ints up to 32-bit signed without importing strconv.
		// (channels are realistically small positive ints.)
		fmtInt(&sb, c.Channel)
	}
	return sb.String()
}

// fmtInt writes the decimal representation of v into sb. Avoids
// pulling strconv into the registry hot path (it is otherwise unused).
func fmtInt(sb *strings.Builder, v int) {
	if v == 0 {
		sb.WriteByte('0')
		return
	}
	if v < 0 {
		sb.WriteByte('-')
		v = -v
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	sb.Write(buf[i:])
}

// Registry maps (category, CCU device TYPE) to [Profile]. Safe for
// concurrent use.
type Registry struct {
	mu        sync.RWMutex
	items     map[registryKey]Profile
	blacklist []string // normalized (lowercase, hb-→hm-) prefix entries

	// constructors holds the per-profile [Constructor] map populated
	// via [RegisterConstructor]. Lazily allocated by the materializer
	// (see materialize.go::ensureConstructors) so the zero-value
	// Registry literal stays usable.
	constructors *constructorRegistry
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry { return &Registry{items: make(map[registryKey]Profile)} }

// Register adds a profile. Returns [ErrProfileConflict] when a profile
// is already registered for the same (category, device type, name) triple.
// The DeviceType is stored in its normalized form (lowercase, hb-→hm-) so
// that lookups via Get / ForCategory / ForDevice / Has / GetConfigs are
// all case-insensitive and HomeBrew-transparent.
func (r *Registry) Register(p Profile) error {
	if p.DeviceType == "" {
		return errors.New("custom: Profile.DeviceType is required")
	}
	p.DeviceType = normalizeModel(p.DeviceType)
	key := registryKey{Category: p.Category, DeviceType: p.DeviceType, Name: p.Name, Channels: channelSignature(p.Channels)}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.items[key]; ok {
		return ErrProfileConflict
	}
	r.items[key] = p
	return nil
}

// MustRegister panics on conflict — useful in package init() blocks.
func (r *Registry) MustRegister(p Profile) {
	if err := r.Register(p); err != nil {
		panic(err)
	}
}

// MustRegisterAll registers every profile in profiles and panics on the
// first conflict. Intended for use in generated init() blocks / tests.
func (r *Registry) MustRegisterAll(profiles []Profile) {
	for _, p := range profiles {
		r.MustRegister(p)
	}
}

// RegisterMultiple atomically registers all profiles in profiles. If
// any profile would produce a conflict (same registryKey already
// present) the whole batch is rolled back and [ErrProfileConflict] is
// returned. No partial state is observable under concurrent reads.
func (r *Registry) RegisterMultiple(profiles []Profile) error {
	if len(profiles) == 0 {
		return nil
	}
	// Validate up front (no lock required for key construction).
	for _, p := range profiles {
		if p.DeviceType == "" {
			return errors.New("custom: Profile.DeviceType is required")
		}
	}
	// Normalize all DeviceTypes before the conflict check so we hold
	// one lock and operate on canonical keys throughout.
	normalized := make([]Profile, len(profiles))
	for i, p := range profiles {
		p.DeviceType = normalizeModel(p.DeviceType)
		normalized[i] = p
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	// Check for conflicts before modifying state (atomic rollback).
	for _, p := range normalized {
		key := registryKey{Category: p.Category, DeviceType: p.DeviceType, Name: p.Name, Channels: channelSignature(p.Channels)}
		if _, ok := r.items[key]; ok {
			return ErrProfileConflict
		}
	}
	// No conflicts: insert all.
	for _, p := range normalized {
		key := registryKey{Category: p.Category, DeviceType: p.DeviceType, Name: p.Name, Channels: channelSignature(p.Channels)}
		r.items[key] = p
	}
	return nil
}

// NormalizeModel applies the same normalization as
// - lowercase
// - replace "hb-" prefix with "hm-" (HomeBrew → Homematic)
//
// Only the leading "hb-" prefix is replaced, not mid-string occurrences.
func normalizeModel(model string) string {
	lower := strings.ToLower(model)
	if strings.HasPrefix(lower, "hb-") {
		return "hm-" + lower[3:]
	}
	return lower
}

// Blacklist adds model strings to the blacklist. Models are normalized
// before storage (lowercase, hb-→hm-). Subsequent calls to
// [GetConfigs] or [IsBlacklisted] for a matching model return empty
// true, respectively. Prefix semantics: a blacklist entry "hmip-foo"
// matches any model whose normalized form starts with "hmip-foo".
func (r *Registry) Blacklist(models ...string) {
	if len(models) == 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, m := range models {
		normalized := normalizeModel(m)
		// Avoid duplicates.
		found := slices.Contains(r.blacklist, normalized)
		if !found {
			r.blacklist = append(r.blacklist, normalized)
		}
	}
	sort.Strings(r.blacklist)
}

// IsBlacklisted reports whether the given model (after normalization)
// has a prefix match against any blacklist entry.
func (r *Registry) IsBlacklisted(model string) bool {
	normalized := normalizeModel(model)
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, bl := range r.blacklist {
		if strings.HasPrefix(normalized, bl) {
			return true
		}
	}
	return false
}

// GetBlacklist returns the current blacklist entries in sorted order.
func (r *Registry) GetBlacklist() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if len(r.blacklist) == 0 {
		return nil
	}
	out := make([]string, len(r.blacklist))
	copy(out, r.blacklist)
	return out
}

// GetConfigs returns all profiles that match the given model, using the
// Same hierarchical algorithm as
//
// 1. Normalize the model (lowercase, hb-→hm-).
// 2. Blacklist check: if any blacklist entry is a prefix of the
// normalized model, return empty.
// 3. Per-category search:
// a. Priority 1 — exact match (normalized model == registered DeviceType).
// b. Priority 2 — prefix match (normalized model starts with registered
// DeviceType); first-match-wins within each category.
// 4. Aggregate results across all categories; sort by Category, then
// Name for stable output.
func (r *Registry) GetConfigs(model string) []Profile {
	normalized := normalizeModel(model)

	// Blacklist check (needs read lock; inline IsBlacklisted to hold
	// one lock for the whole operation).
	r.mu.RLock()
	defer r.mu.RUnlock()

	for _, bl := range r.blacklist {
		if strings.HasPrefix(normalized, bl) {
			return nil
		}
	}

	// Collect the set of categories present in the registry.
	catSeen := make(map[hmenum.DataPointCategory]struct{})
	for k := range r.items {
		catSeen[k.Category] = struct{}{}
	}

	var out []Profile
	for cat := range catSeen {
		// Priority 1: exact match.
		exactFound := false
		for k, v := range r.items {
			if k.Category != cat {
				continue
			}
			if k.DeviceType == normalized {
				out = append(out, v)
				exactFound = true
			}
		}
		if exactFound {
			continue
		}
		// Priority 2: prefix match — first-match-wins.
		// We need a stable ordering of keys to make "first" deterministic;
		// sort keys by DeviceType length descending (longer = more specific).
		type kv struct {
			key   registryKey
			value Profile
		}
		var catKVs []kv
		for k, v := range r.items {
			if k.Category == cat {
				catKVs = append(catKVs, kv{k, v})
			}
		}
		sort.Slice(catKVs, func(i, j int) bool {
			li, lj := len(catKVs[i].key.DeviceType), len(catKVs[j].key.DeviceType)
			if li != lj {
				return li > lj // longer prefix = more specific = wins
			}
			return catKVs[i].key.DeviceType < catKVs[j].key.DeviceType // tiebreak
		})
		for i := range catKVs {
			if strings.HasPrefix(normalized, catKVs[i].key.DeviceType) {
				out = append(out, catKVs[i].value)
				break // first-match-wins per category
			}
		}
	}

	// Stable sort: by category, then profile name.
	sort.Slice(out, func(i, j int) bool {
		if out[i].Category != out[j].Category {
			return out[i].Category < out[j].Category
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// Get returns the profile for (category, deviceType). When a device
// has multiple profiles in the same category, Get returns the one
// that sorts first by name — use [ForCategory] or [ForDevice] for the
// full picture.
// deviceType is normalized before lookup (case-insensitive, hb-→hm-).
func (r *Registry) Get(category hmenum.DataPointCategory, deviceType string) (Profile, error) {
	normalized := normalizeModel(deviceType)
	r.mu.RLock()
	defer r.mu.RUnlock()
	var found Profile
	hit := false
	for k, v := range r.items {
		if k.Category != category || k.DeviceType != normalized {
			continue
		}
		if !hit || v.Name < found.Name {
			found = v
			hit = true
		}
	}
	if !hit {
		return Profile{}, ErrProfileMissing
	}
	return found, nil
}

// ForCategory returns every profile registered for (category,
// deviceType), sorted by name.
// deviceType is normalized before lookup (case-insensitive, hb-→hm-).
func (r *Registry) ForCategory(category hmenum.DataPointCategory, deviceType string) []Profile {
	normalized := normalizeModel(deviceType)
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Profile, 0, 2)
	for k, v := range r.items {
		if k.Category == category && k.DeviceType == normalized {
			out = append(out, v)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// ForDevice returns every profile registered for deviceType across all
// categories, sorted by category for stable iteration.
// deviceType is normalized before lookup (case-insensitive, hb-→hm-).
func (r *Registry) ForDevice(deviceType string) []Profile {
	normalized := normalizeModel(deviceType)
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Profile, 0, 2)
	for k, v := range r.items {
		if k.DeviceType == normalized {
			out = append(out, v)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Category < out[j].Category })
	return out
}

// Has reports whether any profile exists for (category, deviceType).
// deviceType is normalized before lookup (case-insensitive, hb-→hm-).
func (r *Registry) Has(category hmenum.DataPointCategory, deviceType string) bool {
	normalized := normalizeModel(deviceType)
	r.mu.RLock()
	defer r.mu.RUnlock()
	for k := range r.items {
		if k.Category == category && k.DeviceType == normalized {
			return true
		}
	}
	return false
}

// Clear removes all registrations from the registry. Primarily intended for
// testing to reset state between test cases.
func (r *Registry) Clear() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.items = make(map[registryKey]Profile)
	r.blacklist = nil
}

// GetAllExtendedConfigs returns all ExtendedDeviceConfig entries registered
// across all profiles and categories.
func (r *Registry) GetAllExtendedConfigs() []*ExtendedDeviceConfig {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []*ExtendedDeviceConfig
	for _, p := range r.items {
		if p.Extended != nil {
			out = append(out, p.Extended)
		}
	}
	return out
}

// IsMultiChannelDevice reports whether the device with the given model has
// multiple profile-channel assignments for the given category.
func (r *Registry) IsMultiChannelDevice(model string, category hmenum.DataPointCategory) bool {
	profiles := r.ForCategory(category, model)
	count := 0
	for _, p := range profiles {
		count += len(p.Channels)
	}
	return count > 1
}

// Len returns the profile count.
func (r *Registry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.items)
}

// DeviceTypes returns the registered TYPE strings (deduplicated) in
// sorted order.
func (r *Registry) DeviceTypes() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	seen := make(map[string]struct{}, len(r.items))
	out := make([]string, 0, len(r.items))
	for k := range r.items {
		if _, ok := seen[k.DeviceType]; ok {
			continue
		}
		seen[k.DeviceType] = struct{}{}
		out = append(out, k.DeviceType)
	}
	sort.Strings(out)
	return out
}
