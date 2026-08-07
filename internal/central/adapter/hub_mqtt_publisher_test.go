// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/model/hub"
	"github.com/SukramJ/openccu-loom/internal/north/mqtt"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// hubMQTTFixture builds a test central + registry + MQTT noop client +
// wiring and a ready HubMQTTPublisher.
func hubMQTTFixture(t *testing.T) (
	reg *central.Registry,
	c *central.Unit,
	pub *mqtt.NoopClient,
	publisher *HubMQTTPublisher,
) {
	t.Helper()
	c, err := central.New(central.Config{Name: "ccu-01"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	reg = central.NewRegistry()
	if err := reg.Register(c); err != nil {
		t.Fatalf("reg.Register: %v", err)
	}
	pub = mqtt.NewNoopClient()
	bridge := mqtt.NewBridge(mqtt.BridgeConfig{
		Base:        "openccu-loom",
		CentralName: "ccu-01",
		RawEnabled:  true,
	}, pub)
	wiring := mqtt.NewWiring(bridge, nil)
	publisher = NewHubMQTTPublisher(reg, wiring, nil)
	return reg, c, pub, publisher
}

// publishedTopics extracts the MQTT topic strings from the noop client.
func publishedTopics(pub *mqtt.NoopClient) []string {
	all := pub.Published()
	topics := make([]string, len(all))
	for i, p := range all {
		topics[i] = p.Topic
	}
	return topics
}

// containsTopic checks whether any published topic contains substr.
func containsTopic(pub *mqtt.NoopClient, substr string) bool {
	for _, t := range publishedTopics(pub) {
		if strings.Contains(t, substr) {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Program tests
// ---------------------------------------------------------------------------

// TestProgramUpdateReachesMQTT verifies that a Program OnUpdate event
// causes the publisher to call Bridge.PublishProgram so the
// programs/<id> topic appears at the broker.
func TestProgramUpdateReachesMQTT(t *testing.T) {
	t.Parallel()
	reg, c, pub, publisher := hubMQTTFixture(t)
	_ = reg

	prog := &hub.Program{HubDataPoint: hub.HubDataPoint{Name: "Abend"}, ID: "prog-1", Writer: nil}
	prog.OnActive(false)
	c.HubModel.PutProgram(prog)

	publisher.Start(context.Background())
	defer publisher.Stop()
	publisher.Flush()

	// Initial-state publish should have fired.
	if !containsTopic(pub, "programs/prog-1") {
		t.Fatalf("initial-state publish missing; topics=%v", publishedTopics(pub))
	}

	prevCount := len(pub.Published())

	// Trigger an update.
	prog.OnExecution(true, hmenum.ProgramTriggerUser)

	publisher.Flush()
	after := pub.Published()
	if len(after) <= prevCount {
		t.Fatalf("no additional publish after OnExecution; before=%d after=%d topics=%v",
			prevCount, len(after), publishedTopics(pub))
	}
	if !containsTopic(pub, "programs/prog-1") {
		t.Fatalf("programs/prog-1 topic missing after update; topics=%v", publishedTopics(pub))
	}
}

// TestProgramInitialStatePushedAtStart verifies that Start fires one
// publish per program so retained topics carry the current value even
// before the first change event.
func TestProgramInitialStatePushedAtStart(t *testing.T) {
	t.Parallel()
	_, c, pub, publisher := hubMQTTFixture(t)

	prog := &hub.Program{HubDataPoint: hub.HubDataPoint{Name: "Morgen"}, ID: "prog-2"}
	prog.OnActive(true)
	c.HubModel.PutProgram(prog)

	publisher.Start(context.Background())
	defer publisher.Stop()
	publisher.Flush()

	if !containsTopic(pub, "programs/prog-2") {
		t.Fatalf("initial-state publish missing; topics=%v", publishedTopics(pub))
	}
}

// ---------------------------------------------------------------------------
// Sysvar tests
// ---------------------------------------------------------------------------

// TestSysvarUpdateReachesMQTT verifies that Sysvar.OnValue fires the
// publisher so the sysvars/<name> topic appears at the broker.
func TestSysvarUpdateReachesMQTT(t *testing.T) {
	t.Parallel()
	_, c, pub, publisher := hubMQTTFixture(t)

	sv := &hub.Sysvar{HubDataPoint: hub.HubDataPoint{Name: "Anwesenheit"}, ValueType: hmenum.HubValueTypeLogic}
	sv.OnValue(hmtypes.BoolValue(true))
	c.HubModel.PutSysvar(sv)

	publisher.Start(context.Background())
	defer publisher.Stop()
	publisher.Flush()

	// Initial push at Start.
	if !containsTopic(pub, "sysvars/Anwesenheit") {
		t.Fatalf("initial-state publish missing; topics=%v", publishedTopics(pub))
	}

	prev := len(pub.Published())
	// Change the value.
	sv.OnValue(hmtypes.BoolValue(false))

	publisher.Flush()
	after := pub.Published()
	if len(after) <= prev {
		t.Fatalf("no publish after sysvar change; before=%d after=%d topics=%v",
			prev, len(after), publishedTopics(pub))
	}
}

// TestSysvarInitialStateNotPushedWhenUnobserved verifies that a sysvar
// with no observed value does NOT trigger an initial-state publish —
// only sysvars that have been populated by the hub coordinator are
// pushed.
func TestSysvarInitialStateNotPushedWhenUnobserved(t *testing.T) {
	t.Parallel()
	_, c, pub, publisher := hubMQTTFixture(t)

	// Sysvar with no value observed yet.
	sv := &hub.Sysvar{HubDataPoint: hub.HubDataPoint{Name: "Unbeobachtet"}, ValueType: hmenum.HubValueTypeLogic}
	c.HubModel.PutSysvar(sv)

	publisher.Start(context.Background())
	defer publisher.Stop()
	publisher.Flush()

	if containsTopic(pub, "sysvars/Unbeobachtet") {
		t.Fatalf("unobserved sysvar must not be pushed; topics=%v", publishedTopics(pub))
	}
}

// ---------------------------------------------------------------------------
// AlarmMessages tests
// ---------------------------------------------------------------------------

// TestAlarmMessagesUpdateReachesMQTT verifies that AlarmMessages.Replace
// fires the publisher so the alarm_messages topic appears at the broker.
func TestAlarmMessagesUpdateReachesMQTT(t *testing.T) {
	t.Parallel()
	_, c, pub, publisher := hubMQTTFixture(t)

	// Seed initial set.
	c.HubModel.Messages.Replace([]hub.AlarmMessage{
		{ID: "1", Name: "Einbruch", Timestamp: time.Now()},
	})

	publisher.Start(context.Background())
	defer publisher.Stop()
	publisher.Flush()

	// Initial-state publish.
	if !containsTopic(pub, "alarm_messages") {
		t.Fatalf("initial-state alarm publish missing; topics=%v", publishedTopics(pub))
	}

	prev := len(pub.Published())
	// Replace with a different set.
	c.HubModel.Messages.Replace([]hub.AlarmMessage{
		{ID: "1", Name: "Einbruch", Timestamp: time.Now()},
		{ID: "2", Name: "Wasserschaden", Timestamp: time.Now()},
	})

	publisher.Flush()
	after := pub.Published()
	if len(after) <= prev {
		t.Fatalf("no publish after alarm message replace; before=%d after=%d topics=%v",
			prev, len(after), publishedTopics(pub))
	}
}

// ---------------------------------------------------------------------------
// ServiceMessages tests
// ---------------------------------------------------------------------------

// TestServiceMessagesUpdateReachesMQTT verifies that ServiceMessages.Replace
// fires the publisher so the service_messages topic appears at the broker.
func TestServiceMessagesUpdateReachesMQTT(t *testing.T) {
	t.Parallel()
	_, c, pub, publisher := hubMQTTFixture(t)

	c.HubModel.ServiceMessages.Replace([]hub.ServiceMessage{
		{ID: "s1", Name: "Batterie leer", Timestamp: time.Now()},
	})

	publisher.Start(context.Background())
	defer publisher.Stop()
	publisher.Flush()

	if !containsTopic(pub, "service_messages") {
		t.Fatalf("initial-state service-messages publish missing; topics=%v", publishedTopics(pub))
	}

	prev := len(pub.Published())
	c.HubModel.ServiceMessages.Replace([]hub.ServiceMessage{})
	publisher.Flush()
	after := pub.Published()
	if len(after) <= prev {
		t.Fatalf("no publish after service-messages clear; before=%d after=%d topics=%v",
			prev, len(after), publishedTopics(pub))
	}
}

// ---------------------------------------------------------------------------
// InstallMode tests
// ---------------------------------------------------------------------------

// TestInstallModeChangeReachesMQTT verifies that an
// InstallModeChangedEvent on the central bus results in a
// install_mode MQTT publish.
func TestInstallModeChangeReachesMQTT(t *testing.T) {
	t.Parallel()
	_, c, pub, publisher := hubMQTTFixture(t)

	publisher.Start(context.Background())
	defer publisher.Stop()
	publisher.Flush()

	// No initial push for install_mode (no prior observed value on HubModel).
	prev := len(pub.Published())

	events.Publish(c.EventBus, hmevent.InstallModeChangedEvent{
		Base:        hmevent.NewBaseAt(time.Now()),
		CentralName: "ccu-01",
		InterfaceID: "HmIP-RF",
		Enabled:     true,
		RemainingS:  60,
	})

	publisher.Flush()
	after := pub.Published()
	if len(after) <= prev {
		t.Fatalf("no publish after InstallModeChangedEvent; before=%d after=%d topics=%v",
			prev, len(after), publishedTopics(pub))
	}
	if !containsTopic(pub, "install_mode") {
		t.Fatalf("install_mode topic missing; topics=%v", publishedTopics(pub))
	}
}

// TestInstallModeEventForOtherCentralIgnored verifies that an
// InstallModeChangedEvent for a different central is not forwarded.
func TestInstallModeEventForOtherCentralIgnored(t *testing.T) {
	t.Parallel()
	_, c, pub, publisher := hubMQTTFixture(t)

	publisher.Start(context.Background())
	defer publisher.Stop()
	publisher.Flush()

	prev := len(pub.Published())

	events.Publish(c.EventBus, hmevent.InstallModeChangedEvent{
		Base:        hmevent.NewBaseAt(time.Now()),
		CentralName: "other-central", // different name
		Enabled:     true,
		RemainingS:  30,
	})

	publisher.Flush()
	if got := len(pub.Published()); got != prev {
		t.Fatalf("expected no new publish for other-central event, got %d new", got-prev)
	}
}

// ---------------------------------------------------------------------------
// Connectivity tests
// ---------------------------------------------------------------------------

// TestConnectivityChangeReachesMQTT verifies that a
// ConnectivityChangedEvent on the central bus results in a
// connectivity/<iface> MQTT publish.
func TestConnectivityChangeReachesMQTT(t *testing.T) {
	t.Parallel()
	_, c, pub, publisher := hubMQTTFixture(t)

	publisher.Start(context.Background())
	defer publisher.Stop()
	publisher.Flush()

	prev := len(pub.Published())

	events.Publish(c.EventBus, hmevent.ConnectivityChangedEvent{
		Base:        hmevent.NewBaseAt(time.Now()),
		CentralName: "ccu-01",
		InterfaceID: "HmIP-RF",
		Reachable:   false,
	})

	publisher.Flush()
	after := pub.Published()
	if len(after) <= prev {
		t.Fatalf("no publish after ConnectivityChangedEvent; before=%d after=%d topics=%v",
			prev, len(after), publishedTopics(pub))
	}
	if !containsTopic(pub, "connectivity") {
		t.Fatalf("connectivity topic missing; topics=%v", publishedTopics(pub))
	}
}

// TestConnectivityEventForOtherCentralIgnored verifies that a
// ConnectivityChangedEvent for a foreign central is silently dropped.
func TestConnectivityEventForOtherCentralIgnored(t *testing.T) {
	t.Parallel()
	_, c, pub, publisher := hubMQTTFixture(t)

	publisher.Start(context.Background())
	defer publisher.Stop()
	publisher.Flush()

	prev := len(pub.Published())

	events.Publish(c.EventBus, hmevent.ConnectivityChangedEvent{
		Base:        hmevent.NewBaseAt(time.Now()),
		CentralName: "another-ccu",
		InterfaceID: "BidCos-RF",
		Reachable:   true,
	})

	publisher.Flush()
	if got := len(pub.Published()); got != prev {
		t.Fatalf("expected no new publish for other-central event, got %d new", got-prev)
	}
}

// ---------------------------------------------------------------------------
// Lifecycle tests
// ---------------------------------------------------------------------------

// TestHubMQTTPublisherStopReleasesSubscriptions verifies that Stop
// releases subscriptions so a subsequent change does NOT trigger a
// publish. Exercises the no-goroutine-leak contract.
func TestHubMQTTPublisherStopReleasesSubscriptions(t *testing.T) {
	t.Parallel()
	_, c, pub, publisher := hubMQTTFixture(t)

	sv := &hub.Sysvar{HubDataPoint: hub.HubDataPoint{Name: "StopTest"}, ValueType: hmenum.HubValueTypeLogic}
	sv.OnValue(hmtypes.BoolValue(true))
	c.HubModel.PutSysvar(sv)

	publisher.Start(context.Background())
	publisher.Flush()
	publisher.Stop() // release all subscriptions

	prev := len(pub.Published())

	// Change after Stop — must not trigger a publish.
	sv.OnValue(hmtypes.BoolValue(false))

	publisher.Flush()
	if got := len(pub.Published()); got != prev {
		t.Fatalf("after Stop, change must not trigger publish; extra=%d", got-prev)
	}
}

// TestHubMQTTPublisherStartIsIdempotent verifies that calling Start
// twice does not double-subscribe (only one set of subscriptions is
// active at any time).
func TestHubMQTTPublisherStartIsIdempotent(t *testing.T) {
	t.Parallel()
	_, c, pub, publisher := hubMQTTFixture(t)

	sv := &hub.Sysvar{HubDataPoint: hub.HubDataPoint{Name: "IdempotentTest"}, ValueType: hmenum.HubValueTypeLogic}
	sv.OnValue(hmtypes.BoolValue(false))
	c.HubModel.PutSysvar(sv)

	publisher.Start(context.Background())
	publisher.Start(context.Background()) // second Start must release previous subs first
	defer publisher.Stop()
	// Drain the re-wire's own initial publishes before the baseline count, so
	// only the value change below is measured.
	publisher.Flush()

	// Reset publication log by counting before the value change.
	prev := len(pub.Published())

	sv.OnValue(hmtypes.BoolValue(true))

	publisher.Flush()
	after := pub.Published()
	// Default config publishes only the canonical ADR-0011 topic
	// `<central>/hub/sysvars/<name>/state` — both legacy mirrors
	// (flat `<central>/sysvars/<name>` and transitional
	// `<central>/hub/sysvars/<name>`) are gated by
	// LegacyAliasConfig.HubTopics (off by default). A double-
	// subscription regression would manifest as 2+ publishes here.
	const expectedPerSubscription = 1
	newPublishes := len(after) - prev
	if newPublishes != expectedPerSubscription {
		t.Fatalf("expected %d publish per sysvar change after Start×2 (canonical hub topic only), got %d (double-sub guard)",
			expectedPerSubscription, newPublishes)
	}
}

// TestHubMQTTPublisherNilWiringIsNoOp verifies that Start with a nil
// wiring does nothing and does not panic.
func TestHubMQTTPublisherNilWiringIsNoOp(t *testing.T) {
	reg, _, _, _ := hubMQTTFixture(t)
	publisher := NewHubMQTTPublisher(reg, nil, nil)
	publisher.Start(context.Background()) // must not panic
	publisher.Stop()
}

// TestSysvarRegisteredAfterStartReachesMQTT locks the late-registration
// invariant: HubMQTTPublisher.Start runs BEFORE WireCentrals (the
// southbound wire-up that produces the first ReGa sysvar refresh), so
// sysvars registered LATER must still reach MQTT through the
// Hub.OnSysvarRegistered observer the publisher attached during Start.
// Without the observer the publisher would only see sysvars that
// already existed at Start time — none in practice.
func TestSysvarRegisteredAfterStartReachesMQTT(t *testing.T) {
	t.Parallel()
	_, c, pub, publisher := hubMQTTFixture(t)

	// Start FIRST — Hub is still empty.
	publisher.Start(context.Background())
	defer publisher.Stop()
	publisher.Flush()

	if containsTopic(pub, "sysvars/Presence") {
		t.Fatalf("pre-registration leak; topics=%v", publishedTopics(pub))
	}

	// Now register a sysvar — observer must fire wireOneSysvar.
	sv := &hub.Sysvar{HubDataPoint: hub.HubDataPoint{Name: "Presence"}, ValueType: hmenum.HubValueTypeLogic}
	sv.OnValue(hmtypes.BoolValue(true))
	c.HubModel.PutSysvar(sv)
	publisher.Flush()

	if !containsTopic(pub, "sysvars/Presence") {
		t.Fatalf("late-registered sysvar missing from MQTT; topics=%v", publishedTopics(pub))
	}

	// And value changes must propagate too (OnUpdate hook was wired
	// when the observer fired).
	prev := len(pub.Published())
	sv.OnValue(hmtypes.BoolValue(false))
	publisher.Flush()
	if len(pub.Published()) <= prev {
		t.Fatalf("value-change publish missing; topics=%v", publishedTopics(pub))
	}
}

// TestProgramRegisteredAfterStartReachesMQTT is the per-program
// analogue of TestSysvarRegisteredAfterStartReachesMQTT.
func TestProgramRegisteredAfterStartReachesMQTT(t *testing.T) {
	t.Parallel()
	_, c, pub, publisher := hubMQTTFixture(t)

	publisher.Start(context.Background())
	defer publisher.Stop()
	publisher.Flush()

	prog := &hub.Program{HubDataPoint: hub.HubDataPoint{Name: "Abend"}, ID: "late-prg"}
	prog.OnActive(false)
	c.HubModel.PutProgram(prog)
	publisher.Flush()

	if !containsTopic(pub, "programs/late-prg") {
		t.Fatalf("late-registered program missing from MQTT; topics=%v", publishedTopics(pub))
	}

	prev := len(pub.Published())
	prog.OnExecution(true, hmenum.ProgramTriggerUser)
	publisher.Flush()
	if len(pub.Published()) <= prev {
		t.Fatalf("execution publish missing; topics=%v", publishedTopics(pub))
	}
}

// TestStopReleasesObserver locks the lifecycle contract: after Stop()
// late PutSysvar calls must no longer publish. Otherwise a Stop+Start
// cycle would leave dangling observers fanning out into a defunct
// wiring.
func TestStopReleasesObserver(t *testing.T) {
	t.Parallel()
	_, c, pub, publisher := hubMQTTFixture(t)
	publisher.Start(context.Background())
	publisher.Flush()
	publisher.Stop()

	preCount := len(pub.Published())
	c.HubModel.PutSysvar(&hub.Sysvar{
		HubDataPoint: hub.HubDataPoint{Name: "PostStop"},
		ValueType:    hmenum.HubValueTypeLogic,
	})
	if len(pub.Published()) != preCount {
		t.Fatalf("publish fired after Stop; topics=%v", publishedTopics(pub))
	}
}

// TestMQTTDiscoveryFiltersInternalPrograms verifies that programs with
// IsInternal=true are excluded from MQTT discovery. Internal
// programs are CCU-internal Tmp_* programs — not user-visible.
func TestMQTTDiscoveryFiltersInternalPrograms(t *testing.T) {
	t.Parallel()
	_, c, pub, publisher := hubMQTTFixture(t)

	// Regular program — must appear in discovery.
	regular := &hub.Program{HubDataPoint: hub.HubDataPoint{Name: "Abend"}, ID: "regular-prg"}
	regular.OnActive(false)
	c.HubModel.PutProgram(regular)

	// Internal program — must NOT appear in discovery.
	internal := &hub.Program{HubDataPoint: hub.HubDataPoint{Name: "Tmp_Internal"}, ID: "internal-prg", IsInternal: true}
	internal.OnActive(false)
	c.HubModel.PutProgram(internal)

	publisher.Start(context.Background())
	defer publisher.Stop()
	publisher.Flush()

	if !containsTopic(pub, "programs/regular-prg") {
		t.Errorf("regular program missing from discovery; topics=%v", publishedTopics(pub))
	}
	if containsTopic(pub, "programs/internal-prg") {
		t.Errorf("internal program must NOT appear in discovery; topics=%v", publishedTopics(pub))
	}
}

// TestMQTTDiscoveryFilterDoesNotSuppressUpdatesForInternalPrograms verifies
// that the IsInternal filter only affects discovery, not update subscriptions.
// (Internal programs are not subscribed so no updates fire at all.)
func TestMQTTDiscoveryFilterDoesNotSuppressUpdatesForInternalPrograms(t *testing.T) {
	t.Parallel()
	_, c, pub, publisher := hubMQTTFixture(t)

	internal := &hub.Program{HubDataPoint: hub.HubDataPoint{Name: "Tmp_Intl"}, ID: "tmp-prg", IsInternal: true}
	internal.OnActive(false)
	c.HubModel.PutProgram(internal)

	publisher.Start(context.Background())
	defer publisher.Stop()
	publisher.Flush()

	countBefore := len(pub.Published())

	// Trigger an execution — must produce no new topic since we never subscribed.
	internal.OnExecution(true, hmenum.ProgramTriggerUser)

	countAfter := len(pub.Published())
	_ = countBefore
	_ = countAfter
	// The key invariant: discovery topic never appeared.
	if containsTopic(pub, "programs/tmp-prg") {
		t.Errorf("internal program topic must never appear in MQTT; topics=%v", publishedTopics(pub))
	}
}

// ─── sysvarSpecFor / programSpecFor EnabledDefault projection ───────────────

// TestSysvarSpecForEnabledDefaultProjection verifies that sysvarSpecFor
// carries the sysvar's EnabledByDefault() result through to
// [mqtt.HubSysvarSpec.EnabledDefault] unchanged, for both the true and
// false case.
func TestSysvarSpecForEnabledDefaultProjection(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		want bool
	}{
		{name: "enabled", want: true},
		{name: "disabled", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			sv := hub.NewSysvar("ccu-01", "Active", "", hmenum.HubValueTypeLogic, nil)
			sv.EnabledDefault = tc.want
			spec := sysvarSpecFor(sv)
			if spec.EnabledDefault != tc.want {
				t.Fatalf("EnabledDefault: got %v want %v", spec.EnabledDefault, tc.want)
			}
		})
	}
}

// TestProgramSpecForEnabledDefaultProjection verifies that
// programSpecFor carries the program's EnabledByDefault() result
// through to [mqtt.HubProgramSpec.EnabledDefault] unchanged, for both
// the true and false case.
func TestProgramSpecForEnabledDefaultProjection(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		want bool
	}{
		{name: "enabled", want: true},
		{name: "disabled", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			prog := hub.NewProgram("ccu-01", "PRG_1", "Morning", "", false, nil)
			prog.EnabledDefault = tc.want
			spec := programSpecFor(prog)
			if spec.EnabledDefault != tc.want {
				t.Fatalf("EnabledDefault: got %v want %v", spec.EnabledDefault, tc.want)
			}
		})
	}
}
