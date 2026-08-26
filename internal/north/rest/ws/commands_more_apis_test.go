// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

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
	res := r.Dispatch(opCtx(), "paramset.copy", raw)
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
	res := r.Dispatch(opCtx(), "paramset.copy", raw)
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
	res := r.Dispatch(opCtx(), "paramset.copy", raw)
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
	res := r.Dispatch(opCtx(), "paramset.copy", raw)
	if res.Error == nil {
		t.Fatal("expected error when write fails")
	}
}

func TestParamsetCopy_RequiresAddresses(t *testing.T) {
	rw := &stubParamsetReaderWriter{}
	r := newParamsetCopyRouter(rw)

	// Missing both addresses.
	raw, _ := json.Marshal(map[string]any{})
	res := r.Dispatch(opCtx(), "paramset.copy", raw)
	if res.Error == nil {
		t.Fatal("expected error for missing addresses")
	}

	// Missing target only.
	raw, _ = json.Marshal(map[string]any{"source_channel_address": "A:1"})
	res = r.Dispatch(opCtx(), "paramset.copy", raw)
	if res.Error == nil {
		t.Fatal("expected error for missing target")
	}
}

// TestParamsetCopy_StubbedWhenReaderOrWriterNil verifies that
// paramset.copy stays registered — and therefore visible in
// system.commands — even when its provider is only half-wired,
// answering CommandErrorNotImplemented rather than
// CommandErrorUnknownCommand. A command a client cannot tell apart
// from a typo (the two error codes read identically as "no such
// command") hides an availability gap the schema declares as real;
// see the sibling stub commands in commands_missing.go for the same
// convention.
func TestParamsetCopy_StubbedWhenReaderOrWriterNil(t *testing.T) {
	rw := &stubParamsetReaderWriter{}

	// Only ParamsetReader wired, no ParamsetWriter → stubbed, not absent.
	r1 := NewRouter()
	RegisterExtendedCommands(r1, ExtendedCommandsConfig{ParamsetReader: rw})
	raw, _ := json.Marshal(map[string]any{"source_channel_address": "A:1", "target_channel_address": "B:1"})
	if res := r1.Dispatch(opCtx(), "paramset.copy", raw); res.Error == nil || res.Error.Code != CommandErrorNotImplemented {
		t.Fatalf("paramset.copy without a writer should be a stub (not_implemented), got %+v", res.Error)
	}

	// Only ParamsetWriter wired, no ParamsetReader → stubbed, not absent.
	r2 := NewRouter()
	RegisterExtendedCommands(r2, ExtendedCommandsConfig{Paramsets: rw})
	if res := r2.Dispatch(opCtx(), "paramset.copy", raw); res.Error == nil || res.Error.Code != CommandErrorNotImplemented {
		t.Fatalf("paramset.copy without a reader should be a stub (not_implemented), got %+v", res.Error)
	}
}

// TestExtendedCommandsStubEveryOptionalProviderCommandWhenUnwired pins
// the fix for the set of RegisterExtendedCommands commands whose
// domain provider is optional (nil in a deployment that does not wire
// it): each one must stay registered — visible in system.commands,
// dispatchable — and answer CommandErrorNotImplemented rather than
// disappearing from the router and being indistinguishable from a
// misspelled command name (CommandErrorUnknownCommand).
func TestExtendedCommandsStubEveryOptionalProviderCommandWhenUnwired(t *testing.T) {
	t.Parallel()
	commands := []string{
		"change_history.list", "change_history.clear",
		"central.info", "central.connectivity", "central.system_health", "central.reconcile",
		"ccu.throttle_stats", "ccu.device_statistics",
		"incidents.list", "incidents.get", "incidents.clear",
		"service_messages.disable", "service_messages.suppressed", "service_messages.unsuppress",
		"paramset.form_schema", "paramset.copy",
	}
	r := NewRouter()
	RegisterExtendedCommands(r, ExtendedCommandsConfig{})
	for _, cmd := range commands {
		res := r.Dispatch(opCtx(), cmd, nil)
		if res.Error == nil {
			t.Errorf("%s: expected an error with no provider wired, got a result", cmd)
			continue
		}
		if res.Error.Code != CommandErrorNotImplemented {
			t.Errorf("%s: code=%q, want %q (stub, not unregistered)", cmd, res.Error.Code, CommandErrorNotImplemented)
		}
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
