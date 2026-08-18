// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package switchdev

import (
	"slices"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// ---------------------------------------------------------------------------
// Minimal stub for payload.HADiscoveryContext
// ---------------------------------------------------------------------------

type stubDiscoveryCtx struct {
	customStateTopic string
	aggStateTopic    string
}

func (s *stubDiscoveryCtx) CustomDPStateTopic() string { return s.customStateTopic }

func (s *stubDiscoveryCtx) AggregatedStateTopic() string { return s.aggStateTopic }

func (s *stubDiscoveryCtx) ServiceMethodCommandTopic(method string) string { return "cmd/" + method }

func (s *stubDiscoveryCtx) WireParameterCommandTopic(param string) string { return "wire/cmd/" + param }

func (s *stubDiscoveryCtx) WireParameterStateTopic(param string) string { return "wire/state/" + param }

func (s *stubDiscoveryCtx) WireParameterStateTopicOn(_, param string) string {
	return s.WireParameterStateTopic(param)
}

func newPayloadSwitch(t *testing.T) *Switch {
	t.Helper()
	addr := "VCU2128127:4"
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "VCU2128127"})
	ch := d.AddChannel(addr, 4, "SWITCH", hmenum.ParamsetKeyValues)
	dp := generic.NewSwitch(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: addr,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterState),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeBool,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
		},
		Writer: &stubWriter{},
	})
	ch.Put(dp)
	return New(ch)
}

// ---------------------------------------------------------------------------
// HADiscoveryPayload
// ---------------------------------------------------------------------------

func TestHADiscoveryPayloadComponentIsSwitch(t *testing.T) {
	s := newPayloadSwitch(t)
	ctx := &stubDiscoveryCtx{customStateTopic: "hm/state", aggStateTopic: "hm/agg"}
	component, body := s.HADiscoveryPayload(ctx)
	if component != "switch" {
		t.Fatalf("expected component 'switch', got %q", component)
	}
	if body == nil {
		t.Fatal("body must not be nil")
	}
}

func TestHADiscoveryPayloadTopicsPopulated(t *testing.T) {
	s := newPayloadSwitch(t)
	ctx := &stubDiscoveryCtx{}
	_, body := s.HADiscoveryPayload(ctx)
	if _, ok := body["command_topic"]; !ok {
		t.Error("missing command_topic")
	}
	if _, ok := body["state_topic"]; !ok {
		t.Error("missing state_topic")
	}
	if _, ok := body["value_template"]; !ok {
		t.Error("missing value_template")
	}
	if body["payload_on"] != "true" {
		t.Errorf("payload_on=%v want 'true'", body["payload_on"])
	}
	if body["payload_off"] != "false" {
		t.Errorf("payload_off=%v want 'false'", body["payload_off"])
	}
}

func TestHADiscoveryPayloadNilSwitchReturnsEmpty(t *testing.T) {
	var s *Switch
	component, body := s.HADiscoveryPayload(&stubDiscoveryCtx{})
	if component != "" || body != nil {
		t.Fatalf("expected empty return from nil switch, got (%q, %v)", component, body)
	}
}

func TestHADiscoveryPayloadNilContextReturnsEmpty(t *testing.T) {
	s := newPayloadSwitch(t)
	component, body := s.HADiscoveryPayload(nil)
	if component != "" || body != nil {
		t.Fatalf("expected empty return from nil context, got (%q, %v)", component, body)
	}
}

// ---------------------------------------------------------------------------
// InfoPayload
// ---------------------------------------------------------------------------

func TestInfoPayloadContainsAddress(t *testing.T) {
	s := newTestSwitch(t, "VCU2128127:4", "Bookshelf", &stubWriter{})
	m, ok := s.Info().(*payload.SwitchInfo)
	if !ok || m == nil {
		t.Fatal("InfoPayload must return a non-nil *payload.SwitchInfo")
	}
	if m.Address != "VCU2128127:4" {
		t.Errorf("address=%v want VCU2128127:4", m.Address)
	}
	if m.Category != "switch" {
		t.Errorf("category=%v want switch", m.Category)
	}
}

func TestInfoPayloadNilSwitchReturnsNil(t *testing.T) {
	var s *Switch
	if s.Info() != nil {
		t.Fatal("expected nil InfoPayload from nil switch")
	}
}

// ---------------------------------------------------------------------------
// ConfigPayload
// ---------------------------------------------------------------------------

func TestConfigPayloadCategoryIsSwitch(t *testing.T) {
	s := newPayloadSwitch(t)
	m, _ := s.Config().(*payload.SwitchConfig)
	if m == nil {
		t.Fatal("ConfigPayload must not be nil")
	}
	if m.Category != "switch" {
		t.Errorf("category=%v want switch", m.Category)
	}
}

func TestConfigPayloadNilSwitchReturnsNil(t *testing.T) {
	var s *Switch
	if s.Config() != nil {
		t.Fatal("expected nil ConfigPayload from nil switch")
	}
}

// ---------------------------------------------------------------------------
// StatePayload
// ---------------------------------------------------------------------------

func TestStatePayloadEmptyBeforeObservation(t *testing.T) {
	s := newPayloadSwitch(t)
	m, ok := s.State().(*payload.SwitchState)
	if !ok || m == nil {
		t.Fatal("StatePayload must return a non-nil *payload.SwitchState")
	}
	// is_on is nil until the data point is observed.
	if m.IsOn != nil {
		t.Errorf("is_on should be nil before observation, got %v", *m.IsOn)
	}
}

func TestStatePayloadAfterTurnOn(t *testing.T) {
	w := &stubWriter{}
	s := newTestSwitch(t, "VCU2128127:4", "", w)
	// Simulate the data point receiving a true value.
	s.OnState(true)
	m, ok := s.State().(*payload.SwitchState)
	if !ok || m == nil {
		t.Fatal("StatePayload must not be nil")
	}
	if m.IsOn == nil {
		t.Fatal("is_on must be present after OnState")
	}
	if !*m.IsOn {
		t.Errorf("is_on=%v want true", *m.IsOn)
	}
}

func TestStatePayloadNilSwitchReturnsNil(t *testing.T) {
	var s *Switch
	if s.State() != nil {
		t.Fatal("expected nil StatePayload from nil switch")
	}
}

// ---------------------------------------------------------------------------
// registerSwitchServices — verify turn_on_for is registered
// ---------------------------------------------------------------------------

func TestRegisterSwitchServicesRegisteredTurnOnFor(t *testing.T) {
	s := newPayloadSwitch(t)
	names := s.ServiceMethodNames()
	found := slices.Contains(names, "turn_on_for")
	if !found {
		t.Fatalf("turn_on_for not in service methods: %v", names)
	}
}

// ---------------------------------------------------------------------------
// AttachPowerEnergySources — nil-safe smoke test
// ---------------------------------------------------------------------------

func TestAttachPowerEnergySourcesNilDevice(t *testing.T) {
	// Must not panic.
	AttachPowerEnergySources(nil)
}

// ---------------------------------------------------------------------------
// findSiblingMeasurementSources — empty channels returns nil
// ---------------------------------------------------------------------------

func TestFindSiblingMeasurementSourcesEmptyChannels(t *testing.T) {
	pwr, eng := findSiblingMeasurementSources(nil)
	if pwr != nil || eng != nil {
		t.Fatalf("expected nil for empty channels, got pwr=%v eng=%v", pwr, eng)
	}
}

// ---------------------------------------------------------------------------
// topology.go — HAComponent and TopicSlot
// ---------------------------------------------------------------------------

func TestHAComponentIsSwitch(t *testing.T) {
	s := newPayloadSwitch(t)
	if s.HAComponent() != "switch" {
		t.Fatalf("HAComponent=%q want 'switch'", s.HAComponent())
	}
}

func TestTopicSlotHasCorrectFields(t *testing.T) {
	s := newTestSwitch(t, "VCU2128127:4", "", &stubWriter{})
	slot := s.TopicSlot()
	if slot.Address != "VCU2128127" {
		t.Errorf("TopicSlot.Address=%q want 'VCU2128127'", slot.Address)
	}
	if slot.Channel != 4 {
		t.Errorf("TopicSlot.Channel=%d want 4", slot.Channel)
	}
	if slot.Parameter != "switch" {
		t.Errorf("TopicSlot.Parameter=%q want 'switch'", slot.Parameter)
	}
}

func TestTopicSlotInvalidAddressFallsBack(t *testing.T) {
	// An address without a colon separator should not panic.
	s := newTestSwitch(t, "NOCOMP", "", &stubWriter{})
	slot := s.TopicSlot()
	if slot.Address != "NOCOMP" {
		t.Errorf("TopicSlot.Address=%q want 'NOCOMP' for invalid address", slot.Address)
	}
	if slot.Channel != 0 {
		t.Errorf("TopicSlot.Channel=%d want 0 for invalid address", slot.Channel)
	}
}
