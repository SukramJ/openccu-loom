// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package lock

import (
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/payload"
)

// discoveryCtx is a minimal stub for [payload.HADiscoveryTopics] used in
// entity-builder smoke tests. It returns stable, testable topic strings.
type discoveryCtx struct{}

func (discoveryCtx) CustomDPStateTopic() string   { return "test/custom/state" }
func (discoveryCtx) CustomDPCommandTopic() string { return "test/custom/state/set" }

// ServiceMethodCommandTopic is what the render context builds on its own out
// of [discoveryCtx.CustomDPCommandTopic]; it is spelled here so an assertion
// can name a method topic without repeating the join.
func (c discoveryCtx) ServiceMethodCommandTopic(method string) string {
	return c.CustomDPCommandTopic() + "/" + method
}

func (discoveryCtx) WireParameterCommandTopic(channelAddress, parameter string) string {
	if channelAddress == "" {
		return "test/" + parameter + "/set"
	}
	return "test/" + channelAddress + "/" + parameter + "/set"
}

func (discoveryCtx) WireParameterStateTopic(channelAddress, parameter string) string {
	if channelAddress == "" {
		return "test/" + parameter
	}
	return "test/" + channelAddress + "/" + parameter
}

func (discoveryCtx) DeviceAvailabilityTopic() string { return "test/availability" }
func (discoveryCtx) BridgeStatusTopic() string       { return "test/bridge/status" }

// compile-time check: discoveryCtx satisfies payload.HADiscoveryTopics.
var _ payload.HADiscoveryTopics = discoveryCtx{}

func TestLockHADiscoveryPayload_NilReceiverReturnsNil(t *testing.T) {
	t.Parallel()
	var l *Lock
	comp, body := haEntity(t, l.HADiscoveryEntity(), discoveryCtx{})
	if comp != "" || body != nil {
		t.Fatalf("nil receiver: want (\"\", nil), got (%q, %v)", comp, body)
	}
}

// TestLockHADiscoveryPayload_WithoutTopicsStillDescribes pins what a missing transport does to
// an entity: nothing. The entity is its description and its bindings, and it
// is complete before any topic is resolved — a renderer with no layout simply
// produces no topic strings.
func TestLockHADiscoveryPayload_WithoutTopicsStillDescribes(t *testing.T) {
	t.Parallel()
	r := newRig(t, "HmIP-DLD:1", KindIP, &stubWriter{}, custom.LockCapabilities{SupportsOpen: true})
	comp, body := haEntity(t, r.lock.HADiscoveryEntity(), nil)
	if comp != "lock" {
		t.Fatalf("component = %q, want lock", comp)
	}
	for key := range body {
		if strings.HasSuffix(key, "_topic") {
			t.Errorf("%s present with no topic layout: %v", key, body[key])
		}
	}
}

func TestLockHADiscoveryPayload_Component(t *testing.T) {
	t.Parallel()
	r := newRig(t, "HmIP-DLD:1", KindIP, &stubWriter{}, custom.LockCapabilities{SupportsOpen: true})
	comp, body := haEntity(t, r.lock.HADiscoveryEntity(), discoveryCtx{})
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
	_, body := haEntity(t, r.lock.HADiscoveryEntity(), discoveryCtx{})

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
	_, body := haEntity(t, r.lock.HADiscoveryEntity(), ctx)

	if v, _ := body["state_topic"].(string); v != ctx.CustomDPStateTopic() {
		t.Errorf("state_topic = %q, want %q", v, ctx.CustomDPStateTopic())
	}
	wantCmd := ctx.WireParameterCommandTopic("", "LOCK_TARGET_LEVEL")
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
	_, body := haEntity(t, r.lock.HADiscoveryEntity(), ctx)

	wantCmd := ctx.WireParameterCommandTopic("", "STATE")
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
	_, body := haEntity(t, r.lock.HADiscoveryEntity(), ctx)

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
		_, body := haEntity(t, r.lock.HADiscoveryEntity(), discoveryCtx{})
		_, hasOpen := body["payload_open"]
		if hasOpen != tc.wantOpen {
			t.Errorf("kind=%d supOpen=%v: payload_open present=%v, want %v", tc.kind, tc.supOpen, hasOpen, tc.wantOpen)
		}
	}
}

// TestLockHADiscoveryPayload_LockUnlockPayloads pins that an IP lock
// advertises the LOCK_TARGET_LEVEL ENUM labels, i.e. exactly the tokens
// [Lock.sendIP] writes. It previously pinned the positional indices
// "0"/"1", which restated the VALUE_LIST order that neither the payload
// builder nor the discovery context can read.
func TestLockHADiscoveryPayload_LockUnlockPayloads(t *testing.T) {
	t.Parallel()
	r := newRig(t, "HmIP-DLD:1", KindIP, &stubWriter{}, custom.LockCapabilities{SupportsOpen: true})
	_, body := haEntity(t, r.lock.HADiscoveryEntity(), discoveryCtx{})

	if v, _ := body["payload_lock"].(string); v != ipTargetLocked {
		t.Errorf("payload_lock = %q, want %q", v, ipTargetLocked)
	}
	if v, _ := body["payload_unlock"].(string); v != ipTargetUnlocked {
		t.Errorf("payload_unlock = %q, want %q", v, ipTargetUnlocked)
	}
	if v, _ := body["payload_open"].(string); v != ipTargetOpen {
		t.Errorf("payload_open = %q, want %q", v, ipTargetOpen)
	}
}
