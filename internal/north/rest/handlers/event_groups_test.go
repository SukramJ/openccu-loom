// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	modevent "github.com/SukramJ/openccu-loom/internal/model/event"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// ---------------------------------------------------------------------------
// toEventGroupSummary — unit tests (whitebox)
// ---------------------------------------------------------------------------

func TestToEventGroupSummary_KeypressGroup(t *testing.T) {
	t.Parallel()

	g := modevent.NewGroup("0001ABCD:1", modevent.KindKeypress)
	short := modevent.NewSource("0001ABCD:1", hmenum.ParameterPressShort)
	long := modevent.NewSource("0001ABCD:1", hmenum.ParameterPressLong)
	g.Add(short)
	g.Add(long)

	s := toEventGroupSummary(g, "")

	if s.Kind != "keypress" {
		t.Errorf("Kind = %q, want %q", s.Kind, "keypress")
	}
	if s.ChannelAddress != "0001ABCD:1" {
		t.Errorf("ChannelAddress = %q, want %q", s.ChannelAddress, "0001ABCD:1")
	}
	if !s.Available {
		t.Error("Available must be true when no delegate is set")
	}
	// event_types must be lowercased and sorted
	if len(s.EventTypes) != 2 {
		t.Fatalf("EventTypes len = %d, want 2", len(s.EventTypes))
	}
	if s.EventTypes[0] != "press_long" || s.EventTypes[1] != "press_short" {
		t.Errorf("EventTypes = %v, want [press_long press_short]", s.EventTypes)
	}
	// parameters must be upper-case and sorted
	if len(s.Parameters) != 2 {
		t.Fatalf("Parameters len = %d, want 2", len(s.Parameters))
	}
	for _, p := range s.Parameters {
		for _, c := range p {
			if c >= 'a' && c <= 'z' {
				t.Errorf("parameter %q must be upper-case", p)
			}
		}
	}
	if s.Parameters[0] != "PRESS_LONG" || s.Parameters[1] != "PRESS_SHORT" {
		t.Errorf("Parameters = %v, want [PRESS_LONG PRESS_SHORT]", s.Parameters)
	}
	// no fire yet → LastTriggeredEvent must be nil
	if s.LastTriggeredEvent != nil {
		t.Errorf("LastTriggeredEvent must be nil before any fire, got %+v", s.LastTriggeredEvent)
	}
}

func TestToEventGroupSummary_LastTriggeredEvent_SetAfterFire(t *testing.T) {
	t.Parallel()

	g := modevent.NewGroup("0001ABCD:1", modevent.KindKeypress)
	src := modevent.NewSource("0001ABCD:1", hmenum.ParameterPressShort)
	g.Add(src)

	before := time.Now().UTC().Add(-time.Second)
	src.Fire(true)
	after := time.Now().UTC().Add(time.Second)

	s := toEventGroupSummary(g, "")

	if s.LastTriggeredEvent == nil {
		t.Fatal("LastTriggeredEvent must not be nil after fire")
	}
	if s.LastTriggeredEvent.Parameter != "press_short" {
		t.Errorf("Parameter = %q, want %q", s.LastTriggeredEvent.Parameter, "press_short")
	}
	// TriggeredAt must be a valid RFC3339 timestamp within the fire window.
	at, err := time.Parse(time.RFC3339, s.LastTriggeredEvent.TriggeredAt)
	if err != nil {
		t.Fatalf("TriggeredAt %q is not RFC3339: %v", s.LastTriggeredEvent.TriggeredAt, err)
	}
	if at.Before(before) || at.After(after) {
		t.Errorf("TriggeredAt %v not in [%v, %v]", at, before, after)
	}
}

func TestToEventGroupSummary_NoFire_LastTriggeredEventNil(t *testing.T) {
	t.Parallel()

	g := modevent.NewGroup("0001ABCD:1", modevent.KindKeypress)
	src := modevent.NewSource("0001ABCD:1", hmenum.ParameterPressShort)
	g.Add(src)

	s := toEventGroupSummary(g, "")
	if s.LastTriggeredEvent != nil {
		t.Errorf("LastTriggeredEvent must be nil before any fire, got %+v", s.LastTriggeredEvent)
	}
}

func TestToEventGroupSummary_EmptyGroup_EventTypesIsEmptySlice(t *testing.T) {
	t.Parallel()

	g := modevent.NewGroup("0001ABCD:1", modevent.KindKeypress)
	s := toEventGroupSummary(g, "")

	if s.EventTypes == nil {
		t.Error("EventTypes must not be nil — handler marshals it as []")
	}
	if len(s.EventTypes) != 0 {
		t.Errorf("EventTypes = %v, want empty", s.EventTypes)
	}
}

// ---------------------------------------------------------------------------
// ListEventGroups handler tests
// ---------------------------------------------------------------------------

func TestListEventGroups_NoGroups_Returns200EmptyArray(t *testing.T) {
	t.Parallel()

	d := newTestDevice("0001ABCD", "HmIP-BSM")
	d.AddChannel("0001ABCD:1", 1, "KEY", hmenum.ParamsetKeyValues)
	idx := &stubDeviceIndex{devices: map[string]*device.Device{"0001ABCD": d}}

	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "0001ABCD", "no": "1"}))
	w := httptest.NewRecorder()
	ListEventGroups(idx).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	// Must be a JSON array (possibly empty), not null.
	body := strings.TrimSpace(w.Body.String())
	if body != "[]" {
		// Might also be a non-empty valid JSON array from other paths — just
		// ensure it deserialises as an array.
		var out []EventGroupSummary
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatalf("expected JSON array, got: %s (err: %v)", body, err)
		}
		if len(out) != 0 {
			t.Fatalf("expected empty array, got %d entries", len(out))
		}
	}
}

func TestListEventGroups_WithKeypressGroup_Returns200WithSummary(t *testing.T) {
	t.Parallel()

	d := newTestDevice("0001ABCD", "HmIP-RC2")
	ch := d.AddChannel("0001ABCD:1", 1, "KEY", hmenum.ParamsetKeyValues)
	short := modevent.NewSource("0001ABCD:1", hmenum.ParameterPressShort)
	long := modevent.NewSource("0001ABCD:1", hmenum.ParameterPressLong)
	ch.AttachGenericEvent(short)
	ch.AttachGenericEvent(long)
	ch.BuildEventGroups("")

	idx := &stubDeviceIndex{devices: map[string]*device.Device{"0001ABCD": d}}

	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "0001ABCD", "no": "1"}))
	w := httptest.NewRecorder()
	ListEventGroups(idx).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var out []EventGroupSummary
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("expected 1 group, got %d: %+v", len(out), out)
	}
	g := out[0]
	if g.Kind != "keypress" {
		t.Errorf("Kind = %q, want %q", g.Kind, "keypress")
	}
	if !g.Available {
		t.Error("Available must be true")
	}
	if len(g.EventTypes) != 2 {
		t.Fatalf("EventTypes len = %d, want 2", len(g.EventTypes))
	}
	for _, et := range g.EventTypes {
		for _, c := range et {
			if c >= 'A' && c <= 'Z' {
				t.Errorf("EventTypes entry %q must be lowercase", et)
			}
		}
	}
	if len(g.Parameters) != 2 {
		t.Fatalf("Parameters len = %d, want 2", len(g.Parameters))
	}
}

func TestListEventGroups_UnknownDevice_Returns404(t *testing.T) {
	t.Parallel()

	idx := &stubDeviceIndex{devices: map[string]*device.Device{}}

	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req = req.WithContext(chiContext(req, map[string]string{"addr": "MISSING", "no": "1"}))
	w := httptest.NewRecorder()
	ListEventGroups(idx).ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d body=%s", w.Code, w.Body.String())
	}
}

// TestToEventGroupSummary_UniqueID verifies that toEventGroupSummary stamps a
// loom_-prefixed unique_id, and that Group.CanonicalUniqueID is non-empty
// for a non-nil group. The group address is device-bound and unique, so no
// serial prefix is needed for collision avoidance — any non-nil group returns
// a loom_ key regardless of serialSuffix.
func TestToEventGroupSummary_UniqueID(t *testing.T) {
	t.Parallel()
	g := modevent.NewGroup("0001ABCD:1", modevent.KindKeypress)
	src := modevent.NewSource("0001ABCD:1", hmenum.ParameterPressShort)
	g.Add(src)

	t.Run("with serialSuffix produces loom_ prefix", func(t *testing.T) {
		t.Parallel()
		s := toEventGroupSummary(g, "vccu0000000")
		if s.UniqueID == "" {
			t.Fatal("UniqueID must not be empty when serialSuffix is set")
		}
		if !strings.HasPrefix(s.UniqueID, "loom_") {
			t.Errorf("UniqueID = %q, want loom_ prefix", s.UniqueID)
		}
	})

	t.Run("CanonicalUniqueID on non-nil group returns non-empty loom_ key", func(t *testing.T) {
		t.Parallel()
		got := g.CanonicalUniqueID("vccu0000000")
		if got == "" {
			t.Fatal("CanonicalUniqueID must not be empty for a non-nil group")
		}
		if !strings.HasPrefix(got, "loom_") {
			t.Errorf("CanonicalUniqueID = %q, want loom_ prefix", got)
		}
	})

	t.Run("CanonicalUniqueID on nil group returns empty string", func(t *testing.T) {
		t.Parallel()
		var nilGroup *modevent.Group
		got := nilGroup.CanonicalUniqueID("vccu0000000")
		if got != "" {
			t.Errorf("CanonicalUniqueID on nil group = %q, want empty string", got)
		}
	})
}
