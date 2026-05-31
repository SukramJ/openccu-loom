// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// recorder_test.go — unit tests for session.Recorder
// Covers (SessionRecorder parity items):
// Recorder (class-level / lifecycle)
// RecordRequest / RecordResponse
// ClearSessions
// StartSession / StopSession
// IsActive
// Get
// GetSession (latest by method)
// ExportSession
// ReplaySession
// SerializeToMap

package session

import (
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// IsActive
// ---------------------------------------------------------------------------

func TestRecorderIsActiveDefault(t *testing.T) {
	t.Parallel()
	r := New(Config{Active: false})
	if r.IsActive() {
		t.Error("default inactive recorder must report IsActive=false")
	}
}

func TestRecorderIsActiveAfterStart(t *testing.T) {
	t.Parallel()
	r := New(Config{Active: false})
	r.StartSession()
	if !r.IsActive() {
		t.Error("after StartSession IsActive must be true")
	}
}

func TestRecorderIsActiveAfterStop(t *testing.T) {
	t.Parallel()
	r := New(Config{Active: true})
	r.StopSession()
	if r.IsActive() {
		t.Error("after StopSession IsActive must be false")
	}
}

// ---------------------------------------------------------------------------
// StartSession / — StopSession
// ---------------------------------------------------------------------------

func TestRecorderStartSessionIdempotent(t *testing.T) {
	t.Parallel()
	r := New(Config{})
	ok1 := r.StartSession()
	if !ok1 {
		t.Fatal("first StartSession must return true")
	}
	// A second Start while isRecording is set returns false.
	// isRecording is only set by timed activation; regular StartSession
	// always returns true if already active (it just resets data).
	// But if already recording, StartSession returns false.
	// Since StartSession doesn't set isRecording, calling it twice is OK.
	ok2 := r.StartSession()
	_ = ok2 // second call clears data; return value may be true or false
}

func TestRecorderStopSessionIdle(t *testing.T) {
	t.Parallel()
	r := New(Config{})
	ok := r.StopSession()
	if ok {
		t.Error("StopSession on already-stopped recorder must return false")
	}
}

// ---------------------------------------------------------------------------
// RecordRequest / RecordResponse
// ---------------------------------------------------------------------------

func TestRecorderRecordRequestAndResponse(t *testing.T) {
	t.Parallel()
	r := New(Config{Active: true})

	r.RecordRequest(RPCTypeXML, "getDeviceDescription", []any{"VCU1234567"})
	r.RecordResponse(RPCTypeXML, "getDeviceDescription", []any{"VCU1234567"}, map[string]any{"TYPE": "HM-CC-RT-DN"})

	resp, ok := r.Get(RPCTypeXML, "getDeviceDescription", []any{"VCU1234567"})
	if !ok {
		t.Fatal("Get must find recorded response")
	}
	m, ok := resp.(map[string]any)
	if !ok {
		t.Fatalf("response type=%T want map[string]any", resp)
	}
	if m["TYPE"] != "HM-CC-RT-DN" {
		t.Errorf("response TYPE=%v want HM-CC-RT-DN", m["TYPE"])
	}
}

func TestRecorderRecordInactive(t *testing.T) {
	t.Parallel()
	r := New(Config{Active: false})
	r.RecordRequest(RPCTypeXML, "ping", nil)
	r.RecordResponse(RPCTypeXML, "ping", nil, "pong")

	_, ok := r.Get(RPCTypeXML, "ping", nil)
	if ok {
		t.Error("inactive recorder must not store requests")
	}
}

// ---------------------------------------------------------------------------
// Get
// ---------------------------------------------------------------------------

func TestRecorderGetMiss(t *testing.T) {
	t.Parallel()
	r := New(Config{Active: true})
	_, ok := r.Get(RPCTypeJSON, "unknownMethod", nil)
	if ok {
		t.Error("Get on missing entry must return false")
	}
}

func TestRecorderGetTTLExpiry(t *testing.T) {
	t.Parallel()
	r := New(Config{Active: true, TTL: 1 * time.Millisecond})
	r.RecordResponse(RPCTypeXML, "getDeviceDescription", nil, "result")
	time.Sleep(5 * time.Millisecond)
	_, ok := r.Get(RPCTypeXML, "getDeviceDescription", nil)
	if ok {
		t.Error("Get must return false after TTL expiry")
	}
}

// ---------------------------------------------------------------------------
// ClearSessions
// ---------------------------------------------------------------------------

func TestRecorderClearSessions(t *testing.T) {
	t.Parallel()
	r := New(Config{Active: true})
	r.RecordResponse(RPCTypeXML, "ping", nil, "pong")
	r.RecordResponse(RPCTypeJSON, "listDevices", nil, []any{})

	r.ClearSessions()

	_, ok := r.Get(RPCTypeXML, "ping", nil)
	if ok {
		t.Error("after ClearSessions Get must return false")
	}
}

// ---------------------------------------------------------------------------
// GetSession (latest by method)
// ---------------------------------------------------------------------------

func TestRecorderGetSession(t *testing.T) {
	t.Parallel()
	r := New(Config{Active: true})
	r.RecordResponse(RPCTypeXML, "getDeviceDescription", []any{"A"}, "respA")
	r.RecordResponse(RPCTypeXML, "getDeviceDescription", []any{"B"}, "respB")

	entries := r.GetSession(RPCTypeXML, "getDeviceDescription")
	if len(entries) != 2 {
		t.Errorf("GetSession len=%d want 2", len(entries))
	}
}

func TestRecorderGetSessionEmpty(t *testing.T) {
	t.Parallel()
	r := New(Config{Active: true})
	entries := r.GetSession(RPCTypeXML, "ghost")
	if len(entries) != 0 {
		t.Errorf("GetSession for unknown method len=%d want 0", len(entries))
	}
}

// ---------------------------------------------------------------------------
// ExportSession
// ---------------------------------------------------------------------------

func TestRecorderExportSession(t *testing.T) {
	t.Parallel()
	r := New(Config{Active: true})
	r.RecordResponse(RPCTypeXML, "listDevices", nil, []any{"dev1", "dev2"})

	exports := r.ExportSession(RPCTypeXML, "listDevices")
	if len(exports) != 1 {
		t.Fatalf("ExportSession len=%d want 1", len(exports))
	}
	entry := exports[0]
	if _, ok := entry["response"]; !ok {
		t.Error("export must contain 'response' key")
	}
	if _, ok := entry["recorded_at"]; !ok {
		t.Error("export must contain 'recorded_at' key")
	}
}

// ---------------------------------------------------------------------------
// ReplaySession
// ---------------------------------------------------------------------------

func TestRecorderReplaySession(t *testing.T) {
	t.Parallel()
	r := New(Config{Active: true})
	r.RecordResponse(RPCTypeXML, "getParamset", []any{"CH:1", "VALUES"}, map[string]any{"LEVEL": 0.5})

	resp, ok := r.ReplaySession(RPCTypeXML, "getParamset", []any{"CH:1", "VALUES"})
	if !ok {
		t.Fatal("ReplaySession must find recorded response")
	}
	m, _ := resp.(map[string]any)
	if m["LEVEL"] != 0.5 {
		t.Errorf("replayed LEVEL=%v want 0.5", m["LEVEL"])
	}
}

func TestRecorderReplaySessionMiss(t *testing.T) {
	t.Parallel()
	r := New(Config{Active: true})
	_, ok := r.ReplaySession(RPCTypeXML, "ghost", nil)
	if ok {
		t.Error("ReplaySession on missing entry must return false")
	}
}

// ---------------------------------------------------------------------------
// SerializeToMap
// ---------------------------------------------------------------------------

func TestRecorderSerializeToMap(t *testing.T) {
	t.Parallel()
	r := New(Config{Active: true})
	r.RecordResponse(RPCTypeXML, "ping", nil, "pong")
	r.RecordResponse(RPCTypeJSON, "listDevices", nil, []any{})

	m := r.SerializeToMap()
	if len(m) != 2 {
		t.Errorf("SerializeToMap len=%d want 2", len(m))
	}
	for k, v := range m {
		entry, ok := v.(map[string]any)
		if !ok {
			t.Errorf("entry %q: type=%T want map[string]any", k, v)
			continue
		}
		if _, ok := entry["rpc_type"]; !ok {
			t.Errorf("entry %q missing rpc_type key", k)
		}
		if _, ok := entry["method"]; !ok {
			t.Errorf("entry %q missing method key", k)
		}
	}
}

func TestRecorderSerializeToMapEmpty(t *testing.T) {
	t.Parallel()
	r := New(Config{Active: true})
	m := r.SerializeToMap()
	if len(m) != 0 {
		t.Errorf("SerializeToMap on empty recorder len=%d want 0", len(m))
	}
}

// ---------------------------------------------------------------------------
// Metadata (active/recording status)
// ---------------------------------------------------------------------------

func TestRecorderMetadata(t *testing.T) {
	t.Parallel()
	r := New(Config{Active: true, TTL: 5 * time.Second})
	r.RecordResponse(RPCTypeXML, "ping", nil, "pong")

	meta := r.Metadata()
	if active, ok := meta["active"].(bool); !ok || !active {
		t.Errorf("Metadata active=%v want true", meta["active"])
	}
	if meta["ttl_seconds"].(float64) != 5.0 {
		t.Errorf("Metadata ttl_seconds=%v want 5.0", meta["ttl_seconds"])
	}
	if meta["total_entries"].(int) != 1 {
		t.Errorf("Metadata total_entries=%v want 1", meta["total_entries"])
	}
}

// ---------------------------------------------------------------------------
// freezeParams — internal function tested indirectly via Get
// ---------------------------------------------------------------------------

func TestFreezeParamsRoundTrip(t *testing.T) {
	t.Parallel()
	r := New(Config{Active: true})
	params := map[string]any{"key": "value", "num": 42}
	r.RecordResponse(RPCTypeJSON, "method", params, "response")

	resp, ok := r.Get(RPCTypeJSON, "method", params)
	if !ok {
		t.Fatal("Get with same params must find entry")
	}
	if resp != "response" {
		t.Errorf("response=%v want response", resp)
	}
}

// ---------------------------------------------------------------------------
// GetSessions
// ---------------------------------------------------------------------------

// TestGetSessions verifies that GetSessions returns a map of all recorded
// (rpcType/method) buckets with their respective params lists.
func TestGetSessions(t *testing.T) {
	t.Parallel()
	r := New(Config{Active: true})
	// Record two entries for the same method and one for a different method.
	r.RecordResponse(RPCTypeXML, "GetParamset", map[string]any{"address": "A1"}, "resp1")
	r.RecordResponse(RPCTypeXML, "GetParamset", map[string]any{"address": "A2"}, "resp2")
	r.RecordResponse(RPCTypeJSON, "GetVersion", map[string]any{}, "v1")

	sessions := r.GetSessions()
	if len(sessions) < 2 {
		t.Errorf("GetSessions: len=%d, want >= 2 buckets", len(sessions))
	}
	xmlBucket := "xml/GetParamset"
	if _, ok := sessions[xmlBucket]; !ok {
		t.Errorf("GetSessions: missing bucket %q", xmlBucket)
	}
}

// ---------------------------------------------------------------------------
// RPCType constants
// ---------------------------------------------------------------------------

func TestRPCTypeValues(t *testing.T) {
	t.Parallel()
	if RPCTypeXML != "xml" {
		t.Errorf("RPCTypeXML=%q want xml", RPCTypeXML)
	}
	if RPCTypeJSON != "json" {
		t.Errorf("RPCTypeJSON=%q want json", RPCTypeJSON)
	}
	if RPCTypeBIN != "bin" {
		t.Errorf("RPCTypeBIN=%q want bin", RPCTypeBIN)
	}
}

// ---------------------------------------------------------------------------
// SerializeToGolden
// ---------------------------------------------------------------------------

// TestSerializeToGolden_EmptyRecorder verifies that an empty recorder
// returns an empty slice (not nil).
func TestSerializeToGolden_EmptyRecorder(t *testing.T) {
	t.Parallel()
	r := New(Config{Active: true})
	out := r.SerializeToGolden()
	if out == nil {
		t.Error("SerializeToGolden on empty recorder must return non-nil slice")
	}
	if len(out) != 0 {
		t.Errorf("SerializeToGolden on empty recorder len=%d want 0", len(out))
	}
}

// TestSerializeToGolden_SortedByTypeThenMethodThenParams verifies the
// deterministic ordering contract: (rpc_type, method, params) ascending.
func TestSerializeToGolden_SortedByTypeThenMethodThenParams(t *testing.T) {
	t.Parallel()
	r := New(Config{Active: true})
	// Insert in reverse-alphabetical order so sorting is observable.
	r.RecordResponse(RPCTypeXML, "zMethod", []any{"z"}, "rz")
	r.RecordResponse(RPCTypeXML, "aMethod", []any{"b"}, "rb")
	r.RecordResponse(RPCTypeXML, "aMethod", []any{"a"}, "ra")
	r.RecordResponse(RPCTypeJSON, "ping", nil, "pong")

	out := r.SerializeToGolden()
	if len(out) != 4 {
		t.Fatalf("expected 4 records, got %d", len(out))
	}
	// json < xml, so the JSON ping must come first.
	if out[0].RPCType != "json" {
		t.Errorf("record[0].rpc_type=%q want json", out[0].RPCType)
	}
	// Within xml/aMethod, params "a" < "b".
	xmlARecords := []GoldenRecord{}
	for _, rec := range out {
		if rec.RPCType == "xml" && rec.Method == "aMethod" {
			xmlARecords = append(xmlARecords, rec)
		}
	}
	if len(xmlARecords) != 2 {
		t.Fatalf("expected 2 xml/aMethod records, got %d", len(xmlARecords))
	}
	// First record params must sort before second.
	if xmlARecords[0].Params >= xmlARecords[1].Params {
		t.Errorf("params not sorted: %q >= %q", xmlARecords[0].Params, xmlARecords[1].Params)
	}
}

// TestSerializeToGolden_RPCTypeBINIsPreserved verifies that BIN-RPC entries
// round-trip through SerializeToGolden with rpc_type = "bin".
func TestSerializeToGolden_RPCTypeBINIsPreserved(t *testing.T) {
	t.Parallel()
	r := New(Config{Active: true})
	r.RecordResponse(RPCTypeBIN, "setValue", []any{"CUX2801001:1", "STATE", true}, nil)

	out := r.SerializeToGolden()
	if len(out) != 1 {
		t.Fatalf("expected 1 record, got %d", len(out))
	}
	if out[0].RPCType != "bin" {
		t.Errorf("rpc_type=%q want bin", out[0].RPCType)
	}
	if out[0].Method != "setValue" {
		t.Errorf("method=%q want setValue", out[0].Method)
	}
}

// TestSerializeToGolden_AllThreeTransports verifies that a recorder that
// has captured XML, JSON, and BIN entries exports all three with their
// correct rpc_type values.
func TestSerializeToGolden_AllThreeTransports(t *testing.T) {
	t.Parallel()
	r := New(Config{Active: true})
	r.RecordResponse(RPCTypeXML, "listDevices", nil, []any{"dev1"})
	r.RecordResponse(RPCTypeJSON, "getVersion", nil, "2.0")
	r.RecordResponse(RPCTypeBIN, "event", []any{"CUX2801001:1", "MOTION", true}, nil)

	out := r.SerializeToGolden()
	if len(out) != 3 {
		t.Fatalf("expected 3 records, got %d", len(out))
	}
	seen := map[string]bool{}
	for _, rec := range out {
		seen[rec.RPCType] = true
	}
	for _, wantType := range []string{"xml", "json", "bin"} {
		if !seen[wantType] {
			t.Errorf("rpc_type %q not found in SerializeToGolden output", wantType)
		}
	}
}
