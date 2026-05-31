// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package valve

import (
	"testing"

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

var _ payload.HADiscoveryContext = discoveryCtx{}

// --- Irrigation ---

func TestIrrigationHADiscoveryPayload_NilReceiverReturnsNil(t *testing.T) {
	t.Parallel()
	var v *Irrigation
	comp, body := v.HADiscoveryPayload(discoveryCtx{})
	if comp != "" || body != nil {
		t.Fatalf("nil receiver: want (\"\", nil), got (%q, %v)", comp, body)
	}
}

func TestIrrigationHADiscoveryPayload_Component(t *testing.T) {
	t.Parallel()
	v := newTestIrrigation(t, "HmIP-IRRIG:3", &stubWriter{})
	comp, body := v.HADiscoveryPayload(discoveryCtx{})
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
	_, body := v.HADiscoveryPayload(ctx)

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
	_, body := v.HADiscoveryPayload(ctx)

	// Irrigation uses STATE (boolean) matching STATE (valve.py:35).
	wantCmd := ctx.WireParameterCommandTopic("STATE")
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
	comp, body := v.HADiscoveryPayload(discoveryCtx{})
	if comp != "" || body != nil {
		t.Fatalf("nil receiver: want (\"\", nil), got (%q, %v)", comp, body)
	}
}

func TestModulatingHADiscoveryPayload_Component(t *testing.T) {
	t.Parallel()
	v := newTestModulating(t, "x", &stubWriter{})
	comp, body := v.HADiscoveryPayload(discoveryCtx{})
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
	_, body := v.HADiscoveryPayload(ctx)

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
	_, body := v.HADiscoveryPayload(ctx)

	wantCmd := ctx.ServiceMethodCommandTopic("set_level")
	if got, _ := body["command_topic"].(string); got != wantCmd {
		t.Errorf("command_topic = %q, want %q", got, wantCmd)
	}
	if got, _ := body["state_topic"].(string); got != ctx.CustomDPStateTopic() {
		t.Errorf("state_topic = %q, want %q", got, ctx.CustomDPStateTopic())
	}
}
