// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// wave8_new_items_test.go — tests for A6 SessionRecorder items:
// Delete, PeekTS, GetLatestResponseByParams

package session

import (
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Recorder.Delete
// ---------------------------------------------------------------------------

func TestRecorderDeleteExists(t *testing.T) {
	t.Parallel()
	r := New(Config{Active: true})
	r.RecordResponse(RPCTypeJSON, "getValue", "param1", "response1")

	deleted := r.Delete(RPCTypeJSON, "getValue", "param1")
	if !deleted {
		t.Error("Delete must return true when an entry existed")
	}
	if _, ok := r.Get(RPCTypeJSON, "getValue", "param1"); ok {
		t.Error("deleted entry must not be retrievable via Get")
	}
}

func TestRecorderDeleteMissing(t *testing.T) {
	t.Parallel()
	r := New(Config{Active: true})

	deleted := r.Delete(RPCTypeJSON, "getValue", "nonexistent")
	if deleted {
		t.Error("Delete must return false when no entry exists")
	}
}

func TestRecorderDeleteDoesNotAffectOtherEntries(t *testing.T) {
	t.Parallel()
	r := New(Config{Active: true})
	r.RecordResponse(RPCTypeJSON, "getValue", "param1", "resp1")
	r.RecordResponse(RPCTypeJSON, "getValue", "param2", "resp2")

	r.Delete(RPCTypeJSON, "getValue", "param1")

	if _, ok := r.Get(RPCTypeJSON, "getValue", "param2"); !ok {
		t.Error("Delete must not remove other entries with different params")
	}
}

// ---------------------------------------------------------------------------
// Recorder.PeekTS
// ---------------------------------------------------------------------------

func TestRecorderPeekTSExists(t *testing.T) {
	t.Parallel()
	r := New(Config{Active: true})
	before := time.Now()
	r.RecordResponse(RPCTypeXML, "listDevices", nil, []any{"dev1"})
	after := time.Now()

	ts, ok := r.PeekTS(RPCTypeXML, "listDevices", nil)
	if !ok {
		t.Fatal("PeekTS must return true for an existing entry")
	}
	if ts.Before(before) || ts.After(after) {
		t.Errorf("PeekTS returned timestamp %v outside expected range [%v, %v]", ts, before, after)
	}
}

func TestRecorderPeekTSMissing(t *testing.T) {
	t.Parallel()
	r := New(Config{Active: true})

	ts, ok := r.PeekTS(RPCTypeXML, "listDevices", nil)
	if ok {
		t.Error("PeekTS must return false for a missing entry")
	}
	if !ts.IsZero() {
		t.Error("PeekTS must return zero time for a missing entry")
	}
}

func TestRecorderPeekTSDoesNotConsume(t *testing.T) {
	t.Parallel()
	r := New(Config{Active: true})
	r.RecordResponse(RPCTypeXML, "listDevices", nil, "result")

	_, _ = r.PeekTS(RPCTypeXML, "listDevices", nil)
	// Entry must still be retrievable after PeekTS.
	if _, ok := r.Get(RPCTypeXML, "listDevices", nil); !ok {
		t.Error("PeekTS must not consume the entry; it must still be available via Get")
	}
}

// ---------------------------------------------------------------------------
// Recorder.GetLatestResponseByParams
// ---------------------------------------------------------------------------

func TestRecorderGetLatestResponseByParamsMatch(t *testing.T) {
	t.Parallel()
	r := New(Config{Active: true})
	r.RecordResponse(RPCTypeJSON, "getValue", "channel:1:SET_TEMPERATURE", 21.5)

	resp, ok := r.GetLatestResponseByParams(RPCTypeJSON, "getValue", "SET_TEMPERATURE")
	if !ok {
		t.Fatal("GetLatestResponseByParams must find entry with matching params substring")
	}
	if resp != 21.5 {
		t.Errorf("response=%v want 21.5", resp)
	}
}

func TestRecorderGetLatestResponseByParamsNoMatch(t *testing.T) {
	t.Parallel()
	r := New(Config{Active: true})
	r.RecordResponse(RPCTypeJSON, "getValue", "channel:1:SET_TEMPERATURE", 21.5)

	_, ok := r.GetLatestResponseByParams(RPCTypeJSON, "getValue", "NONEXISTENT_PARAM")
	if ok {
		t.Error("GetLatestResponseByParams must return false when no params match")
	}
}

func TestRecorderGetLatestResponseByParamsEmptySubstr(t *testing.T) {
	t.Parallel()
	r := New(Config{Active: true})
	r.RecordResponse(RPCTypeJSON, "getValue", "channel:1:SET_TEMPERATURE", 21.5)
	r.RecordResponse(RPCTypeJSON, "getValue", "channel:2:TEMPERATURE", 19.0)

	// Empty substring should match any entry.
	resp, ok := r.GetLatestResponseByParams(RPCTypeJSON, "getValue", "")
	if !ok {
		t.Fatal("GetLatestResponseByParams with empty substring must match at least one entry")
	}
	_ = resp
}

func TestRecorderGetLatestResponseByParamsReturnsLatest(t *testing.T) {
	t.Parallel()
	r := New(Config{Active: true})
	// Record two entries with the same substring; the second is newer.
	r.RecordResponse(RPCTypeJSON, "getValue", "channel:1:LEVEL", 0.0)
	time.Sleep(2 * time.Millisecond) // ensure distinct timestamps
	r.RecordResponse(RPCTypeJSON, "getValue", "channel:2:LEVEL", 1.0)

	resp, ok := r.GetLatestResponseByParams(RPCTypeJSON, "getValue", "LEVEL")
	if !ok {
		t.Fatal("GetLatestResponseByParams must return an entry")
	}
	if resp != 1.0 {
		t.Errorf("expected latest response 1.0, got %v", resp)
	}
}

// ---------------------------------------------------------------------------
// ParamsetStore.IsInMultipleChannels — tested via sqlite test helper
// (DB-based: lives in sqlite package; tested in paramsets_test.go context)
// ---------------------------------------------------------------------------
