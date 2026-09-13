// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"context"
	"testing"

	hamodel "github.com/SukramJ/go-hamqtt/model"

	"github.com/SukramJ/openccu-loom/internal/model/hub"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// stubSysvarWriter makes a sysvar writable, which is what puts a `Set` topic
// on its topic set — the half this test exists to compare.
type stubSysvarWriter struct{}

func (stubSysvarWriter) SetSysvar(context.Context, string, any) error { return nil }

// TestHubModelAndDiscoveryDeclareOneTopic pins the cost of ADR 0070's step D
// decision: the hub plane keeps [pload.MQTTAddressable], so every topic on it
// is spelled twice — once by the model object that owns it
// (`(*hub.Sysvar).MQTTTopics` and friends) and once by the discovery builder
// that declares it to Home Assistant (`BuildSysvarDiscovery` and friends,
// which call the [naming] free functions directly). This test is what keeps
// the two equal.
//
// [TestHubPlaneTopicsRoundTrip] does not cover this. It asks whether a
// declared topic is one the plane publishes to or subscribes on, and the
// command half is matched against wildcard filters — so a `command_topic`
// that drifted from the model's `Set` while staying under
// `…/hub/sysvars/+/set` passes it, and the entity would command a topic
// nothing writes to with nothing logged anywhere. The state half is the same
// shape in reverse: the publisher writes the model's string and the config
// names the builder's, and Home Assistant's only report of a disagreement is
// an entity that stays "unknown" forever.
//
// The fixture names deliberately carry a space and an umlaut. With an
// invariant `ccu-01` fixture a divergence in escaping is invisible, because
// both sides render an unescaped name identically.
func TestHubModelAndDiscoveryDeclareOneTopic(t *testing.T) {
	t.Parallel()
	const (
		base       = "openccu-loom"
		central    = "Haus CCÜ"
		sysvarName = "Außen Temperatur"
		programID  = "PRG_1"
		iface      = "HmIP-RF"
	)
	db := NewDefaultDiscoveryBuilder(NewTopicBuilder(base), central)
	db.SetHubInfoFor(central, HubInfo{Serial: "3014F711A0001234"})

	t.Run("sysvar", func(t *testing.T) {
		t.Parallel()
		// Extended + writable is the shape that declares both halves.
		item := db.BuildSysvarDiscovery(central, HubSysvarSpec{
			Name: sysvarName, ValueType: hmenum.HubValueTypeLogic, Writable: true, IsExtended: true,
		})
		body := jsonMap(t, item)
		sv := hub.NewSysvar(central, sysvarName, "", hmenum.HubValueTypeLogic, stubSysvarWriter{})
		topics := sv.MQTTTopics(base, central)
		assertSameTopic(t, "sysvar state", topics.State, body["state_topic"])
		assertSameTopic(t, "sysvar command", topics.Set, body["command_topic"])
	})

	t.Run("program roles", func(t *testing.T) {
		t.Parallel()
		prog := hub.NewProgram(central, programID, "Morning", "", false, nil)
		roles := prog.MQTTRoles(base, central)
		if len(roles) != 2 {
			t.Fatalf("program declared %d roles, want the switch and the execute button", len(roles))
		}
		// The principal role's state topic is the one Bridge.PublishProgram
		// writes through MQTTTopics, so the two model-side spellings have to
		// agree before either is compared with the declared one.
		assertSameTopic(t, "program principal state", prog.MQTTTopics(base, central).State, roles[0].Topics.State)
		items := db.BuildProgramDiscoveryRoles(central, HubProgramSpec{ID: programID, Name: "Morning"}, roles)
		if len(items) != 2 {
			t.Fatalf("built %d discovery items for 2 roles", len(items))
		}
		sw := jsonMap(t, items[0])
		assertSameTopic(t, "program switch state", roles[0].Topics.State, sw["state_topic"])
		assertSameTopic(t, "program switch command", roles[0].Topics.Set, sw["command_topic"])
		btn := jsonMap(t, items[1])
		assertSameTopic(t, "program execute command", roles[1].Topics.Trigger, btn["command_topic"])
		assertRoleAvailabilityDeclared(t, roles[1].Topics.Availability, btn)
	})

	t.Run("aggregates", func(t *testing.T) {
		t.Parallel()
		alarm := hub.NewAlarmMessagesWithCentral(central, nil)
		assertSameTopic(t, "alarm_messages state",
			alarm.MQTTTopics(base, central).State,
			jsonMap(t, db.BuildAlarmMessagesDiscovery(central))["state_topic"])

		svc := hub.NewServiceMessagesWithCentral(central, nil)
		assertSameTopic(t, "service_messages state",
			svc.MQTTTopics(base, central).State,
			jsonMap(t, db.BuildServiceMessagesDiscovery(central))["state_topic"])

		inbox := hub.NewInboxWithCentral(central)
		assertSameTopic(t, "inbox state",
			inbox.MQTTTopics(base, central).State,
			jsonMap(t, db.BuildInboxDiscovery(central))["state_topic"])

		conn := hub.NewConnectivity()
		assertSameTopic(t, "connectivity state",
			conn.MQTTTopicsForInterface(base, central, iface).State,
			jsonMap(t, db.BuildConnectivityDiscovery(central, iface))["state_topic"])
	})
}

// assertSameTopic fails when the model's spelling and the declared one are
// not byte-equal, and when either is absent — an empty pair agrees vacuously
// and would let the whole test pass on a builder that stopped declaring.
func assertSameTopic(t *testing.T, what, modelTopic string, declared any) {
	t.Helper()
	got, _ := declared.(string)
	if modelTopic == "" {
		t.Errorf("%s: the model declares no topic; this test would pass vacuously", what)
		return
	}
	if got == "" {
		t.Errorf("%s: the discovery payload declares no topic, but the model owns %q", what, modelTopic)
		return
	}
	if got != modelTopic {
		t.Errorf("%s: the model owns %q, the discovery payload declares %q — Home Assistant reports "+
			"such a disagreement as an entity that never updates, and nothing else", what, modelTopic, got)
	}
}

// assertRoleAvailabilityDeclared checks that a role's own availability gate
// reaches the payload's availability list. The execute button is unavailable
// while the program is deactivated, and that is the only thing that says so.
func assertRoleAvailabilityDeclared(t *testing.T, topic string, body map[string]any) {
	t.Helper()
	if topic == "" {
		t.Fatal("the execute role declares no availability gate; this assertion would pass vacuously")
	}
	entries, _ := body["availability"].([]any)
	for _, e := range entries {
		m, _ := e.(map[string]any)
		if s, _ := m["topic"].(string); s == topic {
			return
		}
	}
	t.Errorf("the execute role's availability gate %q is in no availability entry of its discovery payload", topic)
}

// TestHubTopicLayoutIsACarrierNotASchema pins the other half of the same
// decision: [hubTopicLayout] is a [hatopic.Layout], but it is a carrier of
// strings its builder already composed and not a second implementation of
// this daemon's topic schema. Every slot-taking method must therefore return
// the same string for every coordinate.
//
// This is what makes "the model owns its topics" mechanical rather than a
// convention. A layout that started deriving from the slot would be a second
// spelling of the tree in internal/model/naming, with nothing keeping the two
// in step — and it would be the spelling the discovery payload carries, so
// the drift would reach the broker retained.
func TestHubTopicLayoutIsACarrierNotASchema(t *testing.T) {
	t.Parallel()
	l := hubTopicLayout{
		state:   "openccu-loom/ccu/hub/sysvars/x/state",
		command: "openccu-loom/ccu/hub/sysvars/x/set",
		device:  "openccu-loom/ccu/hub/programs/p/execute_available",
		bridge:  "openccu-loom/bridge/status",
	}
	slots := []hamodel.Slot{
		{},
		hamodel.S("VCU1234567", "3", hamodel.BucketValues, "TEMPERATURE").In("ccu", "HmIP-RF"),
		hamodel.S("other", "", hamodel.BucketMaster, "a", "b"),
	}
	for i, s := range slots {
		if got := l.State(s); got != l.state {
			t.Errorf("slot %d: State() = %q, want the carried %q — the layout is deriving from the coordinate",
				i, got, l.state)
		}
		if got := l.Command(s); got != l.command {
			t.Errorf("slot %d: Command() = %q, want the carried %q", i, got, l.command)
		}
		if got := l.Availability(s); got != l.device {
			t.Errorf("slot %d: Availability() = %q, want the carried %q", i, got, l.device)
		}
	}
}
