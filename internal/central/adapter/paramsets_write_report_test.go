// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// paramsetReadCounter answers GetParamset from a per-key script and counts
// the reads per key, so a test can pin how many round trips a write costs.
type paramsetReadCounter struct {
	mu     sync.Mutex
	reads  map[hmenum.ParamsetKey]int
	answer func(key hmenum.ParamsetKey, n int) (map[string]any, error)
}

func (c *paramsetReadCounter) get(_ context.Context, _ string, key hmenum.ParamsetKey) (map[string]any, error) {
	c.mu.Lock()
	if c.reads == nil {
		c.reads = map[hmenum.ParamsetKey]int{}
	}
	c.reads[key]++
	n := c.reads[key]
	c.mu.Unlock()
	return c.answer(key, n)
}

func (c *paramsetReadCounter) count(key hmenum.ParamsetKey) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.reads[key]
}

// A MASTER write whose stored value was clamped by the interface process
// reports the divergence, and the one post-write read also refreshes the
// model: the channel's MASTER data point carries the stored value.
func TestPutParamsetMasterReportsClampedValue(t *testing.T) {
	t.Parallel()
	domain, _, fakeOps := buildParamsetFixture(t)
	reads := &paramsetReadCounter{answer: func(_ hmenum.ParamsetKey, n int) (map[string]any, error) {
		if n == 1 {
			return map[string]any{string(hmenum.ParameterLevel): 0.3}, nil // before
		}
		return map[string]any{string(hmenum.ParameterLevel): 0.5}, nil // clamped
	}}
	fakeOps.getParamsetFn = reads.get

	report, err := domain.PutParamset(context.Background(), "0001ABCD:1", hmenum.ParamsetKeyMaster,
		map[string]any{string(hmenum.ParameterLevel): 0.8})
	if err != nil {
		t.Fatalf("PutParamset: %v", err)
	}
	if report == nil {
		t.Fatal("MASTER write returned a nil report")
	}
	if len(report.Written) != 1 || report.Written[0] != string(hmenum.ParameterLevel) {
		t.Fatalf("Written = %v, want [LEVEL]", report.Written)
	}
	want := interfaces.ParamsetDivergence{Parameter: string(hmenum.ParameterLevel), Sent: 0.8, Stored: 0.5}
	if len(report.Divergences) != 1 || report.Divergences[0] != want {
		t.Fatalf("Divergences = %+v, want [%+v]", report.Divergences, want)
	}
	if report.ReadbackError != "" {
		t.Fatalf("ReadbackError = %q, want empty", report.ReadbackError)
	}
	// One read before the write for the audit row, exactly one after it:
	// the comparison and the model refresh share the post-write read.
	if got := reads.count(hmenum.ParamsetKeyMaster); got != 2 {
		t.Fatalf("MASTER reads = %d, want 2 (before + one read-back)", got)
	}
	ch := domain.resolveChannel("0001ABCD:1")
	if v := ch.GetAll(hmenum.ParamsetKeyMaster)[hmenum.ParameterLevel]; v.Unwrap() != 0.5 {
		t.Fatalf("model MASTER LEVEL = %v, want the stored 0.5", v.Unwrap())
	}
}

// A value stored exactly as sent is no divergence, and the list is empty
// rather than nil so the wire layers render it as [].
func TestPutParamsetMasterCleanWriteHasEmptyDivergences(t *testing.T) {
	t.Parallel()
	domain, _, fakeOps := buildParamsetFixture(t)
	fakeOps.getParamsetFn = func(context.Context, string, hmenum.ParamsetKey) (map[string]any, error) {
		return map[string]any{string(hmenum.ParameterLevel): 0.8}, nil
	}
	report, err := domain.PutParamset(context.Background(), "0001ABCD:1", hmenum.ParamsetKeyMaster,
		map[string]any{string(hmenum.ParameterLevel): 0.8})
	if err != nil {
		t.Fatalf("PutParamset: %v", err)
	}
	if report == nil || report.Divergences == nil || len(report.Divergences) != 0 {
		t.Fatalf("report = %+v, want a non-nil empty divergence list", report)
	}
}

// A failing post-write read does not fail the write — the wire write
// already succeeded — but the report says the comparison is unknown.
func TestPutParamsetMasterReadbackErrorIsReported(t *testing.T) {
	t.Parallel()
	domain, chw, fakeOps := buildParamsetFixture(t)
	fakeOps.getParamsetFn = func(context.Context, string, hmenum.ParamsetKey) (map[string]any, error) {
		return nil, errors.New("ccu unreachable")
	}
	report, err := domain.PutParamset(context.Background(), "0001ABCD:1", hmenum.ParamsetKeyMaster,
		map[string]any{string(hmenum.ParameterLevel): 0.8})
	if err != nil {
		t.Fatalf("PutParamset: %v, want nil — the write itself succeeded", err)
	}
	if chw.putCallCount() != 1 {
		t.Fatalf("channel writer PutParamset calls = %d, want 1", chw.putCallCount())
	}
	if report == nil || report.ReadbackError == "" {
		t.Fatalf("report = %+v, want ReadbackError set", report)
	}
	if len(report.Divergences) != 0 {
		t.Fatalf("Divergences = %+v, want none when the read-back failed", report.Divergences)
	}
}

// A VALUES write answers a nil report and computes no comparison. Its
// post-write read still runs and still refreshes the model.
func TestPutParamsetValuesReturnsNilReport(t *testing.T) {
	t.Parallel()
	domain, _, fakeOps := buildParamsetFixture(t)
	reads := &paramsetReadCounter{answer: func(_ hmenum.ParamsetKey, _ int) (map[string]any, error) {
		return map[string]any{string(hmenum.ParameterLevel): 0.9}, nil
	}}
	fakeOps.getParamsetFn = reads.get

	report, err := domain.PutParamset(context.Background(), "0001ABCD:1", hmenum.ParamsetKeyValues,
		map[string]any{string(hmenum.ParameterLevel): 0.8})
	if err != nil {
		t.Fatalf("PutParamset: %v", err)
	}
	if report != nil {
		t.Fatalf("VALUES report = %+v, want nil", report)
	}
	if got := reads.count(hmenum.ParamsetKeyValues); got != 2 {
		t.Fatalf("VALUES reads = %d, want 2 (before + model refresh)", got)
	}
	ch := domain.resolveChannel("0001ABCD:1")
	if v := ch.GetAll(hmenum.ParamsetKeyValues)[hmenum.ParameterLevel]; v.Unwrap() != 0.9 {
		t.Fatalf("model VALUES LEVEL = %v, want the refreshed 0.9", v.Unwrap())
	}
}

// On the backend path (channel unknown to the model) the comparison
// normalises through the fetched descriptor: an INTEGER stored as a Go int
// equals the JSON float that was sent, a missing parameter reports a nil
// Stored, and a stored value the descriptor cannot coerce is itself a
// divergence reported raw.
func TestPutParamsetBackendPathComparesThroughDescriptor(t *testing.T) {
	t.Parallel()
	domain, _, fakeOps := buildParamsetFixture(t)
	fakeOps.getParamsetDescriptionFn = func(context.Context, string, hmenum.ParamsetKey) (map[string]hmproto.ParameterData, error) {
		rw := hmenum.OperationsRead | hmenum.OperationsWrite
		return map[string]hmproto.ParameterData{
			"SAME":    {Type: hmenum.ParameterTypeInteger, Operations: rw},
			"MISSING": {Type: hmenum.ParameterTypeInteger, Operations: rw},
			"GARBLED": {Type: hmenum.ParameterTypeInteger, Operations: rw},
		}, nil
	}
	fakeOps.getParamsetFn = func(context.Context, string, hmenum.ParamsetKey) (map[string]any, error) {
		return map[string]any{"SAME": 5, "GARBLED": "not-a-number"}, nil
	}
	var sent map[string]any
	fakeOps.putParamsetFn = func(_ context.Context, _ string, _ hmenum.ParamsetKey, values map[string]any) error {
		sent = values
		return nil
	}

	report, err := domain.PutParamset(context.Background(), "0001ABCD:9", hmenum.ParamsetKeyMaster,
		map[string]any{"SAME": 5.0, "MISSING": 3.0, "GARBLED": 7.0})
	if err != nil {
		t.Fatalf("PutParamset: %v", err)
	}
	if sent == nil {
		t.Fatal("backend PutParamset was not called — the backend path was not taken")
	}
	wantWritten := []string{"GARBLED", "MISSING", "SAME"}
	if len(report.Written) != 3 || report.Written[0] != wantWritten[0] || report.Written[1] != wantWritten[1] || report.Written[2] != wantWritten[2] {
		t.Fatalf("Written = %v, want %v", report.Written, wantWritten)
	}
	want := []interfaces.ParamsetDivergence{
		{Parameter: "GARBLED", Sent: 7, Stored: "not-a-number"},
		{Parameter: "MISSING", Sent: 3, Stored: nil},
	}
	if len(report.Divergences) != len(want) {
		t.Fatalf("Divergences = %+v, want %+v", report.Divergences, want)
	}
	for i := range want {
		if report.Divergences[i] != want[i] {
			t.Fatalf("Divergences[%d] = %+v, want %+v", i, report.Divergences[i], want[i])
		}
	}
}

// A LINK write compares against the flush read it already performs.
func TestPutLinkParamsetReportsClampedValue(t *testing.T) {
	t.Parallel()
	be := &linkCoercionFakeBackend{stored: map[string]any{
		linkTestParamEnum:    4,
		linkTestParamInteger: 100, // clamped from 150
		linkTestParamBool:    false,
		linkTestParamFloat:   1.5,
	}}
	reg, w := buildLinkCoercionFixture(t, be)
	domain := NewParamsetsDomain(reg, w)

	report, err := domain.PutLinkParamset(context.Background(), "LNK0001:4", "PEER0001:1", linkParamsetTestValues())
	if err != nil {
		t.Fatalf("PutLinkParamset: %v", err)
	}
	want := interfaces.ParamsetDivergence{Parameter: linkTestParamInteger, Sent: 150, Stored: 100}
	if report == nil || len(report.Divergences) != 1 || report.Divergences[0] != want {
		t.Fatalf("report = %+v, want exactly [%+v]", report, want)
	}
	if len(report.Written) != 4 {
		t.Fatalf("Written = %v, want the four sent names", report.Written)
	}
}

// A LINK write whose flush read fails still succeeds and reports why the
// comparison is missing.
func TestPutLinkParamsetReadbackErrorIsReported(t *testing.T) {
	t.Parallel()
	be := &linkCoercionFakeBackend{storedErr: errors.New("timeout")}
	reg, w := buildLinkCoercionFixture(t, be)
	domain := NewParamsetsDomain(reg, w)

	report, err := domain.PutLinkParamset(context.Background(), "LNK0001:4", "PEER0001:1", linkParamsetTestValues())
	if err != nil {
		t.Fatalf("PutLinkParamset: %v", err)
	}
	if !be.putCalled {
		t.Fatal("backend PutLinkParamset was not called")
	}
	if report == nil || report.ReadbackError != "timeout" {
		t.Fatalf("report = %+v, want ReadbackError \"timeout\"", report)
	}
}

func TestParamValuesEqual(t *testing.T) {
	t.Parallel()
	cases := []struct {
		a, b any
		want bool
	}{
		{1.0, 1.0 + 1e-12, true},
		{1.0, 1.001, false},
		{1, 1.0, false},
		{5, 5, true},
		{"a", "a", true},
		{true, false, false},
		{nil, nil, true},
		{[]string{"x", "y"}, []string{"x", "y"}, true},
		{[]string{"x"}, []string{"y"}, false},
		{"x", []string{"x"}, false},
	}
	for _, tc := range cases {
		if got := paramValuesEqual(tc.a, tc.b); got != tc.want {
			t.Errorf("paramValuesEqual(%#v, %#v) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}
