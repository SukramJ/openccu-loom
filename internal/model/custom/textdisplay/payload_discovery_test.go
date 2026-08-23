// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package textdisplay

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
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

// TestTextDisplayHACommandCarriesRowID pins that the HA text entity can
// actually write. HA's text platform publishes the bare string the
// operator typed; the `write` service method addresses one of the
// display's rows and returns before touching the device when no id is
// present, so the discovery payload has to template the id in.
func TestTextDisplayHACommandCarriesRowID(t *testing.T) {
	t.Parallel()
	td := New("VCU3756007:3", &stubWriter{})
	_, body := td.HADiscoveryPayload(discoveryCtx{})

	tmpl, ok := body["command_template"].(string)
	if !ok {
		t.Fatal("command_template missing — HA would publish a bare string the write method rejects")
	}
	if !strings.Contains(tmpl, `"id"`) {
		t.Errorf("command_template %q carries no row id", tmpl)
	}
	if !strings.Contains(tmpl, "value | tojson") {
		t.Errorf("command_template %q must JSON-quote the operator's text", tmpl)
	}
}

// TestTextDisplayWriteAcceptsTemplatedPayload feeds the object the
// command template renders through the invoke path the MQTT bridge uses
// and asserts it reaches the wire.
func TestTextDisplayWriteAcceptsTemplatedPayload(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	td := New("VCU3756007:3", w)

	// What HA publishes after rendering the command template.
	var params map[string]any
	if err := json.Unmarshal([]byte(`{"id": 1, "text": "Hello"}`), &params); err != nil {
		t.Fatal(err)
	}
	if err := td.Invoke(context.Background(), "write", params, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("write: %v", err)
	}
	if len(w.calls) == 0 {
		t.Fatal("write produced no wire call")
	}
}
