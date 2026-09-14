// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package contract

// TestMQTTTopicSchemaDocExamples pins every concrete topic string that
// appears verbatim in docs/mqtt-topic-schema.md against the
// live [mqtt.TopicBuilder] output.
//
// A failure means one of two things:
// - TopicBuilder changed without updating the schema doc (code drift).
// - The schema doc was updated with a new shape that has not yet been
// implemented (spec-ahead-of-code drift).
//
// What a PASS here does NOT mean: that anything publishes the topic. A
// builder nobody calls renders its documented string perfectly, which is
// how `<base>/<central>/hub/status` stayed pinned and green while no
// daemon build ever put a byte on it. That half is
// TestMQTTDocumentedTopicsHaveAProducer's job
// (mqtt_topic_schema_producer_test.go).
//
// Either case requires a deliberate reconciliation: fix the doc to match
// the code, or fix the code to match the doc. Do NOT just silence the
// failure.
//
// Source: docs/mqtt-topic-schema.md §"Concrete examples" and §"Schema".
//
// Assumption fixture: base="openccu-loom", central="GoOtto",
// interface=hmenum.InterfaceHmIPRF, device address="000C9709AEF157",
// channel=1.
//
// The `iface` argument every TopicBuilder method takes is a *wire*
// interface id — `<central>-<interface>`. Production callers never spell
// one: they pass `ev.Interface`, which the CCU produced and
// [hmtypes.NewWireInterfaceID] is the constructor for. This test therefore
// derives it from the central name and the bare interface enum, the same
// two inputs the daemon has, rather than typing the wire form out. Typing
// it out is what let `openccu-loom/GoOtto/HmIP-RF/…` stand in the schema
// doc and in this test simultaneously, green, for as long as it did:
// [hmtypes.ParseWireInterfaceID] validates nothing, so a bare token handed
// to the builder renders a bare token back and the pin confirmed its own
// input.

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/naming"
	"github.com/SukramJ/openccu-loom/internal/north/mqtt"
	"github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// docTopicCase pairs a human-readable label, the topic string from
// docs/mqtt-topic-schema.md, and the string produced by the
// TopicBuilder for the same inputs.
type docTopicCase struct {
	name string
	// docTopic is the verbatim string from the migration doc.
	docTopic string
	// got is the TopicBuilder output for the same semantic inputs.
	got string
}

// TestMQTTTopicSchemaDoc_StateTopics exercises the "State topics" table
// and the "Concrete mapping examples" section for per-DP state.
func TestMQTTTopicSchemaDoc_StateTopics(t *testing.T) {
	t.Parallel()
	runDocTopicCases(t, stateTopicCases())
}

func stateTopicCases() []docTopicCase {
	b := mqtt.NewTopicBuilder("openccu-loom")
	const (
		central = "GoOtto"
		addr    = "000C9709AEF157"
		ch      = 1
	)
	// Built, not typed — see the file header.
	iface := hmtypes.NewWireInterfaceID(central, hmenum.InterfaceHmIPRF).String()

	cases := []docTopicCase{
		{
			// §"State topics" table row 1: Per-DP VALUES state
			// §"Concrete mapping examples" / "Actual temperature"
			name:     "values-state/ACTUAL_TEMPERATURE",
			docTopic: "openccu-loom/GoOtto/GoOtto-HmIP-RF/000C9709AEF157/1/values/ACTUAL_TEMPERATURE",
			got:      b.ParameterState(central, iface, addr, ch, payload.BucketValues, "ACTUAL_TEMPERATURE"),
		},
		{
			// §"State topics" table row 2: Per-DP MASTER state
			name:     "master-state/TEMPERATURE_MINIMUM",
			docTopic: "openccu-loom/GoOtto/GoOtto-HmIP-RF/000C9709AEF157/1/master/TEMPERATURE_MINIMUM",
			got:      b.ParameterState(central, iface, addr, ch, payload.BucketMaster, "TEMPERATURE_MINIMUM"),
		},
		{
			// §"State topics" table row 3: Custom-DP derived state
			name:     "custom-state/climate",
			docTopic: "openccu-loom/GoOtto/GoOtto-HmIP-RF/000C9709AEF157/1/custom/climate",
			got: b.SlotState(central, iface, payload.TopicSlot{
				Address:   addr,
				Channel:   ch,
				Bucket:    payload.BucketCustom,
				Parameter: "climate",
			}),
		},
		{
			// §"State topics" table row 4: Device availability
			name:     "device-availability",
			docTopic: "openccu-loom/GoOtto/GoOtto-HmIP-RF/000C9709AEF157/availability",
			got:      b.DeviceAvailability(central, iface, addr),
		},
		{
			// §"State topics" table row 5: Device info snapshot
			name:     "device-info",
			docTopic: "openccu-loom/GoOtto/GoOtto-HmIP-RF/000C9709AEF157/info",
			got:      b.DeviceInfo(central, iface, addr),
		},
		{
			// §"State topics" table row 6: Device diagnostics
			name:     "device-diagnostics",
			docTopic: "openccu-loom/GoOtto/GoOtto-HmIP-RF/000C9709AEF157/diagnostics",
			got:      b.DeviceDiagnostics(central, iface, addr),
		},
	}

	return cases
}

// TestMQTTTopicSchemaDoc_CommandTopics exercises the "Command (set) topics"
// table rows.
func TestMQTTTopicSchemaDoc_CommandTopics(t *testing.T) {
	t.Parallel()
	runDocTopicCases(t, commandTopicCases())
}

func commandTopicCases() []docTopicCase {
	b := mqtt.NewTopicBuilder("openccu-loom")
	const (
		central = "GoOtto"
		addr    = "000C9709AEF157"
		ch      = 1
	)
	// Built, not typed — see the file header.
	iface := hmtypes.NewWireInterfaceID(central, hmenum.InterfaceHmIPRF).String()

	cases := []docTopicCase{
		{
			// §"Command topics" table row 1: Write single parameter VALUES
			// §"Concrete mapping examples" / "Set-point temperature"
			name:     "values-set/SET_POINT_TEMPERATURE",
			docTopic: "openccu-loom/GoOtto/GoOtto-HmIP-RF/000C9709AEF157/1/values/SET_POINT_TEMPERATURE/set",
			got:      b.ParameterCommand(central, iface, addr, ch, payload.BucketValues, "SET_POINT_TEMPERATURE"),
		},
		{
			// §"Command topics" table row 2: Write MASTER parameter
			name:     "master-set/TEMPERATURE_MINIMUM",
			docTopic: "openccu-loom/GoOtto/GoOtto-HmIP-RF/000C9709AEF157/1/master/TEMPERATURE_MINIMUM/set",
			got:      b.ParameterCommand(central, iface, addr, ch, payload.BucketMaster, "TEMPERATURE_MINIMUM"),
		},
		{
			// §"Command topics" table row 3: Custom-DP service method
			// §"Concrete mapping examples" / "Climate service method"
			name:     "custom-service-method/climate/set_mode",
			docTopic: "openccu-loom/GoOtto/GoOtto-HmIP-RF/000C9709AEF157/1/custom/climate/set/set_mode",
			got: b.CustomDPServiceMethod(central, iface,
				payload.TopicSlot{Address: addr, Channel: ch, Bucket: payload.BucketCustom, Parameter: "climate"},
				"set_mode"),
		},
	}

	return cases
}

// TestMQTTTopicSchemaDoc_BridgeHubTopics exercises the "Bridge / hub status"
// table and the concrete hub examples.
func TestMQTTTopicSchemaDoc_BridgeHubTopics(t *testing.T) {
	t.Parallel()
	runDocTopicCases(t, bridgeHubTopicCases())
}

func bridgeHubTopicCases() []docTopicCase {
	b := mqtt.NewTopicBuilder("openccu-loom")
	const central = "GoOtto"
	// The `<iface>` in the connectivity row is the same wire id as in the
	// datapoint rows: production reaches it through
	// `WireInterfaceID(centralName, iface)` (hub_mqtt_publisher.go) and
	// `dp.InterfaceID`, never a bare token. Built here for the same reason.
	iface := hmtypes.NewWireInterfaceID(central, hmenum.InterfaceHmIPRF).String()

	cases := []docTopicCase{
		{
			// §"Bridge / hub status" row: Bridge online/offline (LWT)
			name:     "bridge-status",
			docTopic: "openccu-loom/bridge/status",
			got:      b.BridgeStatus(),
		},
		{
			// §"Bridge / hub status" row: Bridge health
			name:     "bridge-health",
			docTopic: "openccu-loom/bridge/health",
			got:      b.BridgeHealth(),
		},
		{
			// §"Reserved `hub/` shapes" row — NOT a published topic.
			// Pinned so the reserved shape cannot drift; that nothing
			// publishes it is asserted by
			// TestMQTTDocumentedTopicsHaveAProducer, which this test
			// cannot see (ADR 0011, amendment 2026-09-12).
			name:     "hub-status-reserved",
			docTopic: "openccu-loom/GoOtto/hub/status",
			got:      b.HubStatus(central),
		},
		{
			// §"Reserved `hub/` shapes" row — NOT a published topic.
			name:     "hub-info-reserved",
			docTopic: "openccu-loom/GoOtto/hub/info",
			got:      b.HubInfo(central),
		},
		{
			// §"Reserved `hub/` shapes" row — NOT a published topic, and
			// never documented as one. It reached no pin at all before
			// the reserved table existed.
			name:     "hub-diagnostics-reserved",
			docTopic: "openccu-loom/GoOtto/hub/diagnostics",
			got:      b.HubDiagnostics(central),
		},
		{
			// §"Bridge / hub status" row: System-variable state
			// §"Concrete mapping examples" / "System variable"
			name:     "hub-sysvar-state/Presence",
			docTopic: "openccu-loom/GoOtto/hub/sysvars/Presence/state",
			got:      naming.MQTTHubSysvarState(b.Base, central, "Presence"),
		},
		{
			// §"Bridge / hub status" row: System-variable set
			name:     "hub-sysvar-set/Presence",
			docTopic: "openccu-loom/GoOtto/hub/sysvars/Presence/set",
			got:      naming.MQTTHubSysvarCommand(b.Base, central, "Presence"),
		},
		{
			// §"Bridge / hub status" row: Program trigger
			name:     "hub-program-trigger/12",
			docTopic: "openccu-loom/GoOtto/hub/programs/12/trigger",
			got:      naming.MQTTHubProgramTrigger(b.Base, central, "12"),
		},
		{
			// §"Bridge / hub status" row: Interface connectivity
			name:     "hub-connectivity/GoOtto-HmIP-RF",
			docTopic: "openccu-loom/GoOtto/hub/connectivity/GoOtto-HmIP-RF",
			got:      naming.MQTTHubConnectivity(b.Base, central, iface),
		},
		{
			// §"Bridge / hub status" row: System status event
			name:     "system-status",
			docTopic: "openccu-loom/GoOtto/system/status",
			got:      b.SystemStatus(central),
		},
	}

	return cases
}

// TestMQTTTopicSchemaDoc_DiscoveryTopic exercises the HA Discovery config
// Topic, which the doc states is identical
func TestMQTTTopicSchemaDoc_DiscoveryTopic(t *testing.T) {
	t.Parallel()

	b := mqtt.NewTopicBuilder("openccu-loom")

	// §"HA Discovery" table: homeassistant/<component>/<node_id>/<object_id>/config
	got := b.DiscoveryConfig("climate", "gootto_000c9709aef157", "1_climate")
	want := "homeassistant/climate/gootto_000c9709aef157/1_climate/config"
	if got != want {
		t.Errorf("DiscoveryConfig mismatch:\n  doc says: %q\n  builder:  %q\n  → update docs/mqtt-topic-schema.md or fix TopicBuilder",
			want, got)
	}
}

// TestMQTTTopicSchemaDoc_DiscoveryNodeScopeIsNotATopic holds
// [mqtt.TopicBuilder.DiscoveryNodeScope] to the claim that lets it out of
// docs/mqtt-topic-schema.md: it returns part of one segment, not a topic.
//
// The producer inventory classifies it `segmentOf: "DiscoveryConfig"`, which
// is the one class that excuses a producer from having a documented row. That
// excuse is only sound while what it returns genuinely cannot be published —
// a fragment carrying a `/` would be a topic shape hiding behind the
// classification, and the document would be a subset of the wire again, which
// is the failure the whole guard exists to stop.
//
// It also pins the scope's two ends, which are the behaviour the rest of the
// daemon depends on: empty on the default topic base (so nothing moves for an
// installation that never set one), and a trailing `_` otherwise (so it
// composes onto a node id as a segment prefix rather than a segment of its
// own).
func TestMQTTTopicSchemaDoc_DiscoveryNodeScopeIsNotATopic(t *testing.T) {
	t.Parallel()

	for _, base := range []string{"openccu-loom", "gh", "home/hm", "Büro", ""} {
		scope := mqtt.NewTopicBuilder(base).DiscoveryNodeScope()
		if strings.Contains(scope, "/") {
			t.Errorf("DiscoveryNodeScope(%q) = %q, which carries a `/` — that is a topic shape, and a "+
				"topic shape needs a row in docs/mqtt-topic-schema.md rather than the segmentOf class",
				base, scope)
		}
		if scope != "" && !strings.HasSuffix(scope, "_") {
			t.Errorf("DiscoveryNodeScope(%q) = %q, which does not end in `_` — it is prefixed onto a "+
				"node id, so without the separator it fuses with the central slug", base, scope)
		}
	}

	// The default base contributes nothing: this is what makes the change
	// that introduced the scope a no-op for every installation that never set
	// `north.mqtt.topic_base`.
	if scope := mqtt.NewTopicBuilder("openccu-loom").DiscoveryNodeScope(); scope != "" {
		t.Errorf("the default topic base produced scope %q; every single-daemon installation's retained "+
			"discovery configs would move for a collision it cannot have", scope)
	}
	// NewTopicBuilder("") fills in the default, so an unset base is the
	// default base and must behave identically.
	if scope := mqtt.NewTopicBuilder("").DiscoveryNodeScope(); scope != "" {
		t.Errorf("an empty topic base produced scope %q; NewTopicBuilder fills in the default, so it "+
			"must reach the same answer as naming it", scope)
	}
}

// runDocTopicCases is the shared assertion for every pinned table above.
func runDocTopicCases(t *testing.T, cases []docTopicCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if tc.got != tc.docTopic {
				t.Errorf("topic mismatch for %q:\n  doc says: %q\n  builder:  %q\n  → update docs/mqtt-topic-schema.md or fix TopicBuilder",
					tc.name, tc.docTopic, tc.got)
			}
		})
	}
}

// docTopicLiteralRE matches a backticked topic literal in
// docs/mqtt-topic-schema.md: a `/`-separated string with no placeholder
// segment. A row written against `<iface>` is a shape, not an example, and
// the shape rows are pinned by the tables above through the fixture.
var docTopicLiteralRE = regexp.MustCompile("`(openccu-loom/[A-Za-z0-9_./-]+)`")

// TestMQTTTopicSchemaDoc_ConcreteExamplesAreAllPinned closes the loop the
// rest of this file leaves open.
//
// Every other test here compares a builder call against a string typed into
// this file. That pins the builder, and it pins nothing at all about the
// document: the "Concrete examples" table shipped
// `openccu-loom/GoOtto/HmIP-RF/…` — a bare interface token the daemon has
// never published — while this file typed the same bare token into both
// sides of its own comparison and stayed green through every release that
// carried it. An operator copying that row got a topic with no retained
// message on it, and the doctest meant to stop exactly that agreed with the
// doc because it had transcribed it.
//
// So this test reads the document. Every concrete topic literal under the
// default base that appears in docs/mqtt-topic-schema.md has to be a
// docTopic some case above pins — and every one of those cases now gets its
// `got` from the real builder, driven by the inputs a production caller has
// (a central name and a bare interface enum) rather than by the wire string
// the answer is made of. A literal that reaches the document without a pin
// fails here; a pin that drifts from the builder fails above. Neither half
// can be satisfied by transcription any more.
func TestMQTTTopicSchemaDoc_ConcreteExamplesAreAllPinned(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile(filepath.Join(repoRoot(t), "docs", "mqtt-topic-schema.md"))
	if err != nil {
		t.Fatalf("read schema doc: %v", err)
	}

	pinned := map[string]bool{}
	for _, set := range [][]docTopicCase{stateTopicCases(), commandTopicCases(), bridgeHubTopicCases()} {
		for _, tc := range set {
			pinned[tc.docTopic] = true
		}
	}

	seen := 0
	for _, m := range docTopicLiteralRE.FindAllStringSubmatch(string(raw), -1) {
		topic := m[1]
		// Placeholder rows describe a shape; only fully concrete rows are
		// examples an operator can copy.
		if strings.ContainsAny(topic, "<>+#") {
			continue
		}
		// A prefix, not a whole topic (the wildcard-subscription section).
		if !strings.Contains(strings.TrimPrefix(topic, "openccu-loom/"), "/") {
			continue
		}
		seen++
		if !pinned[topic] {
			t.Errorf("docs/mqtt-topic-schema.md carries the concrete topic %q, which no case in this "+
				"file pins against the builder — an operator can copy it and the daemon may never "+
				"publish it. Add a docTopicCase, or make the row a placeholder shape.", topic)
		}
	}

	// Anti-vacuity: a regexp that stopped matching would pass silently. The
	// floor is the size of the "Concrete examples" table — the rows written
	// to be copied, and the ones the bare-interface defect shipped in. If a
	// row is removed rather than fixed, that is a deliberate edit and this
	// number moves with it.
	if seen < 4 {
		t.Errorf("only %d concrete topic literals found in docs/mqtt-topic-schema.md; the extractor has "+
			"stopped seeing the document and this test is no longer checking anything", seen)
	}
}
