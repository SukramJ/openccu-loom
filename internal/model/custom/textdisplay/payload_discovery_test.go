// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package textdisplay

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

func TestTextDisplayHADiscoveryPayload_NilReceiverReturnsNil(t *testing.T) {
	t.Parallel()
	var td *TextDisplay
	comp, body := td.HADiscoveryPayload(discoveryCtx{})
	if comp != "" || body != nil {
		t.Fatalf("nil receiver: want (\"\", nil), got (%q, %v)", comp, body)
	}
}

func TestTextDisplayHADiscoveryPayload_NilContextReturnsNil(t *testing.T) {
	t.Parallel()
	td := New("VCU3756007:3", &stubWriter{})
	comp, body := td.HADiscoveryPayload(nil)
	if comp != "" || body != nil {
		t.Fatalf("nil ctx: want (\"\", nil), got (%q, %v)", comp, body)
	}
}

func TestTextDisplayHADiscoveryPayload_Component(t *testing.T) {
	t.Parallel()
	td := New("VCU3756007:3", &stubWriter{})
	comp, body := td.HADiscoveryPayload(discoveryCtx{})
	if comp != "text" {
		t.Fatalf("component = %q, want %q", comp, "text")
	}
	if body == nil {
		t.Fatal("body must not be nil")
	}
}

func TestTextDisplayHADiscoveryPayload_RequiredKeys(t *testing.T) {
	t.Parallel()
	td := New("VCU3756007:3", &stubWriter{})
	_, body := td.HADiscoveryPayload(discoveryCtx{})

	for _, key := range []string{
		"command_topic",
		"mode",
		"max",
	} {
		if _, ok := body[key]; !ok {
			t.Errorf("missing required key %q", key)
		}
	}
}

func TestTextDisplayHADiscoveryPayload_CommandTopic(t *testing.T) {
	t.Parallel()
	td := New("VCU3756007:3", &stubWriter{})
	ctx := discoveryCtx{}
	_, body := td.HADiscoveryPayload(ctx)

	wantCmd := ctx.ServiceMethodCommandTopic("write")
	if v, _ := body["command_topic"].(string); v != wantCmd {
		t.Errorf("command_topic = %q, want %q", v, wantCmd)
	}
}

func TestTextDisplayHADiscoveryPayload_ModeAndMax(t *testing.T) {
	t.Parallel()
	td := New("VCU3756007:3", &stubWriter{})
	_, body := td.HADiscoveryPayload(discoveryCtx{})

	if v, _ := body["mode"].(string); v != "text" {
		t.Errorf("mode = %q, want %q", v, "text")
	}
	maxVal, ok := body["max"]
	if !ok {
		t.Fatal("max key missing")
	}
	// max must be a positive number (64 per spec).
	switch v := maxVal.(type) {
	case int:
		if v <= 0 {
			t.Errorf("max = %d, want > 0", v)
		}
	case int32:
		if v <= 0 {
			t.Errorf("max = %d, want > 0", v)
		}
	case int64:
		if v <= 0 {
			t.Errorf("max = %d, want > 0", v)
		}
	case float64:
		if v <= 0 {
			t.Errorf("max = %v, want > 0", v)
		}
	default:
		t.Errorf("max has unexpected type %T (value %v)", maxVal, maxVal)
	}
}

func TestTextDisplayHADiscoveryPayload_StateTopicPresent(t *testing.T) {
	t.Parallel()
	td := New("VCU3756007:3", &stubWriter{})
	ctx := discoveryCtx{}
	_, body := td.HADiscoveryPayload(ctx)

	if _, ok := body["state_topic"]; !ok {
		t.Error("missing state_topic")
	}
	if v, _ := body["state_topic"].(string); v != ctx.CustomDPStateTopic() {
		t.Errorf("state_topic = %q, want %q", v, ctx.CustomDPStateTopic())
	}
}
