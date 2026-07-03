// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Tests for the reload_device_config, reload_channel_config, and
// config.session.save WS commands.
package ws

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/configui"
)

// ─── stubs ────────────────────────────────────────────────────────────────────

// stubDeviceReloader implements [DeviceReloader].
type stubDeviceReloader struct {
	calledWith string
	err        error
}

func (s *stubDeviceReloader) ReloadDeviceConfig(_ context.Context, address string) error {
	s.calledWith = address
	return s.err
}

// stubConstraintProvider implements [ConstraintProvider].
type stubConstraintProvider struct {
	constraints []configui.CrossValidationConstraint
	err         error
}

func (s *stubConstraintProvider) Constraints(_ context.Context, _ configui.SessionKey) ([]configui.CrossValidationConstraint, error) {
	return s.constraints, s.err
}

// ─── L-7002: config.reload_device_config ─────────────────────────────────────

func TestReloadDeviceConfig_Success(t *testing.T) {
	stub := &stubDeviceReloader{}
	r := NewRouter()
	RegisterDefaultCommands(r, DefaultCommandsConfig{DeviceReloader: stub})

	raw, _ := json.Marshal(map[string]any{"device_address": "ABC0001"})
	res := r.Dispatch(opCtx(), "config.reload_device_config", raw)
	if res.Error != nil {
		t.Fatalf("unexpected error: %v", res.Error)
	}
	out := res.Data.(map[string]any)
	if out["success"] != true {
		t.Fatalf("expected success=true, got %v", out)
	}
	if out["device_address"] != "ABC0001" {
		t.Fatalf("expected device_address=ABC0001, got %v", out["device_address"])
	}
	if stub.calledWith != "ABC0001" {
		t.Fatalf("domain not called with correct address: %s", stub.calledWith)
	}
}

func TestReloadDeviceConfig_MissingAddress(t *testing.T) {
	stub := &stubDeviceReloader{}
	r := NewRouter()
	RegisterDefaultCommands(r, DefaultCommandsConfig{DeviceReloader: stub})

	raw, _ := json.Marshal(map[string]any{})
	res := r.Dispatch(opCtx(), "config.reload_device_config", raw)
	if res.Error == nil {
		t.Fatal("expected error for missing device_address")
	}
	if res.Error.Code != CommandErrorBadRequest {
		t.Fatalf("expected bad_request, got %s", res.Error.Code)
	}
}

func TestReloadDeviceConfig_DomainError(t *testing.T) {
	stub := &stubDeviceReloader{err: errors.New("CCU unreachable")}
	r := NewRouter()
	RegisterDefaultCommands(r, DefaultCommandsConfig{DeviceReloader: stub})

	raw, _ := json.Marshal(map[string]any{"device_address": "ABC0001"})
	res := r.Dispatch(opCtx(), "config.reload_device_config", raw)
	if res.Error == nil {
		t.Fatal("expected error from domain")
	}
	if res.Error.Code != CommandErrorInternal {
		t.Fatalf("expected internal error, got %s", res.Error.Code)
	}
}

func TestReloadDeviceConfig_NotRegisteredWhenNilReloader(t *testing.T) {
	r := NewRouter()
	RegisterDefaultCommands(r, DefaultCommandsConfig{}) // no DeviceReloader

	raw, _ := json.Marshal(map[string]any{"device_address": "ABC0001"})
	res := r.Dispatch(opCtx(), "config.reload_device_config", raw)
	if res.Error == nil || res.Error.Code != CommandErrorUnknownCommand {
		t.Fatalf("expected unknown_command when reloader not wired, got %v", res.Error)
	}
}

// ─── L-7002: ccu.reload_device_config (panel variant) ────────────────────────

func TestCCUReloadDeviceConfig_Success(t *testing.T) {
	stub := &stubDeviceReloader{}
	r := NewRouter()
	RegisterDefaultCommands(r, DefaultCommandsConfig{DeviceReloader: stub})

	raw, _ := json.Marshal(map[string]any{"device_address": "DEF0002"})
	res := r.Dispatch(opCtx(), "ccu.reload_device_config", raw)
	if res.Error != nil {
		t.Fatalf("unexpected error: %v", res.Error)
	}
	if stub.calledWith != "DEF0002" {
		t.Fatalf("panel variant did not call domain: %s", stub.calledWith)
	}
}

func TestBothReloadVariantsRegistered(t *testing.T) {
	stub := &stubDeviceReloader{}
	r := NewRouter()
	RegisterDefaultCommands(r, DefaultCommandsConfig{DeviceReloader: stub})

	registered := make(map[string]bool)
	for _, n := range r.Commands() {
		registered[n] = true
	}
	for _, cmd := range []string{"config.reload_device_config", "ccu.reload_device_config"} {
		if !registered[cmd] {
			t.Errorf("command %q not registered", cmd)
		}
	}
}

// ─── config.reload_channel_config ────────────────────────────────────────────

// stubChannelReloader implements [ChannelReloader].
type stubChannelReloader struct {
	calledWith string
	err        error
}

func (s *stubChannelReloader) ReloadChannelConfig(_ context.Context, channelAddress string) error {
	s.calledWith = channelAddress
	return s.err
}

func TestReloadChannelConfig_Success(t *testing.T) {
	stub := &stubChannelReloader{}
	r := NewRouter()
	RegisterDefaultCommands(r, DefaultCommandsConfig{ChannelReloader: stub})

	raw, _ := json.Marshal(map[string]any{"channel_address": "ABC0001:1"})
	res := r.Dispatch(opCtx(), "config.reload_channel_config", raw)
	if res.Error != nil {
		t.Fatalf("unexpected error: %v", res.Error)
	}
	out := res.Data.(map[string]any)
	if out["success"] != true {
		t.Fatalf("expected success=true, got %v", out)
	}
	if out["channel_address"] != "ABC0001:1" {
		t.Fatalf("expected channel_address=ABC0001:1, got %v", out["channel_address"])
	}
	if stub.calledWith != "ABC0001:1" {
		t.Fatalf("domain not called with correct address: %s", stub.calledWith)
	}
}

func TestReloadChannelConfig_AddressAlias(t *testing.T) {
	stub := &stubChannelReloader{}
	r := NewRouter()
	RegisterDefaultCommands(r, DefaultCommandsConfig{ChannelReloader: stub})

	// Send only the "address" alias key — channel_address should fall back to it.
	raw, _ := json.Marshal(map[string]any{"address": "ABC0001:2"})
	res := r.Dispatch(opCtx(), "config.reload_channel_config", raw)
	if res.Error != nil {
		t.Fatalf("unexpected error using address alias: %v", res.Error)
	}
	if stub.calledWith != "ABC0001:2" {
		t.Fatalf("alias not resolved to domain: got %q", stub.calledWith)
	}
}

func TestReloadChannelConfig_MissingAddress(t *testing.T) {
	stub := &stubChannelReloader{}
	r := NewRouter()
	RegisterDefaultCommands(r, DefaultCommandsConfig{ChannelReloader: stub})

	raw, _ := json.Marshal(map[string]any{})
	res := r.Dispatch(opCtx(), "config.reload_channel_config", raw)
	if res.Error == nil {
		t.Fatal("expected error for missing channel_address")
	}
	if res.Error.Code != CommandErrorBadRequest {
		t.Fatalf("expected bad_request, got %s", res.Error.Code)
	}
}

func TestReloadChannelConfig_DomainError(t *testing.T) {
	stub := &stubChannelReloader{err: errors.New("CCU unreachable")}
	r := NewRouter()
	RegisterDefaultCommands(r, DefaultCommandsConfig{ChannelReloader: stub})

	raw, _ := json.Marshal(map[string]any{"channel_address": "ABC0001:1"})
	res := r.Dispatch(opCtx(), "config.reload_channel_config", raw)
	if res.Error == nil {
		t.Fatal("expected error from domain")
	}
	if res.Error.Code != CommandErrorInternal {
		t.Fatalf("expected internal error, got %s", res.Error.Code)
	}
}

func TestReloadChannelConfig_NotRegisteredWhenNilReloader(t *testing.T) {
	r := NewRouter()
	RegisterDefaultCommands(r, DefaultCommandsConfig{}) // no ChannelReloader

	raw, _ := json.Marshal(map[string]any{"channel_address": "ABC0001:1"})
	res := r.Dispatch(opCtx(), "config.reload_channel_config", raw)
	if res.Error == nil || res.Error.Code != CommandErrorUnknownCommand {
		t.Fatalf("expected unknown_command when reloader not wired, got %v", res.Error)
	}
}

// ─── ccu.reload_channel_config (panel variant) ───────────────────────────────

func TestCCUReloadChannelConfig_Success(t *testing.T) {
	stub := &stubChannelReloader{}
	r := NewRouter()
	RegisterDefaultCommands(r, DefaultCommandsConfig{ChannelReloader: stub})

	raw, _ := json.Marshal(map[string]any{"channel_address": "DEF0002:3"})
	res := r.Dispatch(opCtx(), "ccu.reload_channel_config", raw)
	if res.Error != nil {
		t.Fatalf("unexpected error: %v", res.Error)
	}
	if stub.calledWith != "DEF0002:3" {
		t.Fatalf("panel variant did not call domain: %s", stub.calledWith)
	}
}

func TestBothChannelReloadVariantsRegistered(t *testing.T) {
	stub := &stubChannelReloader{}
	r := NewRouter()
	RegisterDefaultCommands(r, DefaultCommandsConfig{ChannelReloader: stub})

	registered := make(map[string]bool)
	for _, n := range r.Commands() {
		registered[n] = true
	}
	for _, cmd := range []string{"config.reload_channel_config", "ccu.reload_channel_config"} {
		if !registered[cmd] {
			t.Errorf("command %q not registered", cmd)
		}
	}
}

// ─── L-7009: session save + cross-validation constraints ─────────────────────

// newSessionRouterWithConstraints creates a router wired with both a
// session backend and a constraint provider.
func newSessionRouterWithConstraints(t *testing.T, backend *stubBackend, cp ConstraintProvider) (*Router, *configui.SessionStore) {
	t.Helper()
	store := configui.NewSessionStore()
	r := NewRouter()
	RegisterDefaultCommands(r, DefaultCommandsConfig{
		Sessions:       store,
		SessionBackend: backend,
		Constraints:    cp,
	})
	return r, store
}

func TestSessionSavePassesWithNoConstraints(t *testing.T) {
	backend := &stubBackend{openInitial: map[string]any{"TEMPERATURE": float64(20)}}
	r, _ := newSessionRouterWithConstraints(t, backend, nil) // nil cp → no validation

	r.Dispatch(opCtx(), "config.session.open", sessionArgs("addr:1", "MASTER"))
	setArgs, _ := json.Marshal(map[string]any{
		"central_name": "test", "channel_address": "addr:1", "paramset_key": "MASTER",
		"parameter": "TEMPERATURE", "value": float64(22),
	})
	r.Dispatch(opCtx(), "config.session.set", setArgs)

	res := r.Dispatch(opCtx(), "config.session.save", sessionArgs("addr:1", "MASTER"))
	if res.Error != nil {
		t.Fatalf("save with nil constraints should succeed: %v", res.Error)
	}
}

func TestSessionSavePassesWithSatisfiedConstraints(t *testing.T) {
	backend := &stubBackend{openInitial: map[string]any{"MIN_TEMP": float64(10), "MAX_TEMP": float64(25)}}
	// Constraint: MAX_TEMP >= MIN_TEMP.
	cp := &stubConstraintProvider{constraints: []configui.CrossValidationConstraint{
		{Rule: "gte", ParamA: "MAX_TEMP", ParamB: "MIN_TEMP", AppliesToParams: []string{"MAX_TEMP", "MIN_TEMP"}},
	}}
	r, _ := newSessionRouterWithConstraints(t, backend, cp)

	r.Dispatch(opCtx(), "config.session.open", sessionArgs("addr:1", "MASTER"))
	// Change MAX_TEMP to 30 — still satisfies MAX_TEMP >= MIN_TEMP.
	setArgs, _ := json.Marshal(map[string]any{
		"central_name": "test", "channel_address": "addr:1", "paramset_key": "MASTER",
		"parameter": "MAX_TEMP", "value": float64(30),
	})
	r.Dispatch(opCtx(), "config.session.set", setArgs)

	res := r.Dispatch(opCtx(), "config.session.save", sessionArgs("addr:1", "MASTER"))
	if res.Error != nil {
		t.Fatalf("save should succeed when constraints are satisfied: %v", res.Error)
	}
	if backend.saved["MAX_TEMP"] != float64(30) {
		t.Fatalf("expected MAX_TEMP=30 in saved values, got %v", backend.saved)
	}
}

func TestSessionSaveBlockedByConstraintViolation(t *testing.T) {
	backend := &stubBackend{openInitial: map[string]any{"MIN_TEMP": float64(10), "MAX_TEMP": float64(25)}}
	// Constraint: MAX_TEMP >= MIN_TEMP. We will violate it.
	cp := &stubConstraintProvider{constraints: []configui.CrossValidationConstraint{
		{Rule: "gte", ParamA: "MAX_TEMP", ParamB: "MIN_TEMP", AppliesToParams: []string{"MAX_TEMP", "MIN_TEMP"}},
	}}
	r, _ := newSessionRouterWithConstraints(t, backend, cp)

	r.Dispatch(opCtx(), "config.session.open", sessionArgs("addr:1", "MASTER"))
	// Set MAX_TEMP below MIN_TEMP — violates the constraint.
	setArgs, _ := json.Marshal(map[string]any{
		"central_name": "test", "channel_address": "addr:1", "paramset_key": "MASTER",
		"parameter": "MAX_TEMP", "value": float64(5),
	})
	r.Dispatch(opCtx(), "config.session.set", setArgs)

	res := r.Dispatch(opCtx(), "config.session.save", sessionArgs("addr:1", "MASTER"))
	if res.Error == nil {
		t.Fatal("save should be blocked when constraints are violated")
	}
	if res.Error.Code != "validation_error" {
		t.Fatalf("expected validation_error, got %s", res.Error.Code)
	}
	// Backend must NOT have been called.
	if backend.saved != nil {
		t.Fatalf("backend should not be called on validation failure, got %v", backend.saved)
	}
}

func TestSessionSaveConstraintProviderError(t *testing.T) {
	backend := &stubBackend{openInitial: map[string]any{"A": float64(1)}}
	cp := &stubConstraintProvider{err: errors.New("schema unavailable")}
	r, _ := newSessionRouterWithConstraints(t, backend, cp)

	r.Dispatch(opCtx(), "config.session.open", sessionArgs("addr:1", "MASTER"))
	setArgs, _ := json.Marshal(map[string]any{
		"central_name": "test", "channel_address": "addr:1", "paramset_key": "MASTER",
		"parameter": "A", "value": float64(2),
	})
	r.Dispatch(opCtx(), "config.session.set", setArgs)

	res := r.Dispatch(opCtx(), "config.session.save", sessionArgs("addr:1", "MASTER"))
	if res.Error == nil {
		t.Fatal("save should fail when constraint provider returns error")
	}
	if res.Error.Code != CommandErrorInternal {
		t.Fatalf("expected internal error, got %s", res.Error.Code)
	}
}
