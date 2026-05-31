// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package wire_snapshots provides helpers for capturing and pinning the
// wire calls emitted by Custom-DP setters. Each test exercises a setter
// against a fake backend that records every SetValue / PutParamset call;
// the recorded sequence is compared against a golden JSON snapshot so
// that encoding regressions (wrong type, wrong parameter name, wrong
// value shape) are caught at test time.
//
// See README.md in this package for regeneration instructions.
package wire_snapshots

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// CapturedCall records one outbound wire call made by a Custom-DP setter.
type CapturedCall struct {
	// Method is "SetValue" or "PutParamset".
	Method string `json:"method"`
	// ParamsetKey is the paramset key used for PutParamset calls ("VALUES",
	// "MASTER", …). Empty for SetValue.
	ParamsetKey string `json:"paramset_key,omitempty"`
	// Address is the channel address that was written.
	Address string `json:"address"`
	// Parameter is the parameter name for SetValue calls. Empty for
	// PutParamset.
	Parameter string `json:"parameter,omitempty"`
	// Value holds the scalar value for SetValue calls.
	Value any `json:"value,omitempty"`
	// PutValues holds the full key→value map for PutParamset calls.
	PutValues map[string]any `json:"put_values,omitempty"`
}

// WireCapture is the ordered list of calls captured during one setter
// invocation.
type WireCapture []CapturedCall

// fakeWriter implements [generic.Writer] and the optional
// [generic.ParamsetWriter] extension. All calls are appended to the
// internal slice in invocation order. The fake is safe for concurrent
// use so that setters that fan-out goroutines (e.g. grouped commands)
// do not race — the ordering of concurrent calls is deterministic only
// per-invocation; the generator test must normalise where needed.
type fakeWriter struct {
	mu    sync.Mutex
	calls []CapturedCall
}

// SetValue records a single-parameter write.
func (f *fakeWriter) SetValue(
	_ context.Context,
	address string,
	parameter hmenum.Parameter,
	value any,
	_ hmenum.CommandPriority,
) error {
	f.mu.Lock()
	f.calls = append(f.calls, CapturedCall{
		Method:    "SetValue",
		Address:   address,
		Parameter: string(parameter),
		Value:     value,
	})
	f.mu.Unlock()
	return nil
}

// PutParamset records an atomic multi-parameter write. The values map is
// shallow-copied so later mutations by the caller do not affect the
// captured snapshot.
func (f *fakeWriter) PutParamset(
	_ context.Context,
	address string,
	paramsetKey hmenum.ParamsetKey,
	values map[string]any,
	_ hmenum.CommandPriority,
) error {
	cp := make(map[string]any, len(values))
	for k, v := range values {
		cp[k] = v
	}
	f.mu.Lock()
	f.calls = append(f.calls, CapturedCall{
		Method:      "PutParamset",
		ParamsetKey: string(paramsetKey),
		Address:     address,
		PutValues:   cp,
	})
	f.mu.Unlock()
	return nil
}

// Capture returns the recorded calls in invocation order and resets the
// internal slice so the same fakeWriter can be reused across multiple
// invocations within a test.
func (f *fakeWriter) Capture() WireCapture {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make(WireCapture, len(f.calls))
	copy(out, f.calls)
	f.calls = f.calls[:0]
	return out
}

// NewFakeWriter constructs a ready-to-use fakeWriter.
func NewFakeWriter() *fakeWriter { //nolint:revive // unexported type by design
	return &fakeWriter{}
}

// SnapshotFile is the top-level structure of a golden JSON snapshot file
// located under snapshots/<dp_type>__<setter>.json.
type SnapshotFile struct {
	// DPType is the human-readable Custom-DP category (e.g. "Switch").
	DPType string `json:"dp_type"`
	// Setter is the method name (e.g. "TurnOn").
	Setter string `json:"setter"`
	// Inputs is the ordered list of input cases.
	Inputs []SnapshotEntry `json:"inputs"`
}

// SnapshotEntry pairs one input description with the expected wire calls.
type SnapshotEntry struct {
	// Label is an optional human-readable description of this input case
	// (e.g. "priority=normal" or "level=0.5").
	Label string `json:"label,omitempty"`
	// Calls is the ordered list of expected wire calls.
	Calls []CapturedCall `json:"calls"`
}

// SnapshotFileName returns the conventional file name for a snapshot.
func SnapshotFileName(dpType, setter string) string {
	return fmt.Sprintf("%s__%s.json", dpType, setter)
}

// MarshalSnapshot serialises a SnapshotFile to indented JSON.
func MarshalSnapshot(sf SnapshotFile) ([]byte, error) {
	return json.MarshalIndent(sf, "", "  ")
}

// UnmarshalSnapshot deserialises a SnapshotFile from JSON bytes.
func UnmarshalSnapshot(data []byte) (SnapshotFile, error) {
	var sf SnapshotFile
	if err := json.Unmarshal(data, &sf); err != nil {
		return SnapshotFile{}, err
	}
	return sf, nil
}

// NormaliseCalls sorts a WireCapture so that concurrent multi-call
// invocations (e.g. two SetValue calls whose relative order is
// non-deterministic) produce a stable golden representation. The sort
// key is Method + Address + Parameter + ParamsetKey, lexicographically.
//
// Only use this helper during snapshot generation; the pin test compares
// against the stored (already-normalised) sequence without re-sorting,
// because the production code path is deterministic.
func NormaliseCalls(calls WireCapture) WireCapture {
	sorted := make(WireCapture, len(calls))
	copy(sorted, calls)
	sort.Slice(sorted, func(i, j int) bool {
		ki := sorted[i].Method + "|" + sorted[i].Address + "|" + sorted[i].Parameter + "|" + sorted[i].ParamsetKey
		kj := sorted[j].Method + "|" + sorted[j].Address + "|" + sorted[j].Parameter + "|" + sorted[j].ParamsetKey
		return ki < kj
	})
	return sorted
}
