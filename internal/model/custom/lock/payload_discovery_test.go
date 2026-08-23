// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package lock

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/payload"
)

// discoveryCtx is a minimal stub for payload.HADiscoveryContext used in
// payload-builder smoke tests.
type discoveryCtx struct{}

func (discoveryCtx) AggregatedStateTopic() string { return "test/state" }
func (discoveryCtx) CustomDPStateTopic() string   { return "test/custom/state" }
func (discoveryCtx) ServiceMethodCommandTopic(method string) string {
	return "test/svc/" + method + "/set"
}

func (discoveryCtx) WireParameterCommandTopic(parameter string) string {
	return "test/" + parameter + "/set"
}

func (discoveryCtx) WireParameterStateTopic(parameter string) string {
	return "test/" + parameter
}

func (discoveryCtx) WireParameterStateTopicOn(_, parameter string) string {
	return discoveryCtx{}.WireParameterStateTopic(parameter)
}

var _ payload.HADiscoveryContext = discoveryCtx{}

func TestLockHADiscoveryPayload_NilReceiverReturnsNil(t *testing.T) {
	t.Parallel()
	var l *Lock
	comp, body := l.HADiscoveryPayload(discoveryCtx{})
	if comp != "" || body != nil {
		t.Fatalf("nil receiver: want (\"\", nil), got (%q, %v)", comp, body)
	}
}

func TestLockHADiscoveryPayload_NilContextReturnsNil(t *testing.T) {
	t.Parallel()
	r := newRig(t, "HmIP-DLD:1", KindIP, &stubWriter{}, custom.LockCapabilities{SupportsOpen: true})
	comp, body := r.lock.HADiscoveryPayload(nil)
	if comp != "" || body != nil {
		t.Fatalf("nil ctx: want (\"\", nil), got (%q, %v)", comp, body)
	}
}

func TestLockHADiscoveryPayload_Component(t *testing.T) {
	t.Parallel()
	r := newRig(t, "HmIP-DLD:1", KindIP, &stubWriter{}, custom.LockCapabilities{SupportsOpen: true})
	comp, body := r.lock.HADiscoveryPayload(discoveryCtx{})
	if comp != "lock" {
		t.Fatalf("component = %q, want %q", comp, "lock")
	}
	if body == nil {
		t.Fatal("body must not be nil")
	}
}

func TestLockHADiscoveryPayload_RequiredKeys(t *testing.T) {
	t.Parallel()
	r := newRig(t, "HmIP-DLD:1", KindIP, &stubWriter{}, custom.LockCapabilities{})
	_, body := r.lock.HADiscoveryPayload(discoveryCtx{})

	for _, key := range []string{
		"state_topic",
		"command_topic",
		"value_template",
		"payload_lock",
		"payload_unlock",
	} {
		if _, ok := body[key]; !ok {
			t.Errorf("missing required key %q", key)
		}
	}
}

func TestLockHADiscoveryPayload_TopicValues(t *testing.T) {
	t.Parallel()
	r := newRig(t, "HmIP-DLD:1", KindIP, &stubWriter{}, custom.LockCapabilities{})
	ctx := discoveryCtx{}
	_, body := r.lock.HADiscoveryPayload(ctx)

	if v, _ := body["state_topic"].(string); v != ctx.CustomDPStateTopic() {
		t.Errorf("state_topic = %q, want %q", v, ctx.CustomDPStateTopic())
	}
	wantCmd := ctx.WireParameterCommandTopic("LOCK_TARGET_LEVEL")
	if v, _ := body["command_topic"].(string); v != wantCmd {
		t.Errorf("command_topic = %q, want %q", v, wantCmd)
	}
}

// TestLockHADiscoveryPayload_KindRFUsesState pins that KindRF command_topic
// points at STATE, not LOCK_TARGET_LEVEL. RF locks have no LOCK_TARGET_LEVEL
// wire parameter — pointing at it causes an XML-RPC fault on every HA command.
func TestLockHADiscoveryPayload_KindRFUsesState(t *testing.T) {
	t.Parallel()
	r := newRig(t, "HM-Sec-Key:1", KindRF, &stubWriter{}, custom.LockCapabilities{})
	ctx := discoveryCtx{}
	_, body := r.lock.HADiscoveryPayload(ctx)

	wantCmd := ctx.WireParameterCommandTopic("STATE")
	if v, _ := body["command_topic"].(string); v != wantCmd {
		t.Errorf("KindRF command_topic = %q, want %q", v, wantCmd)
	}
	if v, _ := body["payload_lock"].(string); v != "false" {
		t.Errorf("KindRF payload_lock = %q, want %q", v, "false")
	}
	if v, _ := body["payload_unlock"].(string); v != "true" {
		t.Errorf("KindRF payload_unlock = %q, want %q", v, "true")
	}
}

// TestLockHADiscoveryPayload_KindButtonUsesServiceMethod pins that the
// button-lock command topic is the service-method one. The slot a button
// lock writes is GLOBAL_BUTTON_LOCK in the MASTER paramset, so a
// wire-parameter topic cannot carry it: the VALUES setValue it produces
// faults with XML-RPC -5, and BUTTON_LOCK is not on the channel at all —
// every lock/unlock from Home Assistant was silently lost.
func TestLockHADiscoveryPayload_KindButtonUsesServiceMethod(t *testing.T) {
	t.Parallel()
	r := newRig(t, "HmIP-DLD:0", KindButton, &stubWriter{}, custom.LockCapabilities{})
	ctx := discoveryCtx{}
	_, body := r.lock.HADiscoveryPayload(ctx)

	wantCmd := ctx.ServiceMethodCommandTopic(serviceLockCommand)
	if v, _ := body["command_topic"].(string); v != wantCmd {
		t.Errorf("KindButton command_topic = %q, want %q", v, wantCmd)
	}
	if v, _ := body["payload_lock"].(string); v != commandTokenLock {
		t.Errorf("KindButton payload_lock = %q, want %q", v, commandTokenLock)
	}
	if v, _ := body["payload_unlock"].(string); v != commandTokenUnlock {
		t.Errorf("KindButton payload_unlock = %q, want %q", v, commandTokenUnlock)
	}
}

// TestLockHADiscoveryPayload_PayloadOpenOnlyForIP pins that payload_open is
// only emitted for KindIP locks with SupportsOpen=true.
func TestLockHADiscoveryPayload_PayloadOpenOnlyForIP(t *testing.T) {
	t.Parallel()

	cases := []struct {
		kind     Kind
		supOpen  bool
		wantOpen bool
	}{
		{KindIP, true, true},
		{KindIP, false, false},
		{KindRF, true, false},
		{KindButton, true, false},
	}
	for _, tc := range cases {
		r := newRig(t, "x", tc.kind, &stubWriter{}, custom.LockCapabilities{SupportsOpen: tc.supOpen})
		_, body := r.lock.HADiscoveryPayload(discoveryCtx{})
		_, hasOpen := body["payload_open"]
		if hasOpen != tc.wantOpen {
			t.Errorf("kind=%d supOpen=%v: payload_open present=%v, want %v", tc.kind, tc.supOpen, hasOpen, tc.wantOpen)
		}
	}
}

func TestLockHADiscoveryPayload_LockUnlockPayloads(t *testing.T) {
	t.Parallel()
	r := newRig(t, "HmIP-DLD:1", KindIP, &stubWriter{}, custom.LockCapabilities{})
	_, body := r.lock.HADiscoveryPayload(discoveryCtx{})

	if v, _ := body["payload_lock"].(string); v != "0" {
		t.Errorf("payload_lock = %q, want %q", v, "0")
	}
	if v, _ := body["payload_unlock"].(string); v != "1" {
		t.Errorf("payload_unlock = %q, want %q", v, "1")
	}
}
