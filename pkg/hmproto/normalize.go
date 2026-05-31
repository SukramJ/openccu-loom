// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package hmproto

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// NormalizeDevice returns a canonical copy of d with fields coerced to
// standard types, trimmed whitespace, and deterministic ordering.
//
// Invariants (guaranteed on the return value):
//   - Address / Type / Parent / Interface are trimmed.
//   - Paramsets and Children are sorted ascending.
//   - Extra is a fresh, sorted-by-key view (preserves wire fidelity
//     without introducing map iteration non-determinism in the hash).
//   - Idempotent: NormalizeDevice(NormalizeDevice(d)) == NormalizeDevice(d).
func NormalizeDevice(d DeviceDescription) DeviceDescription {
	d.Address = strings.TrimSpace(d.Address)
	d.Type = strings.TrimSpace(d.Type)
	d.Parent = strings.TrimSpace(d.Parent)
	d.Interface = strings.TrimSpace(d.Interface)
	d.Firmware = strings.TrimSpace(d.Firmware)
	d.Serial = strings.TrimSpace(d.Serial)
	d.AvailableFirmware = strings.TrimSpace(d.AvailableFirmware)
	d.FirmwareState = strings.TrimSpace(d.FirmwareState)
	d.UpdateState = strings.TrimSpace(d.UpdateState)

	if d.Paramsets != nil {
		d.Paramsets = sortedCopy(d.Paramsets)
	}
	if d.Children != nil {
		d.Children = sortedCopy(d.Children)
	}
	if d.Extra != nil {
		d.Extra = sortedRawMap(d.Extra)
	}
	return d
}

// NormalizeParameter returns a canonical copy of p.
//
// Invariants:
//   - Unit / ID / Control are trimmed.
//   - Min / Max / Default are reformatted through json.Compact so whitespace
//     inside numeric or string literals does not affect the hash.
//   - ValueList is copied (not sorted — order carries enum semantics).
//   - Extra is a sorted-by-key view.
//   - Idempotent.
func NormalizeParameter(p ParameterData) ParameterData {
	p.Unit = strings.TrimSpace(p.Unit)
	p.ID = strings.TrimSpace(p.ID)
	p.Control = strings.TrimSpace(p.Control)

	p.Min = compactJSON(p.Min)
	p.Max = compactJSON(p.Max)
	p.Default = compactJSON(p.Default)
	p.Special = compactJSON(p.Special)

	if p.ValueList != nil {
		// Copy verbatim — order encodes the enum index.
		clone := make([]string, len(p.ValueList))
		copy(clone, p.ValueList)
		p.ValueList = clone
	}
	if p.Extra != nil {
		p.Extra = sortedRawMap(p.Extra)
	}
	return p
}

// NormalizeParamset returns a canonical copy of ps. Each entry runs
// through [NormalizeParameter].
func NormalizeParamset(ps Paramset) Paramset {
	out := make(Paramset, len(ps))
	for k := range ps {
		out[strings.TrimSpace(k)] = NormalizeParameter(ps[k])
	}
	return out
}

// HashDevice returns a hex-encoded SHA-256 digest over the canonical
// serialisation of d. Equivalent descriptions — including ones whose
// non-canonical forms differ only in whitespace or map iteration order —
// produce the same hash, so persisting this digest and comparing on
// refresh is a reliable change-detection signal.
func HashDevice(d DeviceDescription) (string, error) {
	return hashCanonical(NormalizeDevice(d))
}

// HashParameter returns a hex-encoded SHA-256 digest over p.
func HashParameter(p ParameterData) (string, error) {
	return hashCanonical(NormalizeParameter(p))
}

// HashParamset returns a hex-encoded SHA-256 digest over ps. Keys are
// walked in sorted order so iteration randomisation cannot perturb the
// result.
func HashParamset(ps Paramset) (string, error) {
	normalised := NormalizeParamset(ps)
	keys := make([]string, 0, len(normalised))
	for k := range normalised {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	h := sha256.New()
	for _, k := range keys {
		if _, err := h.Write([]byte(k)); err != nil {
			return "", err
		}
		if _, err := h.Write([]byte{0}); err != nil {
			return "", err
		}
		raw, err := marshalCanonical(normalised[k])
		if err != nil {
			return "", err
		}
		if _, err := h.Write(raw); err != nil {
			return "", err
		}
		if _, err := h.Write([]byte{0}); err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// ---------- helpers ----------

func hashCanonical(v any) (string, error) {
	raw, err := marshalCanonical(v)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

// marshalCanonical returns the JSON encoding of v with compacted
// whitespace. Struct fields already have stable tag ordering; map
// marshaling uses sorted keys in encoding/json since Go 1.12, so the
// result is deterministic without a bespoke canonicaliser.
func marshalCanonical(v any) ([]byte, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("hmproto: marshal canonical: %w", err)
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		return nil, fmt.Errorf("hmproto: compact canonical: %w", err)
	}
	return buf.Bytes(), nil
}

// compactJSON rewrites raw so that numeric or string literals share a
// single canonical byte sequence. Empty input passes through as nil.
func compactJSON(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		// Invalid JSON is preserved verbatim; the caller sees it on the
		// next decode and we don't silently drop data.
		return raw
	}
	out := make(json.RawMessage, buf.Len())
	copy(out, buf.Bytes())
	return out
}

func sortedCopy(in []string) []string {
	out := make([]string, len(in))
	copy(out, in)
	sort.Strings(out)
	return out
}

func sortedRawMap(in map[string]json.RawMessage) map[string]json.RawMessage {
	out := make(map[string]json.RawMessage, len(in))
	for k, v := range in {
		out[strings.TrimSpace(k)] = compactJSON(v)
	}
	return out
}
