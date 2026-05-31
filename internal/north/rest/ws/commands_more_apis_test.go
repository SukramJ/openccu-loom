// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Tests for parity-audit item L-7015: paramset.copy WS-command.
// Generic paramset-to-paramset mirror — reads a paramset from the
// source channel and writes it to the target channel.
// Mirrors Python ws_copy_paramset (websocket_api.py:916).
package ws

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/configui"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// ─── stubs ────────────────────────────────────────────────────────────────────

// stubParamsetReaderWriter satisfies both [ParamsetReader] and [ParamsetWriter].
type stubParamsetReaderWriter struct {
	readValues map[string]any
	readErr    error
	writeCalls []struct {
		Key    configui.SessionKey
		Values map[string]any
	}
	writeErr error
}

func (s *stubParamsetReaderWriter) GetParamset(_ context.Context, _ configui.SessionKey) (map[string]any, error) {
	if s.readErr != nil {
		return nil, s.readErr
	}
	return s.readValues, nil
}

func (s *stubParamsetReaderWriter) PutParamset(_ context.Context, key configui.SessionKey, values map[string]any) error {
	if s.writeErr != nil {
		return s.writeErr
	}
	s.writeCalls = append(s.writeCalls, struct {
		Key    configui.SessionKey
		Values map[string]any
	}{Key: key, Values: values})
	return nil
}

// ─── helpers ─────────────────────────────────────────────────────────────────

func newParamsetCopyRouter(rw *stubParamsetReaderWriter) *Router {
	r := NewRouter()
	RegisterExtendedCommands(r, ExtendedCommandsConfig{
		Paramsets:      rw, // satisfies ParamsetWriter
		ParamsetReader: rw, // satisfies ParamsetReader
	})
	return r
}

func dispatchCopy(t *testing.T, r *Router, src, dst, psKey string) map[string]any {
	t.Helper()
	raw, _ := json.Marshal(map[string]any{
		"source_channel_address": src,
		"target_channel_address": dst,
		"paramset_key":           psKey,
	})
	res := r.Dispatch(context.Background(), "paramset.copy", raw)
	if res.Error != nil {
		t.Fatalf("paramset.copy: unexpected error: %v", res.Error)
	}
	b, _ := json.Marshal(res.Data)
	var out map[string]any
	_ = json.Unmarshal(b, &out)
	return out
}

// ─── tests ────────────────────────────────────────────────────────────────────

func TestParamsetCopy_CopiesSourceToTarget(t *testing.T) {
	rw := &stubParamsetReaderWriter{
		readValues: map[string]any{"TEMPERATURE": float64(20), "MODE": float64(1)},
	}
	r := newParamsetCopyRouter(rw)

	out := dispatchCopy(t, r, "ABC0001:1", "DEF0002:1", "MASTER")
	if out["copied"].(float64) != 2 {
		t.Fatalf("expected copied=2, got %v", out["copied"])
	}
	if out["source"] != "ABC0001:1" {
		t.Fatalf("unexpected source: %v", out["source"])
	}
	if out["target"] != "DEF0002:1" {
		t.Fatalf("unexpected target: %v", out["target"])
	}
	if len(rw.writeCalls) != 1 {
		t.Fatalf("expected 1 write call, got %d", len(rw.writeCalls))
	}
	call := rw.writeCalls[0]
	if call.Key.ChannelAddress != "DEF0002:1" {
		t.Fatalf("write called with wrong address: %s", call.Key.ChannelAddress)
	}
	if call.Key.ParamsetKey != hmenum.ParamsetKeyMaster {
		t.Fatalf("write called with wrong paramset key: %s", call.Key.ParamsetKey)
	}
}

func TestParamsetCopy_DefaultsToMASTER(t *testing.T) {
	rw := &stubParamsetReaderWriter{
		readValues: map[string]any{"X": 1},
	}
	r := NewRouter()
	RegisterExtendedCommands(r, ExtendedCommandsConfig{
		Paramsets:      rw,
		ParamsetReader: rw,
	})

	raw, _ := json.Marshal(map[string]any{
		"source_channel_address": "A:1",
		"target_channel_address": "B:1",
		// no paramset_key → defaults to MASTER
	})
	res := r.Dispatch(context.Background(), "paramset.copy", raw)
	if res.Error != nil {
		t.Fatalf("unexpected error: %v", res.Error)
	}
	if len(rw.writeCalls) != 1 {
		t.Fatalf("expected 1 write call, got %d", len(rw.writeCalls))
	}
	if rw.writeCalls[0].Key.ParamsetKey != hmenum.ParamsetKeyMaster {
		t.Fatalf("expected MASTER, got %s", rw.writeCalls[0].Key.ParamsetKey)
	}
}

func TestParamsetCopy_EmptySourceSkipsWrite(t *testing.T) {
	rw := &stubParamsetReaderWriter{readValues: map[string]any{}}
	r := newParamsetCopyRouter(rw)

	out := dispatchCopy(t, r, "A:1", "B:1", "MASTER")
	if out["copied"].(float64) != 0 {
		t.Fatalf("expected copied=0 for empty source, got %v", out["copied"])
	}
	if len(rw.writeCalls) != 0 {
		t.Fatalf("expected no write calls for empty source, got %d", len(rw.writeCalls))
	}
}

func TestParamsetCopy_ReadErrorPropagates(t *testing.T) {
	rw := &stubParamsetReaderWriter{readErr: errors.New("not found")}
	r := newParamsetCopyRouter(rw)

	raw, _ := json.Marshal(map[string]any{
		"source_channel_address": "A:1",
		"target_channel_address": "B:1",
	})
	res := r.Dispatch(context.Background(), "paramset.copy", raw)
	if res.Error == nil {
		t.Fatal("expected error when read fails")
	}
}

func TestParamsetCopy_WriteErrorPropagates(t *testing.T) {
	rw := &stubParamsetReaderWriter{
		readValues: map[string]any{"A": 1},
		writeErr:   errors.New("CCU busy"),
	}
	r := newParamsetCopyRouter(rw)

	raw, _ := json.Marshal(map[string]any{
		"source_channel_address": "A:1",
		"target_channel_address": "B:1",
	})
	res := r.Dispatch(context.Background(), "paramset.copy", raw)
	if res.Error == nil {
		t.Fatal("expected error when write fails")
	}
}

func TestParamsetCopy_RequiresAddresses(t *testing.T) {
	rw := &stubParamsetReaderWriter{}
	r := newParamsetCopyRouter(rw)

	// Missing both addresses.
	raw, _ := json.Marshal(map[string]any{})
	res := r.Dispatch(context.Background(), "paramset.copy", raw)
	if res.Error == nil {
		t.Fatal("expected error for missing addresses")
	}

	// Missing target only.
	raw, _ = json.Marshal(map[string]any{"source_channel_address": "A:1"})
	res = r.Dispatch(context.Background(), "paramset.copy", raw)
	if res.Error == nil {
		t.Fatal("expected error for missing target")
	}
}

func TestParamsetCopy_NotRegisteredWhenReaderOrWriterNil(t *testing.T) {
	rw := &stubParamsetReaderWriter{}

	// Only ParamsetReader wired, no ParamsetWriter → not registered.
	r1 := NewRouter()
	RegisterExtendedCommands(r1, ExtendedCommandsConfig{ParamsetReader: rw})
	raw, _ := json.Marshal(map[string]any{"source_channel_address": "A:1", "target_channel_address": "B:1"})
	if res := r1.Dispatch(context.Background(), "paramset.copy", raw); res.Error == nil || res.Error.Code != CommandErrorUnknownCommand {
		t.Fatal("paramset.copy should not be registered without both reader and writer")
	}

	// Only ParamsetWriter wired, no ParamsetReader → not registered.
	r2 := NewRouter()
	RegisterExtendedCommands(r2, ExtendedCommandsConfig{Paramsets: rw})
	if res := r2.Dispatch(context.Background(), "paramset.copy", raw); res.Error == nil || res.Error.Code != CommandErrorUnknownCommand {
		t.Fatal("paramset.copy should not be registered without both reader and writer")
	}
}

func TestParamsetCopy_RegistersWhenReaderAndWriterPresent(t *testing.T) {
	// Regression guard: paramset.copy must be registered when both
	// ParamsetReader and ParamsetWriter are wired (L-7015 audit).
	rw := &stubParamsetReaderWriter{readValues: map[string]any{"X": 1}}
	r := NewRouter()
	RegisterExtendedCommands(r, ExtendedCommandsConfig{
		Paramsets:      rw,
		ParamsetReader: rw,
	})

	cmds := make(map[string]bool)
	for _, c := range r.Commands() {
		cmds[c] = true
	}
	if !cmds["paramset.copy"] {
		t.Error("paramset.copy should be registered")
	}
}
