// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package valve

import (
	"testing"

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

// --- Irrigation ---

func TestIrrigationHADiscoveryPayload_NilReceiverReturnsNil(t *testing.T) {
	t.Parallel()
	var v *Irrigation
	comp, body := haEntity(t, v.HADiscoveryEntity(), discoveryCtx{})
	if comp != "" || body != nil {
		t.Fatalf("nil receiver: want (\"\", nil), got (%q, %v)", comp, body)
	}
}

func TestIrrigationHADiscoveryPayload_Component(t *testing.T) {
	t.Parallel()
	v := newTestIrrigation(t, "HmIP-IRRIG:3", &stubWriter{})
	comp, body := haEntity(t, v.HADiscoveryEntity(), discoveryCtx{})
	if comp != "valve" {
		t.Fatalf("component = %q, want %q", comp, "valve")
	}
	if body == nil {
		t.Fatal("body must not be nil")
	}
}

func TestIrrigationHADiscoveryPayload_RequiredKeys(t *testing.T) {
	t.Parallel()
	v := newTestIrrigation(t, "HmIP-IRRIG:3", &stubWriter{})
	ctx := discoveryCtx{}
	_, body := haEntity(t, v.HADiscoveryEntity(), ctx)

	for _, key := range []string{
		"command_topic",
		"state_topic",
	} {
		if _, ok := body[key]; !ok {
			t.Errorf("missing required key %q", key)
		}
	}
}

func TestIrrigationHADiscoveryPayload_TopicValues(t *testing.T) {
	t.Parallel()
	v := newTestIrrigation(t, "HmIP-IRRIG:3", &stubWriter{})
	ctx := discoveryCtx{}
	_, body := haEntity(t, v.HADiscoveryEntity(), ctx)

	// Irrigation uses STATE (boolean) matching STATE (valve.py:35).
	wantCmd := ctx.WireParameterCommandTopic("", "STATE")
	if got, _ := body["command_topic"].(string); got != wantCmd {
		t.Errorf("command_topic = %q, want %q", got, wantCmd)
	}
	if got, _ := body["state_topic"].(string); got != ctx.CustomDPStateTopic() {
		t.Errorf("state_topic = %q, want %q", got, ctx.CustomDPStateTopic())
	}
}

// --- Modulating ---

func TestModulatingHADiscoveryPayload_NilReceiverReturnsNil(t *testing.T) {
	t.Parallel()
	var v *Modulating
	comp, body := haEntity(t, v.HADiscoveryEntity(), discoveryCtx{})
	if comp != "" || body != nil {
		t.Fatalf("nil receiver: want (\"\", nil), got (%q, %v)", comp, body)
	}
}

func TestModulatingHADiscoveryPayload_Component(t *testing.T) {
	t.Parallel()
	v := newTestModulating(t, "x", &stubWriter{})
	comp, body := haEntity(t, v.HADiscoveryEntity(), discoveryCtx{})
	if comp != "valve" {
		t.Fatalf("component = %q, want %q", comp, "valve")
	}
	if body == nil {
		t.Fatal("body must not be nil")
	}
}

func TestModulatingHADiscoveryPayload_RequiredKeys(t *testing.T) {
	t.Parallel()
	v := newTestModulating(t, "x", &stubWriter{})
	ctx := discoveryCtx{}
	_, body := haEntity(t, v.HADiscoveryEntity(), ctx)

	for _, key := range []string{
		"command_topic",
		"state_topic",
	} {
		if _, ok := body[key]; !ok {
			t.Errorf("missing required key %q", key)
		}
	}
}

func TestModulatingHADiscoveryPayload_TopicValues(t *testing.T) {
	t.Parallel()
	v := newTestModulating(t, "x", &stubWriter{})
	ctx := discoveryCtx{}
	_, body := haEntity(t, v.HADiscoveryEntity(), ctx)

	wantCmd := ctx.ServiceMethodCommandTopic("set_level")
	if got, _ := body["command_topic"].(string); got != wantCmd {
		t.Errorf("command_topic = %q, want %q", got, wantCmd)
	}
	if got, _ := body["state_topic"].(string); got != ctx.CustomDPStateTopic() {
		t.Errorf("state_topic = %q, want %q", got, ctx.CustomDPStateTopic())
	}
}
