// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package ws

import (
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/model/hub"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// --- Hub-model singleton push tests ---

// TestHubEventsSubscriberAlarmMessages verifies that replacing alarm messages
// fires a broadcast on AlarmMessagesTopic with the correct count.
func TestHubEventsSubscriberAlarmMessages(t *testing.T) {
	t.Parallel()

	h := NewHub()
	reg, cu := hubEventsRegistry(t)

	sub := NewHubEventsSubscriber(reg, h)
	sub.Start()
	t.Cleanup(sub.Stop)

	cu.HubModel.Messages.Replace([]hub.AlarmMessage{
		{ID: "1", Name: "Alarm A"},
		{ID: "2", Name: "Alarm B"},
	})

	ev := pollHub(t, h, func(topic string) bool {
		return topic == AlarmMessagesTopic("home")
	})
	if ev.Type != string(hmevent.EventTypeAlarmMessage) {
		t.Fatalf("type = %q, want %q", ev.Type, string(hmevent.EventTypeAlarmMessage))
	}
	p, ok := ev.Payload.(HubCountChangedPayload)
	if !ok {
		t.Fatalf("payload type %T, want HubCountChangedPayload", ev.Payload)
	}
	if p.Central != "home" {
		t.Fatalf("central = %q, want %q", p.Central, "home")
	}
	if p.Count != 2 {
		t.Fatalf("count = %d, want 2", p.Count)
	}
}

// TestHubEventsSubscriberServiceMessages verifies that replacing service
// messages fires a broadcast on ServiceMessagesTopic with the correct count.
func TestHubEventsSubscriberServiceMessages(t *testing.T) {
	t.Parallel()

	h := NewHub()
	reg, cu := hubEventsRegistry(t)

	sub := NewHubEventsSubscriber(reg, h)
	sub.Start()
	t.Cleanup(sub.Stop)

	cu.HubModel.ServiceMessages.Replace([]hub.ServiceMessage{
		{ID: "SM1", Name: "Low battery"},
	})

	ev := pollHub(t, h, func(topic string) bool {
		return topic == ServiceMessagesTopic("home")
	})
	if ev.Type != string(hmevent.EventTypeServiceMessage) {
		t.Fatalf("type = %q, want %q", ev.Type, string(hmevent.EventTypeServiceMessage))
	}
	p, ok := ev.Payload.(HubCountChangedPayload)
	if !ok {
		t.Fatalf("payload type %T, want HubCountChangedPayload", ev.Payload)
	}
	if p.Count != 1 {
		t.Fatalf("count = %d, want 1", p.Count)
	}
}

// TestHubEventsSubscriberInbox verifies that replacing inbox devices fires a
// broadcast on InboxTopic with the correct count.
func TestHubEventsSubscriberInbox(t *testing.T) {
	t.Parallel()

	h := NewHub()
	reg, cu := hubEventsRegistry(t)

	sub := NewHubEventsSubscriber(reg, h)
	sub.Start()
	t.Cleanup(sub.Stop)

	cu.HubModel.Inbox.Replace([]hub.InboxDevice{
		{Address: "ADDR1", Name: "New device"},
		{Address: "ADDR2", Name: "Another device"},
		{Address: "ADDR3", Name: "Third device"},
	})

	ev := pollHub(t, h, func(topic string) bool {
		return topic == InboxTopic("home")
	})
	if ev.Type != eventTypeInboxChanged {
		t.Fatalf("type = %q, want %q", ev.Type, eventTypeInboxChanged)
	}
	p, ok := ev.Payload.(HubCountChangedPayload)
	if !ok {
		t.Fatalf("payload type %T, want HubCountChangedPayload", ev.Payload)
	}
	if p.Count != 3 {
		t.Fatalf("count = %d, want 3", p.Count)
	}
}

// TestHubEventsSubscriberMetrics verifies that observing a metric fires a
// broadcast on MetricsTopic with the metric kind, value, and unit.
func TestHubEventsSubscriberMetrics(t *testing.T) {
	t.Parallel()

	h := NewHub()
	reg, cu := hubEventsRegistry(t)

	sub := NewHubEventsSubscriber(reg, h)
	sub.Start()
	t.Cleanup(sub.Stop)

	cu.HubModel.Metrics.Observe(hub.MetricSystemHealth, 95)

	ev := pollHub(t, h, func(topic string) bool {
		return topic == MetricsTopic("home")
	})
	if ev.Type != eventTypeMetricsChanged {
		t.Fatalf("type = %q, want %q", ev.Type, eventTypeMetricsChanged)
	}
	p, ok := ev.Payload.(HubMetricChangedPayload)
	if !ok {
		t.Fatalf("payload type %T, want HubMetricChangedPayload", ev.Payload)
	}
	if p.Central != "home" {
		t.Fatalf("central = %q, want %q", p.Central, "home")
	}
	if p.Metric != string(hub.MetricSystemHealth) {
		t.Fatalf("metric = %q, want %q", p.Metric, string(hub.MetricSystemHealth))
	}
	if p.Value != 95 {
		t.Fatalf("value = %g, want 95", p.Value)
	}
	if p.Unit != "%" {
		t.Fatalf("unit = %q, want %%", p.Unit)
	}
}

// TestHubEventsSubscriberMetricsSuppressesNotReadySentinel verifies that
// the not-ready sentinel (a negative system_health, meaning "the central
// is FAILED, score unknown") never reaches the hub.metrics_changed
// broadcast — mirroring the REST config snapshot (system_hub.go) and the
// hub data-point projection (hub_data_points.go), which both omit it for
// the same reason. Without the suppression, a client mirroring the
// broadcast onto a gauge renders "-1 %" during an outage instead of
// "unknown".
func TestHubEventsSubscriberMetricsSuppressesNotReadySentinel(t *testing.T) {
	t.Parallel()

	h := NewHub()
	reg, cu := hubEventsRegistry(t)

	sub := NewHubEventsSubscriber(reg, h)
	sub.Start()
	t.Cleanup(sub.Stop)

	cu.HubModel.Metrics.Observe(hub.MetricSystemHealth, hub.MetricSystemHealthUnknown)
	// A subsequent legitimate observation proves the subscriber is still
	// alive and forwarding for this metric kind — if the sentinel had
	// fired too, it would appear in the replay buffer alongside this one.
	cu.HubModel.Metrics.Observe(hub.MetricSystemHealth, 97)

	ev := pollHub(t, h, func(topic string) bool {
		return topic == MetricsTopic("home")
	})
	p, ok := ev.Payload.(HubMetricChangedPayload)
	if !ok {
		t.Fatalf("payload type %T, want HubMetricChangedPayload", ev.Payload)
	}
	if p.Value != 97 {
		t.Fatalf("value = %g, want 97 — the not-ready sentinel must not have reached the wire first", p.Value)
	}

	res := h.Replay(0, func(topic string) bool { return topic == MetricsTopic("home") })
	for _, e := range res.Events {
		if pp, ok := e.Payload.(HubMetricChangedPayload); ok && pp.Value < 0 {
			t.Fatalf("found a negative system_health broadcast in the replay buffer: %+v", pp)
		}
	}
}

// TestHubEventsSubscriberConnectivity verifies that a connectivity state change
// fires a broadcast on ConnectivityTopic with the interface ID and reachability.
func TestHubEventsSubscriberConnectivity(t *testing.T) {
	t.Parallel()

	h := NewHub()
	reg, cu := hubEventsRegistry(t)

	sub := NewHubEventsSubscriber(reg, h)
	sub.Start()
	t.Cleanup(sub.Stop)

	// Connectivity rides the event bus (the tracker is attached lazily, so
	// the subscriber cannot wire a model hook at Start time).
	events.Publish(cu.EventBus, hmevent.ConnectivityChangedEvent{
		CentralName: "home",
		InterfaceID: "HmIP-RF",
		Reachable:   true,
		LatencyMs:   12.5,
	})

	ev := pollHub(t, h, func(topic string) bool {
		return topic == ConnectivityTopic("home", "HmIP-RF")
	})
	if ev.Type != string(hmevent.EventTypeConnectivityChanged) {
		t.Fatalf("type = %q, want %q", ev.Type, string(hmevent.EventTypeConnectivityChanged))
	}
	p, ok := ev.Payload.(HubConnectivityChangedPayload)
	if !ok {
		t.Fatalf("payload type %T, want HubConnectivityChangedPayload", ev.Payload)
	}
	if p.Central != "home" {
		t.Fatalf("central = %q, want %q", p.Central, "home")
	}
	if p.InterfaceID != "HmIP-RF" {
		t.Fatalf("interface_id = %q, want %q", p.InterfaceID, "HmIP-RF")
	}
	if !p.Reachable {
		t.Fatalf("reachable = false, want true")
	}
}

// --- Topic-function unit tests ---

func TestAlarmMessagesTopicFormat(t *testing.T) {
	got := AlarmMessagesTopic("home")
	if want := "hub.home.alarm_messages"; got != want {
		t.Fatalf("AlarmMessagesTopic = %q, want %q", got, want)
	}
}

func TestServiceMessagesTopicFormat(t *testing.T) {
	got := ServiceMessagesTopic("home")
	if want := "hub.home.service_messages"; got != want {
		t.Fatalf("ServiceMessagesTopic = %q, want %q", got, want)
	}
}

func TestInboxTopicFormat(t *testing.T) {
	got := InboxTopic("home")
	if want := "hub.home.inbox"; got != want {
		t.Fatalf("InboxTopic = %q, want %q", got, want)
	}
}

func TestMetricsTopicFormat(t *testing.T) {
	got := MetricsTopic("home")
	if want := "hub.home.metrics"; got != want {
		t.Fatalf("MetricsTopic = %q, want %q", got, want)
	}
}

func TestConnectivityTopicFormat(t *testing.T) {
	got := ConnectivityTopic("home", "HmIP-RF")
	if want := "hub.home.connectivity.HmIP-RF"; got != want {
		t.Fatalf("ConnectivityTopic = %q, want %q", got, want)
	}
}

func TestSysvarTopicFormat(t *testing.T) {
	got := SysvarTopic("home", "EnergyCounter")
	if want := "hub.home.sysvars.EnergyCounter"; got != want {
		t.Fatalf("SysvarTopic = %q, want %q", got, want)
	}
}

func TestProgramTopicFormat(t *testing.T) {
	got := ProgramTopic("home", "1234")
	if want := "hub.home.programs.1234"; got != want {
		t.Fatalf("ProgramTopic = %q, want %q", got, want)
	}
}

func TestHubEventsSubscriberNilSafe(t *testing.T) {
	s := NewHubEventsSubscriber(nil, nil)
	s.Start()
	s.Stop()
}

func TestInstallModeTopicFormat(t *testing.T) {
	got := InstallModeTopic("home")
	if want := "hub.home.install_mode"; got != want {
		t.Fatalf("InstallModeTopic = %q, want %q", got, want)
	}
}

func TestInstallModeChangedPayloadShape(t *testing.T) {
	p := InstallModeChangedPayload{
		Central:    "home",
		Enabled:    true,
		RemainingS: 42,
	}
	if p.Central != "home" || !p.Enabled || p.RemainingS != 42 {
		t.Fatalf("payload field round-trip failed: %+v", p)
	}
}

func TestSysvarChangedPayloadShape(t *testing.T) {
	p := SysvarChangedPayload{
		Central:   "home",
		Name:      "EnergyCounter",
		ValueType: hmenum.HubValueType("FLOAT"),
		Value:     42.5,
		Previous:  41.0,
	}
	if p.Central != "home" || p.Name != "EnergyCounter" {
		t.Fatalf("payload field round-trip failed: %+v", p)
	}
}

func TestProgramExecutedPayloadShape(t *testing.T) {
	p := ProgramExecutedPayload{
		Central:   "home",
		ProgramID: "42",
		Trigger:   hmenum.ProgramTrigger("MANUAL"),
		Success:   true,
	}
	if p.ProgramID != "42" || !p.Success {
		t.Fatalf("payload field round-trip failed: %+v", p)
	}
}

// hubEventsRegistry builds a registry with one central whose serial is set to
// "3014F711A0001234" (suffix "11a0001234") for the hub-events end-to-end tests.
func hubEventsRegistry(t *testing.T) (*central.Registry, *central.Unit) {
	t.Helper()
	reg := central.NewRegistry()
	cu, err := central.New(central.Config{Name: "home"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	if err := reg.Register(cu); err != nil {
		t.Fatalf("reg.Register: %v", err)
	}
	cu.SetSystemInformation(central.SystemInfo{Serial: "3014F711A0001234"})
	return reg, cu
}

// pollHub waits up to 2 s for a hub event matching the given topic filter.
// It returns the first matching event or calls t.Fatal if none appears.
func pollHub(t *testing.T, h *Hub, filter func(string) bool) Event {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		res := h.Replay(0, filter)
		if len(res.Events) > 0 {
			return res.Events[0]
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("expected hub event did not appear within deadline")
	return Event{}
}

// TestHubEventsSubscriberSysvarUniqueID publishes a SysvarChangedEvent and
// verifies the payload carries the serial-prefixed unique_id for the sysvar.
func TestHubEventsSubscriberSysvarUniqueID(t *testing.T) {
	t.Parallel()

	h := NewHub()
	reg, cu := hubEventsRegistry(t)

	sub := NewHubEventsSubscriber(reg, h)
	sub.Start()
	t.Cleanup(sub.Stop)

	events.Publish(cu.EventBus, hmevent.SysvarChangedEvent{
		Base:        hmevent.NewBase(),
		CentralName: "home",
		Name:        "Außen Temperatur",
		ValueType:   hmenum.HubValueType("FLOAT"),
		NewValue:    hmtypes.FloatValue(5.5),
		OldValue:    hmtypes.FloatValue(4.0),
	})

	ev := pollHub(t, h, func(topic string) bool {
		return topic == SysvarTopic("home", "Außen Temperatur")
	})
	p, ok := ev.Payload.(SysvarChangedPayload)
	if !ok {
		t.Fatalf("payload type %T, want SysvarChangedPayload", ev.Payload)
	}
	if want := "loom_11a0001234_sysvar_aussen-temperatur"; p.UniqueID != want {
		t.Fatalf("unique_id = %q, want %q", p.UniqueID, want)
	}
}

// TestHubEventsSubscriberProgramUniqueIDResolvable registers a program in the
// hub model and verifies the executed-event payload carries the key built
// from the CCU program id — the value a rename does not move.
func TestHubEventsSubscriberProgramUniqueIDResolvable(t *testing.T) {
	t.Parallel()

	h := NewHub()
	reg, cu := hubEventsRegistry(t)

	// Register the program in the central's hub model so the name can be resolved.
	cu.HubModel.PutProgram(&hub.Program{
		HubDataPoint: hub.HubDataPoint{Name: "Lights Off"},
		ID:           "P1",
	})

	sub := NewHubEventsSubscriber(reg, h)
	sub.Start()
	t.Cleanup(sub.Stop)

	events.Publish(cu.EventBus, hmevent.ProgramExecutedEvent{
		Base:        hmevent.NewBase(),
		CentralName: "home",
		ProgramID:   "P1",
		Trigger:     hmenum.ProgramTriggerUser,
		Success:     true,
	})

	ev := pollHub(t, h, func(topic string) bool {
		return topic == ProgramTopic("home", "P1")
	})
	p, ok := ev.Payload.(ProgramExecutedPayload)
	if !ok {
		t.Fatalf("payload type %T, want ProgramExecutedPayload", ev.Payload)
	}
	if want := "loom_11a0001234_program_p1"; p.UniqueID != want {
		t.Fatalf("unique_id = %q, want %q", p.UniqueID, want)
	}
}

// TestHubEventsSubscriberProgramUniqueIDUnresolvable verifies that an
// unresolvable program ID results in an empty unique_id.
func TestHubEventsSubscriberProgramUniqueIDUnresolvable(t *testing.T) {
	t.Parallel()

	h := NewHub()
	reg, cu := hubEventsRegistry(t)

	sub := NewHubEventsSubscriber(reg, h)
	sub.Start()
	t.Cleanup(sub.Stop)

	// No PutProgram call → ID "UNKNOWN" cannot be resolved.
	events.Publish(cu.EventBus, hmevent.ProgramExecutedEvent{
		Base:        hmevent.NewBase(),
		CentralName: "home",
		ProgramID:   "UNKNOWN",
		Trigger:     hmenum.ProgramTriggerUser,
		Success:     false,
	})

	ev := pollHub(t, h, func(topic string) bool {
		return topic == ProgramTopic("home", "UNKNOWN")
	})
	p, ok := ev.Payload.(ProgramExecutedPayload)
	if !ok {
		t.Fatalf("payload type %T, want ProgramExecutedPayload", ev.Payload)
	}
	if p.UniqueID != "" {
		t.Fatalf("unique_id = %q, want empty for unresolvable program", p.UniqueID)
	}
}

// TestHubEventsSubscriberSysvarDeviceLink verifies that a sysvar with a
// resolved device link (channel set by the southbound assignment pass)
// carries channel + device_address on the change broadcast, while a sysvar
// without a link omits both fields.
func TestHubEventsSubscriberSysvarDeviceLink(t *testing.T) {
	t.Parallel()

	h := NewHub()
	reg, cu := hubEventsRegistry(t)

	linked := hub.NewSysvar("home", "svEnergyCounter_14884_000858A994D482:7", "", hmenum.HubValueType("FLOAT"), nil)
	linked.SetChannel("000858A994D482:7")
	cu.HubModel.PutSysvar(linked)
	cu.HubModel.PutSysvar(hub.NewSysvar("home", "Unlinked", "", hmenum.HubValueType("FLOAT"), nil))

	sub := NewHubEventsSubscriber(reg, h)
	sub.Start()
	t.Cleanup(sub.Stop)

	events.Publish(cu.EventBus, hmevent.SysvarChangedEvent{
		Base:        hmevent.NewBase(),
		CentralName: "home",
		Name:        "svEnergyCounter_14884_000858A994D482:7",
		ValueType:   hmenum.HubValueType("FLOAT"),
		NewValue:    hmtypes.FloatValue(5.5),
		OldValue:    hmtypes.FloatValue(4.0),
	})
	ev := pollHub(t, h, func(topic string) bool {
		return topic == SysvarTopic("home", "svEnergyCounter_14884_000858A994D482:7")
	})
	p, ok := ev.Payload.(SysvarChangedPayload)
	if !ok {
		t.Fatalf("payload type %T, want SysvarChangedPayload", ev.Payload)
	}
	if p.Channel != "000858A994D482:7" {
		t.Fatalf("channel = %q, want %q", p.Channel, "000858A994D482:7")
	}
	if p.DeviceAddress != "000858A994D482" {
		t.Fatalf("device_address = %q, want %q", p.DeviceAddress, "000858A994D482")
	}

	events.Publish(cu.EventBus, hmevent.SysvarChangedEvent{
		Base:        hmevent.NewBase(),
		CentralName: "home",
		Name:        "Unlinked",
		ValueType:   hmenum.HubValueType("FLOAT"),
		NewValue:    hmtypes.FloatValue(1.0),
		OldValue:    hmtypes.FloatValue(0.0),
	})
	ev = pollHub(t, h, func(topic string) bool {
		return topic == SysvarTopic("home", "Unlinked")
	})
	p, ok = ev.Payload.(SysvarChangedPayload)
	if !ok {
		t.Fatalf("payload type %T, want SysvarChangedPayload", ev.Payload)
	}
	if p.Channel != "" || p.DeviceAddress != "" {
		t.Fatalf("unlinked sysvar must omit channel/device_address, got %q/%q", p.Channel, p.DeviceAddress)
	}
}

// --- SystemUpdate tests ---

// TestSystemUpdateTopicFormat verifies the canonical SystemUpdateTopic format.
func TestSystemUpdateTopicFormat(t *testing.T) {
	got := SystemUpdateTopic("home")
	if want := "hub.home.system_update"; got != want {
		t.Fatalf("SystemUpdateTopic = %q, want %q", got, want)
	}
}

// TestHubSystemUpdateChangedPayloadShape verifies that
// HubSystemUpdateChangedPayload fields round-trip correctly.
func TestHubSystemUpdateChangedPayloadShape(t *testing.T) {
	p := HubSystemUpdateChangedPayload{
		Central:           "home",
		CurrentFirmware:   "1.2",
		AvailableFirmware: "1.3",
		UpdateAvailable:   true,
		InProgress:        false,
	}
	if p.Central != "home" {
		t.Fatalf("Central = %q, want %q", p.Central, "home")
	}
	if p.CurrentFirmware != "1.2" {
		t.Fatalf("CurrentFirmware = %q, want %q", p.CurrentFirmware, "1.2")
	}
	if p.AvailableFirmware != "1.3" {
		t.Fatalf("AvailableFirmware = %q, want %q", p.AvailableFirmware, "1.3")
	}
	if !p.UpdateAvailable {
		t.Fatalf("UpdateAvailable = false, want true")
	}
	if p.InProgress {
		t.Fatalf("InProgress = true, want false")
	}
}

// TestHubEventsSubscriberSystemUpdate verifies that firing UpdateInfo on the
// hub model's Update tracker publishes a WS event on SystemUpdateTopic with
// the correct payload fields.
func TestHubEventsSubscriberSystemUpdate(t *testing.T) {
	t.Parallel()

	h := NewHub()
	reg, cu := hubEventsRegistry(t)

	sub := NewHubEventsSubscriber(reg, h)
	sub.Start()
	t.Cleanup(sub.Stop)

	cu.HubModel.Update.OnInfo(hub.UpdateInfo{
		CurrentFirmware:   "1.2",
		AvailableFirmware: "1.3",
		UpdateAvailable:   true,
	})

	ev := pollHub(t, h, func(topic string) bool {
		return topic == SystemUpdateTopic("home")
	})
	if ev.Type != eventTypeSystemUpdateChanged {
		t.Fatalf("type = %q, want %q", ev.Type, eventTypeSystemUpdateChanged)
	}
	p, ok := ev.Payload.(HubSystemUpdateChangedPayload)
	if !ok {
		t.Fatalf("payload type %T, want HubSystemUpdateChangedPayload", ev.Payload)
	}
	if p.Central != "home" {
		t.Fatalf("Central = %q, want %q", p.Central, "home")
	}
	if p.CurrentFirmware != "1.2" {
		t.Fatalf("CurrentFirmware = %q, want %q", p.CurrentFirmware, "1.2")
	}
	if p.AvailableFirmware != "1.3" {
		t.Fatalf("AvailableFirmware = %q, want %q", p.AvailableFirmware, "1.3")
	}
	if !p.UpdateAvailable {
		t.Fatalf("UpdateAvailable = false, want true")
	}
}

// TestHubEventsSubscriberSysvarUniqueIDUsesTheVid pins the WebSocket frame to
// the model's identity for a sysvar the hub has resolved.
//
// The frame used to stamp the name slug unconditionally. A name is editable
// and a vid is not, which is exactly why [hub.Sysvar.CanonicalUniqueID] keys
// on the vid once a scan has produced one — a client seeding its registry
// from these events otherwise bound entities to a key that moved on rename
// and disagreed with the same variable's id over REST.
//
// The sibling test above covers the other half: a sysvar the model has not
// seen keeps the name-keyed fallback, so the field never vanishes mid-scan.
func TestHubEventsSubscriberSysvarUniqueIDUsesTheVid(t *testing.T) {
	t.Parallel()

	h := NewHub()
	reg, cu := hubEventsRegistry(t)

	cu.Hub.SetHubModel(cu.HubModel)
	sv := hub.NewSysvar("home", "Außen Temperatur", "", hmenum.HubValueType("FLOAT"), nil)
	sv.Vid = 4711
	cu.HubModel.PutSysvar(sv)

	sub := NewHubEventsSubscriber(reg, h)
	sub.Start()
	t.Cleanup(sub.Stop)

	events.Publish(cu.EventBus, hmevent.SysvarChangedEvent{
		Base:        hmevent.NewBase(),
		CentralName: "home",
		Name:        "Außen Temperatur",
		ValueType:   hmenum.HubValueType("FLOAT"),
		NewValue:    hmtypes.FloatValue(5.5),
		OldValue:    hmtypes.FloatValue(4.0),
	})

	ev := pollHub(t, h, func(topic string) bool {
		return topic == SysvarTopic("home", "Außen Temperatur")
	})
	p, ok := ev.Payload.(SysvarChangedPayload)
	if !ok {
		t.Fatalf("payload type %T, want SysvarChangedPayload", ev.Payload)
	}
	if want := "loom_11a0001234_sysvar_4711"; p.UniqueID != want {
		t.Fatalf("unique_id = %q, want %q — the frame must carry the model's identity, not the name slug", p.UniqueID, want)
	}
}
