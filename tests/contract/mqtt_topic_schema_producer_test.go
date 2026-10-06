// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package contract

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"
)

// promiseKind says how the daemon keeps a topic class documented in
// docs/mqtt-topic-schema.md.
type promiseKind int

const (
	// promisePublished: the daemon puts bytes on the topic. At least one
	// of the producers must have a call site in a non-test file outside
	// the builder packages themselves.
	promisePublished promiseKind = iota
	// promiseCommand: the daemon subscribes and never publishes. The
	// filters are `+`-wildcards rather than per-topic builder calls, so a
	// call-site count says nothing about them — see the file comment.
	promiseCommand
	// promiseReserved: the shape has a builder and deliberately no
	// publisher. Every producer must have ZERO production call sites.
	promiseReserved
	// promiseUnwired: the shape has a builder WITH production call sites
	// and still no wire behaviour at either end — nothing publishes it and
	// no subscription filter matches it. Distinct from promiseReserved,
	// whose builders are called from nowhere at all: an unwired spelling is
	// handed to a discovery payload that declares no binding for it, so a
	// call-site count says "alive" about a topic that is not. Like
	// promiseCommand these rows carry a recorded reason rather than a
	// count; see the note on [TestMQTTDocumentedTopicsHaveAProducer] for
	// what that does and does not buy.
	promiseUnwired
)

// docTopicPromise classifies one documented topic class.
type docTopicPromise struct {
	kind promiseKind
	// producers are function or method names whose call sites are
	// counted. For promisePublished one of them must be called from
	// production code; for promiseReserved none of them may be.
	producers []string
	// why records the reasoning for a promiseCommand row, or the reason a
	// reserved shape is kept.
	why string
}

// mqttDocTopicPromises maps every topic class documented in a table of
// docs/mqtt-topic-schema.md to the way the daemon keeps that promise.
//
// The keys are the verbatim topic patterns from the document's tables.
// TestMQTTDocTopicTableIsFullyClassified keeps this map and the document
// in step in both directions, so a new documented shape fails the suite
// until someone says here how it is produced.
var mqttDocTopicPromises = map[string]docTopicPromise{
	// --- Instance topics (mqtt-smarthome 2.0 §3.1, §6, §7) -------------
	// `connected` is the Last Will and the runtime's level; the runtime
	// reaches it through bridgeStatusLayout.Connected, which is a call site
	// of TopicBuilder.Connected outside the builder files.
	"<name>/connected": {kind: promisePublished, producers: []string{"Connected"}},
	"<name>/info":      {kind: promisePublished, producers: []string{"Info"}},
	"<name>/maintenance/stats": {
		kind: promisePublished, producers: []string{"Maintenance"},
	},
	"<name>/maintenance/set/loglevel": {
		kind: promiseCommand, producers: []string{"Maintenance"},
		why: "routed as `<name>/maintenance/set/#` by go-hamqtt's publisher.Instance.Register on the " +
			"command plane's router (CommandSubscriber.WithMaintenance)",
	},
	"<name>/maintenance/set/restart": {
		kind: promiseCommand, producers: []string{"Maintenance"},
		why: "routed by the same `<name>/maintenance/set/#` route; refused unless supervised",
	},

	// --- State topics ---------------------------------------------------
	"<name>/status/<central>/<iface>/<addr>/<ch>/values/<param>": {
		kind: promisePublished, producers: []string{"ParameterState", "SlotState"},
	},
	"<name>/status/<central>/<iface>/<addr>/<ch>/master/<param>": {
		kind: promisePublished, producers: []string{"ParameterState", "SlotState"},
	},
	"<name>/status/<central>/<iface>/<addr>/<ch>/calculated/<param>": {
		kind: promisePublished, producers: []string{"ParameterState", "SlotState"},
	},
	"<name>/status/<central>/<iface>/<addr>/<ch>/custom/<kind>": {
		kind: promisePublished, producers: []string{"SlotState"},
	},
	"<name>/status/<central>/<iface>/<addr>/<ch>/event": {
		kind: promisePublished, producers: []string{"ChannelEvent"},
	},
	"<name>/status/<central>/<iface>/<addr>/<ch>/impulse": {
		kind: promisePublished, producers: []string{"ChannelImpulse"},
	},
	"<name>/status/<central>/<iface>/<addr>/<ch>/device_error": {
		kind: promisePublished, producers: []string{"ChannelDeviceError"},
	},
	"<name>/status/<central>/<iface>/<addr>/online": {
		kind: promisePublished, producers: []string{"DeviceAvailability"},
	},
	"<name>/status/<central>/<iface>/<addr>/info": {
		kind: promisePublished, producers: []string{"DeviceInfo"},
	},
	"<name>/status/<central>/<iface>/<addr>/diagnostics": {
		kind: promisePublished, producers: []string{"DeviceDiagnostics"},
	},
	"<name>/status/<central>/<iface>/<addr>/update": {
		kind: promisePublished, producers: []string{"DeviceUpdateState"},
	},
	"<name>/meta/<central>/<iface>/<addr>/<ch>/values/<param>": {
		kind: promisePublished, producers: []string{"ParameterConfig", "SlotConfig"},
	},
	"<name>/meta/<central>/<iface>/<addr>/<ch>/custom/<kind>": {
		kind: promisePublished, producers: []string{"SlotConfig"},
	},
	"<name>/status/<central>/<iface>/<addr>/<ch>/combined/<kind>": {
		kind: promisePublished, producers: []string{"CombinedState"},
	},
	"<name>/status/<central>/<iface>/<addr>/<ch>/week_profile": {
		kind: promisePublished, producers: []string{"WeekProfileState"},
	},
	"<name>/status/<central>/<iface>/<addr>/<ch>/schedule/active_entries": {
		kind: promisePublished, producers: []string{"ScheduleEntityState"},
	},
	"<name>/status/<central>/<iface>/<addr>/<ch>/schedule/attributes": {
		kind: promisePublished, producers: []string{"ScheduleEntityAttrs"},
	},
	"<name>/status/<central>/<iface>/<addr>/<ch>/schedule/switch/<key>": {
		kind: promisePublished, producers: []string{"ScheduleSwitchState"},
	},
	"<name>/status/alarm/<zone>/panel": {
		kind: promisePublished, producers: []string{"alarmStateTopic"},
	},
	"<name>/status/alarm/<zone>/online": {
		kind: promisePublished, producers: []string{"alarmAvailabilityTopic"},
	},
	"<name>/status/alarm/<zone>/event": {
		kind: promisePublished, producers: []string{"alarmEventTopic"},
	},
	"<name>/status/alarm/<zone>/triggered_motion": {
		kind: promisePublished, producers: []string{"alarmTriggeredMotionTopic"},
	},

	// --- Command (set) topics -------------------------------------------
	// The daemon subscribes to these with wildcard filters below
	// `<name>/set/`, so no builder call site exists per topic —
	// TopicBuilder.ParameterCommand has no production caller at all and the
	// shape is still honoured.
	"<name>/set/<central>/<iface>/<addr>/<ch>/values/<param>": {
		kind: promiseCommand,
		why:  "matched by the daemon's data-point filter `<name>/set/+/+/+/+/+/+`",
	},
	"<name>/set/<central>/<iface>/<addr>/<ch>/master/<param>": {
		kind: promiseCommand,
		why:  "matched by the same data-point filter as the VALUES form",
	},
	"<name>/set/<central>/<iface>/<addr>/<ch>/custom/<kind>/<method>": {
		kind: promiseCommand, producers: []string{"CustomDPServiceMethod"},
		why: "declared as command_topic in discovery, consumed by the `<name>/set/+/+/+/+/custom/+/+` filter",
	},
	"<name>/set/<central>/<iface>/<addr>/<ch>/combined/<kind>": {
		kind: promiseCommand, producers: []string{"CombinedCommand"},
		why: "declared as command_topic in combined-DP discovery; dispatched from the data-point " +
			"filter, which the combined kind sits in at the bucket position",
	},
	"<name>/set/<central>/<iface>/<addr>/<ch>/week_profile": {
		kind: promiseCommand, producers: []string{"WeekProfileCommand"},
		why: "declared as command_topic in week-profile discovery; consumed by the literal " +
			"`<name>/set/+/+/+/+/week_profile` filter",
	},
	"<name>/set/<central>/<iface>/<addr>/<ch>/schedule/switch/<key>": {
		kind: promiseCommand, producers: []string{"ScheduleSwitchCommand"},
		why: "declared as command_topic in schedule-switch discovery, consumed by the " +
			"`<name>/set/+/+/+/+/schedule/switch/+` filter",
	},
	"<name>/set/<central>/devices/<addr>/cdps/<cdp>/<op>": {
		kind: promiseCommand, producers: []string{"CustomDPInvoke", "MQTTCustomDPInvoke"},
		why: "an action item dispatched from the data-point filter, which has its length. Like " +
			"ParameterCommand the builder has zero production callers and the shape is honoured",
	},
	"<name>/set/<central>/hub/install_mode/<iface>": {
		kind: promiseCommand, producers: []string{"MQTTHubInstallModeCommand"},
		why: "declared as command_topic in install-mode discovery, consumed by a wildcard filter",
	},
	"<name>/set/system/addon_update": {
		kind: promiseCommand, producers: []string{"AddonUpdateCommand"},
		why: "declared as command_topic in add-on update discovery; consumed by the " +
			"one literal (non-wildcard) route the subscriber registers",
	},
	"<name>/set/alarm/<zone>/panel": {
		kind: promiseCommand, producers: []string{"alarmCommandTopic"},
		why: "declared as command_topic in alarm discovery, consumed by a wildcard filter",
	},
	"<name>/set/<central>/hub/sysvars/<sysvar>": {
		kind: promiseCommand, producers: []string{"MQTTHubSysvarCommand"},
		why: "declared as command_topic in hub discovery, consumed by a wildcard filter",
	},
	"<name>/set/<central>/hub/programs/<id>/active": {
		kind: promiseCommand, producers: []string{"MQTTHubProgramSet"},
		why: "declared as command_topic in hub discovery, consumed by a wildcard filter",
	},
	"<name>/set/<central>/hub/programs/<id>/trigger": {
		kind: promiseCommand, producers: []string{"MQTTHubProgramTrigger"},
		why: "declared as command_topic in hub discovery, consumed by a wildcard filter",
	},

	// --- HA Discovery ---------------------------------------------------
	"homeassistant/<component>/<node_id>/<object_id>/config": {
		kind: promisePublished, producers: []string{"DiscoveryConfig"},
	},

	// --- Bridge / hub status --------------------------------------------
	"<name>/status/<central>/hub/sysvars/<sysvar>": {
		kind: promisePublished, producers: []string{"MQTTHubSysvarState"},
	},
	"<name>/status/<central>/hub/programs/<id>/active": {
		kind: promisePublished, producers: []string{"MQTTHubProgramState"},
	},
	"<name>/status/<central>/hub/programs/<id>/execute_available": {
		kind: promisePublished, producers: []string{"MQTTHubProgramExecuteAvailability"},
	},
	"<name>/status/<central>/hub/connectivity/<iface>": {
		kind: promisePublished, producers: []string{"MQTTHubConnectivity"},
	},
	"<name>/status/<central>/system/status": {
		kind: promisePublished, producers: []string{"SystemStatus"},
	},
	"<name>/status/<central>/hub/install_mode/<iface>": {
		kind: promisePublished, producers: []string{"MQTTHubInstallModeForInterface"},
	},
	"<name>/status/<central>/hub/update": {
		kind: promisePublished, producers: []string{"HubUpdate"},
	},
	"<name>/status/<central>/hub/alarm_messages": {
		kind: promisePublished, producers: []string{"MQTTHubAlarmMessages"},
	},
	"<name>/status/<central>/hub/service_messages": {
		kind: promisePublished, producers: []string{"MQTTHubServiceMessages"},
	},
	"<name>/status/<central>/hub/inbox": {
		kind: promisePublished, producers: []string{"MQTTHubInbox"},
	},
	"<name>/status/<central>/system/health_score": {
		kind: promisePublished, producers: []string{"HubSystemHealthScore"},
	},
	"<name>/status/<central>/system/latency": {
		kind: promisePublished, producers: []string{"HubConnectionLatency"},
	},
	"<name>/status/<central>/system/last_event_age": {
		kind: promisePublished, producers: []string{"HubLastEventAge"},
	},
	"<name>/status/system/addon_update": {
		kind: promisePublished, producers: []string{"AddonUpdateState"},
	},
	// The per-CCU availability gate (ADR 0011 amendments 2026-09-12/13),
	// since ADR 0083 the central's `online` status item.
	"<name>/status/<central>/online": {
		kind: promisePublished, producers: []string{"HubStatus"},
	},

	// --- Reserved `hub/` shapes — nothing publishes here ------------------
	// Documented as reserved after the 2026-09-12 amendment to ADR 0011.
	// Promoting one of these to a published class means giving it a
	// publisher AND moving its row into the table above; flipping the row
	// alone, or adding the publisher alone, fails this test.
	"<name>/status/<central>/hub/info": {
		kind: promiseReserved, producers: []string{"HubInfo", "MQTTHubInfo"},
		why: "its fields are in the HA discovery device block instead",
	},
	"<name>/status/<central>/hub/diagnostics": {
		kind: promiseReserved, producers: []string{"HubDiagnostics", "MQTTHubDiagnostics"},
		why: "per-device data points and the system/* metric items carry this",
	},

	// --- Unwired command spelling ---------------------------------------
	"<name>/set/<central>/<iface>/<addr>/update": {
		kind: promiseUnwired, producers: []string{"DeviceUpdateCommand", "MQTTDeviceUpdateCommand"},
		why: "the update entity's topic layout answers with it, but declares no command_topic, " +
			"and no subscription filter has this shape — flashing firmware from a possibly " +
			"retained broker payload is unsafe",
	},

	// --- Security & Safety plane ----------------------------------------
	"<name>/status/security/severity": {kind: promisePublished, producers: []string{"securityStateTopic"}},
	"<name>/status/security/alarm":    {kind: promisePublished, producers: []string{"securityStateTopic"}},
	"<name>/status/security/problem":  {kind: promisePublished, producers: []string{"securityStateTopic"}},
	"<name>/status/security/health":   {kind: promisePublished, producers: []string{"securityStateTopic"}},
	"<name>/status/security/class/<class>": {
		kind: promisePublished, producers: []string{"securityClassTopic"},
	},
	"<name>/status/security/zone/<slug>": {
		kind: promisePublished, producers: []string{"securityZoneTopic"},
	},
	"<name>/status/security/last_alarm": {kind: promisePublished, producers: []string{"securityStateTopic"}},
	"<name>/status/security/last_fault": {kind: promisePublished, producers: []string{"securityStateTopic"}},
	"<name>/status/security/event":      {kind: promisePublished, producers: []string{"securityStateTopic"}},
	"<name>/status/security/fault":      {kind: promisePublished, producers: []string{"securityStateTopic"}},
	"<name>/status/security/online": {
		kind: promisePublished, producers: []string{"securityAvailabilityTopic"},
	},
}

// mqttBuilderFiles are the two files that define the topic vocabulary:
// internal/north/mqtt/topics.go's [mqtt.TopicBuilder] methods and
// internal/model/naming/pathdata.go's MQTT* functions. They are the input
// of the code -> doc direction ([mqttTopicProducerNames]) and they get
// special treatment in the doc -> code direction ([mqttProducerCallSites]).
var mqttBuilderFiles = []string{
	"internal/north/mqtt/topics.go",
	"internal/model/naming/pathdata.go",
}

// Why these files need special treatment in the call-site scan: they
// delegate among themselves, so a builder called only from here is called
// only by itself. That is the exact hole the doctest fell into —
// naming.MQTTHubStatus had one non-test caller, TopicBuilder.HubStatus,
// which had none.
//
// The scan used to answer that by skipping both files whole. It no longer
// does. A whole-file skip is broader than the problem: it also discards a
// call made from a NON-producer function in the same file, which is an
// ordinary production call site and the only one some builders have. The
// skip is now per enclosing function — a call counts unless the function
// making it is itself one of the producers in [mqttTopicProducers].
//
// Concretely: TopicBuilder.HubStatus calling naming.MQTTHubStatus is still
// discarded (a producer delegating to a producer, which is what the
// reserved rows must not be fooled by), while TopicBuilder.systemMetricTopics
// calling HubSystemHealthScore now counts, because systemMetricTopics is not
// a topic producer — it is the retained-orphan sweep's enumeration helper,
// and a real consumer of the shape.

// parseGoFileOrRecord parses one file, or records why it could not and
// returns nil.
//
// A file the scan cannot read might hold the only call site of a
// documented producer, which would make the guard report a kept promise
// as unkept — or, worse, a published shape as reserved. So a parse
// failure is collected and fails the guard in
// [mqttProducerCallSites] rather than being skipped quietly.
func parseGoFileOrRecord(fset *token.FileSet, path, rel string, unparsed *[]string) *ast.File {
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		*unparsed = append(*unparsed, rel+": "+err.Error())
		return nil
	}
	return file
}

// mqttProducerCallSites parses every non-test .go file under internal/,
// cmd/ and pkg/ and returns, per called function or method name, the
// files that call it.
//
// The scan is name-based on purpose: it uses go/parser without type
// checking, so it costs about a second instead of a full go/packages
// load of the module, and it resolves exactly what a reviewer's grep
// resolves. The looseness is one-directional — a same-named method on an
// unrelated type would make the count too HIGH, never too low — so it
// can leave a producer looking alive, which is the state the published
// rows are asserted to be in anyway. The reserved rows are the ones a
// false positive would matter for, and their three names
// (HubStatus/HubInfo/HubDiagnostics and the naming.MQTT* trio) are
// unique in the module.
func mqttProducerCallSites(t *testing.T, root string) map[string][]string {
	t.Helper()

	calls := make(map[string]map[string]bool)
	fset := token.NewFileSet()
	var unparsed []string

	for _, sub := range []string{"internal", "cmd", "pkg"} {
		err := filepath.Walk(filepath.Join(root, sub), func(path string, info os.FileInfo, walkErr error) error {
			if walkErr != nil || info.IsDir() {
				return walkErr //nolint:wrapcheck // walk error is returned as-is
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				return relErr //nolint:wrapcheck // surfaced by the caller's t.Fatalf
			}
			rel = filepath.ToSlash(rel)

			file := parseGoFileOrRecord(fset, path, rel, &unparsed)
			if file == nil {
				return nil
			}
			builderFile := slices.Contains(mqttBuilderFiles, rel)
			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if ok && builderFile {
					if _, isProducer := mqttTopicProducers[fn.Name.Name]; isProducer {
						// A producer delegating to a producer is the builder
						// vocabulary talking to itself, not a call site.
						continue
					}
				}
				recordCalls(decl, rel, calls)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", sub, err)
		}
	}

	if len(unparsed) > 0 {
		sort.Strings(unparsed)
		t.Fatalf("the producer scan could not parse %d file(s), so its call-site counts are incomplete:\n  %s",
			len(unparsed), strings.Join(unparsed, "\n  "))
	}

	out := make(map[string][]string, len(calls))
	for name, files := range calls {
		list := make([]string, 0, len(files))
		for f := range files {
			list = append(list, f)
		}
		sort.Strings(list)
		out[name] = list
	}
	return out
}

// recordCalls walks one declaration and records every called function or
// method name against the file it was called from.
func recordCalls(decl ast.Decl, rel string, calls map[string]map[string]bool) {
	ast.Inspect(decl, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		var name string
		switch fn := call.Fun.(type) {
		case *ast.Ident:
			name = fn.Name
		case *ast.SelectorExpr:
			name = fn.Sel.Name
		default:
			return true
		}
		if calls[name] == nil {
			calls[name] = make(map[string]bool)
		}
		calls[name][rel] = true
		return true
	})
}

// docTopicPatternRe matches the first backticked topic pattern of a
// markdown table row — the `<name>/…` or `homeassistant/…` shape. The
// pre-ADR-0083 spellings in the migration table are written against `<base>`
// and are deliberately not matched: they are history, not shapes the daemon
// produces.
var docTopicPatternRe = regexp.MustCompile("`((?:<name>|homeassistant)/[^`]*)`")

// mqttDocTableTopics returns every topic pattern that appears in a table
// row of docs/mqtt-topic-schema.md.
//
// Concrete instances ("openccu-loom/GoOtto/hub/status") are skipped: the
// "Concrete examples" table renders the same classes with the fixture
// substituted, and those strings are pinned byte-for-byte by
// TestMQTTTopicSchemaDoc_* in mqtt_topic_schema_doctest_test.go.
func mqttDocTableTopics(t *testing.T, root string) []string {
	t.Helper()

	path := filepath.Join(root, "docs", "mqtt-topic-schema.md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}

	seen := make(map[string]bool)
	var out []string
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "|") {
			continue
		}
		m := docTopicPatternRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		if seen[m[1]] {
			continue
		}
		seen[m[1]] = true
		out = append(out, m[1])
	}
	sort.Strings(out)
	return out
}

// TestMQTTDocumentedTopicsHaveAProducer fails when a topic class
// documented in docs/mqtt-topic-schema.md has no production code that
// produces it — and when a shape documented as reserved acquires one.
//
// This is the half the schema doctest cannot see. That test pins a
// builder's output against a documented string, which a builder nobody
// calls satisfies perfectly: `<base>/<central>/hub/status` (today `<name>/status/<central>/online`) was promised
// as "CCU connection status" from the document's first revision, was
// pinned green by the doctest the whole time, and was never published by
// any daemon build (ADR 0011, amendment 2026-09-12). It is published now
// (amendment 2026-09-13) and its row moved to promisePublished with it —
// which is the movement this guard exists to force: the classification
// and the call site have to move together, and flipping either alone
// fails here.
//
// What it cannot do is prove bytes reach a broker; that needs a broker
// and belongs in an integration test. A production call site of the
// producing builder is the strongest statement available to a unit test,
// and it is the one that was missing. For command topics it is not even
// available: the daemon consumes those through `+`-wildcard filters, so
// no per-topic builder call exists — TopicBuilder.ParameterCommand has
// zero production callers while its two documented `/set` shapes are
// both honoured. Those rows are classified promiseCommand and carry a
// recorded reason instead of a count.
func TestMQTTDocumentedTopicsHaveAProducer(t *testing.T) {
	t.Parallel()

	const root = "../.."
	calls := mqttProducerCallSites(t, root)

	names := make([]string, 0, len(mqttDocTopicPromises))
	for topic := range mqttDocTopicPromises {
		names = append(names, topic)
	}
	sort.Strings(names)

	for _, topic := range names {
		promise := mqttDocTopicPromises[topic]
		switch promise.kind {
		case promisePublished:
			produced := false
			for _, p := range promise.producers {
				if len(calls[p]) > 0 {
					produced = true
					break
				}
			}
			if !produced {
				t.Errorf("documented topic class %q has no production producer: none of %v is called "+
					"from a non-test file outside the builder methods of %v.\n"+
					"  Either the publisher is missing (then the daemon does not keep this promise — "+
					"implement it, or move the row to the reserved table and amend ADR 0011),\n"+
					"  or the producer was renamed (then update mqttDocTopicPromises).",
					topic, promise.producers, mqttBuilderFiles)
			}
		case promiseReserved:
			for _, p := range promise.producers {
				if files := calls[p]; len(files) > 0 {
					t.Errorf("%q is documented as a RESERVED shape that nothing publishes, but %s is now "+
						"called from %v.\n"+
						"  Publishing a reserved shape is new published traffic: promote its row into the "+
						"\"Bridge / hub status\" table, reclassify it here, and record the addition in "+
						"ADR 0011 and the changelog.",
						topic, p, files)
				}
			}
		case promiseCommand, promiseUnwired:
			if promise.why == "" {
				t.Errorf("topic %q carries no recorded reason; say how the daemon consumes it, "+
					"or why nothing at either end touches it", topic)
			}
		}
	}
}

// TestMQTTDocTopicTableIsFullyClassified keeps mqttDocTopicPromises and
// the tables of docs/mqtt-topic-schema.md in step in both directions.
//
// Without it the producer guard above would be silently incomplete: a new
// documented topic class that nobody classifies would simply not be
// checked, which is how the unpublished `hub/` shapes survived four years
// of green contract runs.
func TestMQTTDocTopicTableIsFullyClassified(t *testing.T) {
	t.Parallel()

	const root = "../.."
	documented := mqttDocTableTopics(t, root)

	for _, topic := range documented {
		if _, ok := mqttDocTopicPromises[topic]; !ok {
			t.Errorf("docs/mqtt-topic-schema.md documents %q and mqttDocTopicPromises does not classify it.\n"+
				"  Add it as promisePublished (naming the builder that produces it), promiseCommand "+
				"(with the reason the daemon consumes rather than publishes it), or promiseReserved "+
				"(nothing publishes it — which needs an ADR note).", topic)
		}
	}

	inDoc := make(map[string]bool, len(documented))
	for _, topic := range documented {
		inDoc[topic] = true
	}
	classified := make([]string, 0, len(mqttDocTopicPromises))
	for topic := range mqttDocTopicPromises {
		classified = append(classified, topic)
	}
	sort.Strings(classified)
	for _, topic := range classified {
		if !inDoc[topic] {
			t.Errorf("mqttDocTopicPromises classifies %q, which no table row of "+
				"docs/mqtt-topic-schema.md mentions any more — drop the entry, or restore the row.", topic)
		}
	}
}

// --- code -> doc: the inverse direction ---------------------------------
//
// Everything above this line checks doc -> code: every documented shape has
// to be classified and produced. That is one direction, and on its own it is
// the same mistake it was built to catch. A document can keep every promise
// it makes and still be a strict SUBSET of the wire — which is what
// docs/mqtt-topic-schema.md was when this half was written: the add-on
// update, install-mode, CCU update, message-aggregate, system-metric,
// week-profile, schedule and combined-DP families all had publishers and no
// documented row, so an external consumer reading the document had no way to
// learn they exist.
//
// What follows closes it. [mqttTopicProducers] is the inventory of every
// topic-shape producer in the two files that define the vocabulary, and
// [TestMQTTTopicProducersAreDocumented] fails when the inventory and those
// files disagree, or when an inventoried shape has no row in the document.
// Adding a builder is then a three-part move — the function, the inventory
// entry, the documented row — and doing one or two of the three fails here.

// topicProducer says what one topic-building function contributes to
// docs/mqtt-topic-schema.md.
//
// Exactly one of shape / delegateOf / alias is set:
//
//   - shape: the verbatim topic pattern this producer renders, which must
//     appear in a table row of the document.
//   - delegateOf: this producer renders the same shape as the named
//     [mqtt.TopicBuilder] method, which calls it. The naming package's
//     MQTT* functions are the format strings; the builder methods are the
//     documented surface, and the document cites the builder.
//   - alias: this producer is a convenience spelling of another producer
//     (a fixed bucket argument, say) and adds no shape of its own.
//   - segmentOf: this producer returns a FRAGMENT of another producer's
//     shape — part of one segment, not a topic — so there is no row for it
//     to have. It is the narrowest of the four and the easiest to abuse, so
//     it carries the strictest reading: a producer classified this way must
//     return something that cannot be published as a topic on its own. The
//     class exists because the scan is syntactic (every exported method on
//     *TopicBuilder that the file declares), and a builder may legitimately
//     export a piece of a topic for the sweeps that have to recognise it
//     again. Reaching for it when the function really does render a whole
//     topic is how the document goes back to being a subset of the wire.
type topicProducer struct {
	shape      string
	delegateOf string
	alias      string
	segmentOf  string
}

// mqttTopicProducers is the inventory of every exported topic-shape
// producer in internal/north/mqtt/topics.go (methods on *TopicBuilder) and
// internal/model/naming/pathdata.go (MQTT*-prefixed functions and methods).
//
// It is a hand-maintained table on purpose. The scan that keeps it honest
// ([mqttTopicProducerNames]) can enumerate the functions but cannot know
// which documented shape a given one renders — that is the judgement this
// table records, and the reason a new producer has to stop and say it.
var mqttTopicProducers = map[string]topicProducer{
	// --- internal/north/mqtt/topics.go: *TopicBuilder ------------------
	"Connected": {shape: "<name>/connected"},
	"Info":      {shape: "<name>/info"},
	// Maintenance renders every `<name>/maintenance/<item…>` topic; the
	// documented rows are `stats` and the two `set/…` commands.
	"Maintenance":     {shape: "<name>/maintenance/stats"},
	"DiscoveryConfig": {shape: "homeassistant/<component>/<node_id>/<object_id>/config"},
	// The `<base-slug>_` scope this daemon prefixes its discovery node ids
	// with — part of DiscoveryConfig's `<node_id>` segment, never a topic.
	// It is exported for the retained-config sweeps, which have to recognise
	// the node ids DiscoveryConfig writes; the documented shape is unchanged
	// because the scope lives inside `<node_id>` rather than beside it.
	"DiscoveryNodeScope": {segmentOf: "DiscoveryConfig"},

	"AddonUpdateState":   {shape: "<name>/status/system/addon_update"},
	"AddonUpdateCommand": {shape: "<name>/set/system/addon_update"},

	"ParameterState":   {shape: "<name>/status/<central>/<iface>/<addr>/<ch>/values/<param>"},
	"ParameterCommand": {shape: "<name>/set/<central>/<iface>/<addr>/<ch>/values/<param>"},
	"ParameterConfig":  {shape: "<name>/meta/<central>/<iface>/<addr>/<ch>/values/<param>"},
	"DataPointState":   {alias: "ParameterState"},
	"DataPointCommand": {alias: "ParameterCommand"},
	"DataPointConfig":  {alias: "ParameterConfig"},

	"ChannelEvent":       {shape: "<name>/status/<central>/<iface>/<addr>/<ch>/event"},
	"ChannelImpulse":     {shape: "<name>/status/<central>/<iface>/<addr>/<ch>/impulse"},
	"ChannelDeviceError": {shape: "<name>/status/<central>/<iface>/<addr>/<ch>/device_error"},

	"SlotState":             {shape: "<name>/status/<central>/<iface>/<addr>/<ch>/custom/<kind>"},
	"SlotConfig":            {shape: "<name>/meta/<central>/<iface>/<addr>/<ch>/custom/<kind>"},
	"CustomDPServiceMethod": {shape: "<name>/set/<central>/<iface>/<addr>/<ch>/custom/<kind>/<method>"},
	"CustomDPInvoke":        {shape: "<name>/set/<central>/devices/<addr>/cdps/<cdp>/<op>"},

	"DeviceAvailability":  {shape: "<name>/status/<central>/<iface>/<addr>/online"},
	"DeviceInfo":          {shape: "<name>/status/<central>/<iface>/<addr>/info"},
	"DeviceDiagnostics":   {shape: "<name>/status/<central>/<iface>/<addr>/diagnostics"},
	"DeviceUpdateState":   {shape: "<name>/status/<central>/<iface>/<addr>/update"},
	"DeviceUpdateCommand": {shape: "<name>/set/<central>/<iface>/<addr>/update"},

	"WeekProfileState":      {shape: "<name>/status/<central>/<iface>/<addr>/<ch>/week_profile"},
	"WeekProfileCommand":    {shape: "<name>/set/<central>/<iface>/<addr>/<ch>/week_profile"},
	"CombinedState":         {shape: "<name>/status/<central>/<iface>/<addr>/<ch>/combined/<kind>"},
	"CombinedCommand":       {shape: "<name>/set/<central>/<iface>/<addr>/<ch>/combined/<kind>"},
	"ScheduleEntityState":   {shape: "<name>/status/<central>/<iface>/<addr>/<ch>/schedule/active_entries"},
	"ScheduleEntityAttrs":   {shape: "<name>/status/<central>/<iface>/<addr>/<ch>/schedule/attributes"},
	"ScheduleSwitchState":   {shape: "<name>/status/<central>/<iface>/<addr>/<ch>/schedule/switch/<key>"},
	"ScheduleSwitchCommand": {shape: "<name>/set/<central>/<iface>/<addr>/<ch>/schedule/switch/<key>"},

	"SystemStatus":         {shape: "<name>/status/<central>/system/status"},
	"HubStatus":            {shape: "<name>/status/<central>/online"},
	"HubInfo":              {shape: "<name>/status/<central>/hub/info"},
	"HubDiagnostics":       {shape: "<name>/status/<central>/hub/diagnostics"},
	"HubSystemHealthScore": {shape: "<name>/status/<central>/system/health_score"},
	"HubConnectionLatency": {shape: "<name>/status/<central>/system/latency"},
	"HubLastEventAge":      {shape: "<name>/status/<central>/system/last_event_age"},
	"HubUpdate":            {shape: "<name>/status/<central>/hub/update"},

	// --- internal/model/naming/pathdata.go ----------------------------
	// naming.StatusTopic / SetTopic / MetaTopic are deliberately absent: they
	// compose `<base>/<function>/<item…>` for any item and render no shape of
	// their own — every shape they help build is inventoried through the
	// producer that names its item path. The scan below enumerates only
	// MQTT*-prefixed names in pathdata.go, which is what keeps them out.
	"MQTTState":                 {delegateOf: "ParameterState"},
	"MQTTCommand":               {delegateOf: "ParameterCommand"},
	"MQTTConfig":                {delegateOf: "ParameterConfig"},
	"MQTTChannelEvent":          {delegateOf: "ChannelEvent"},
	"MQTTChannelImpulse":        {delegateOf: "ChannelImpulse"},
	"MQTTChannelDeviceError":    {delegateOf: "ChannelDeviceError"},
	"MQTTCustomDPState":         {delegateOf: "SlotState"},
	"MQTTCustomDPConfig":        {delegateOf: "SlotConfig"},
	"MQTTCustomDPServiceMethod": {delegateOf: "CustomDPServiceMethod"},
	"MQTTCustomDPInvoke":        {delegateOf: "CustomDPInvoke"},
	"MQTTDeviceAvailability":    {delegateOf: "DeviceAvailability"},
	"MQTTDeviceInfo":            {delegateOf: "DeviceInfo"},
	"MQTTDeviceDiagnostics":     {delegateOf: "DeviceDiagnostics"},
	"MQTTDeviceUpdateState":     {delegateOf: "DeviceUpdateState"},
	"MQTTDeviceUpdateCommand":   {delegateOf: "DeviceUpdateCommand"},
	"MQTTWeekProfileState":      {delegateOf: "WeekProfileState"},
	"MQTTWeekProfileCommand":    {delegateOf: "WeekProfileCommand"},
	"MQTTSystemStatus":          {delegateOf: "SystemStatus"},
	"MQTTHubStatus":             {delegateOf: "HubStatus"},
	"MQTTHubInfo":               {delegateOf: "HubInfo"},
	"MQTTHubDiagnostics":        {delegateOf: "HubDiagnostics"},
	"MQTTHubUpdate":             {delegateOf: "HubUpdate"},

	// No TopicBuilder wrapper — the document cites these free functions
	// directly, so they carry their own shape.
	"MQTTHubSysvarState":                {shape: "<name>/status/<central>/hub/sysvars/<sysvar>"},
	"MQTTHubSysvarCommand":              {shape: "<name>/set/<central>/hub/sysvars/<sysvar>"},
	"MQTTHubProgramState":               {shape: "<name>/status/<central>/hub/programs/<id>/active"},
	"MQTTHubProgramSet":                 {shape: "<name>/set/<central>/hub/programs/<id>/active"},
	"MQTTHubProgramTrigger":             {shape: "<name>/set/<central>/hub/programs/<id>/trigger"},
	"MQTTHubProgramExecuteAvailability": {shape: "<name>/status/<central>/hub/programs/<id>/execute_available"},
	"MQTTHubConnectivity":               {shape: "<name>/status/<central>/hub/connectivity/<iface>"},
	"MQTTHubInstallModeForInterface":    {shape: "<name>/status/<central>/hub/install_mode/<iface>"},
	"MQTTHubInstallModeCommand":         {shape: "<name>/set/<central>/hub/install_mode/<iface>"},
	"MQTTHubAlarmMessages":              {shape: "<name>/status/<central>/hub/alarm_messages"},
	"MQTTHubServiceMessages":            {shape: "<name>/status/<central>/hub/service_messages"},
	"MQTTHubInbox":                      {shape: "<name>/status/<central>/hub/inbox"},
}

// mqttTopicProducerNames parses the two builder files and returns the name
// of every topic-shape producer they declare: exported methods on
// *TopicBuilder in topics.go, and exported MQTT*-prefixed functions and
// methods in pathdata.go.
//
// Both rules are syntactic, which is the point — a name-based scan is what
// keeps the inventory from being a list someone has to remember to extend.
func mqttTopicProducerNames(t *testing.T, root string) map[string]string {
	t.Helper()

	out := make(map[string]string)
	fset := token.NewFileSet()
	for _, rel := range mqttBuilderFiles {
		file, err := parser.ParseFile(fset, filepath.Join(root, filepath.FromSlash(rel)), nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", rel, err)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || !fn.Name.IsExported() {
				continue
			}
			name := fn.Name.Name
			switch {
			case strings.HasSuffix(rel, "topics.go"):
				if fn.Recv == nil {
					continue // NewTopicBuilder and friends build no topic
				}
			case strings.HasSuffix(rel, "pathdata.go"):
				if !strings.HasPrefix(name, "MQTT") {
					continue
				}
			}
			out[name] = rel
		}
	}
	return out
}

// TestMQTTTopicProducersAreDocumented is the code -> doc direction: it fails
// when the daemon can put a shape on the wire that
// docs/mqtt-topic-schema.md does not describe.
//
// Three failures, and each names the missing move:
//
//   - a producer the scan finds and [mqttTopicProducers] does not classify —
//     a new builder landed without a documented row;
//   - an inventory entry no producer backs — a builder was renamed or
//     deleted and the document still promises its shape;
//   - an inventoried shape with no table row in the document — the row was
//     dropped, or the shape was changed without the document following.
//
// What it deliberately does NOT do is count call sites. That is
// [TestMQTTDocumentedTopicsHaveAProducer]'s job, and the two rules must not
// be merged: a naive "every builder must have a caller" is wrong, because
// the daemon consumes commands through `+` wildcards —
// TopicBuilder.ParameterCommand has zero production callers while both its
// documented `/set` shapes are honoured. Classify, do not count.
func TestMQTTTopicProducersAreDocumented(t *testing.T) {
	t.Parallel()

	const root = "../.."
	found := mqttTopicProducerNames(t, root)

	names := make([]string, 0, len(found))
	for name := range found {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if _, ok := mqttTopicProducers[name]; !ok {
			t.Errorf("%s (%s) builds a topic shape and mqttTopicProducers does not classify it.\n"+
				"  Add a row for its shape to docs/mqtt-topic-schema.md and an entry here — or, if it "+
				"renders a shape another producer already documents, record that with delegateOf/alias.\n"+
				"  A shape the daemon can put on the wire with no documented row is what this test exists "+
				"to stop: the document was a strict subset of the wire for eight topic families before it.",
				name, found[name])
		}
	}

	inventoried := make([]string, 0, len(mqttTopicProducers))
	for name := range mqttTopicProducers {
		inventoried = append(inventoried, name)
	}
	sort.Strings(inventoried)

	documented := make(map[string]bool)
	for _, topic := range mqttDocTableTopics(t, root) {
		documented[topic] = true
	}

	for _, name := range inventoried {
		p := mqttTopicProducers[name]
		if _, ok := found[name]; !ok {
			t.Errorf("mqttTopicProducers classifies %q, which neither %v declares any more — "+
				"drop the entry and the document's row, or restore the producer.", name, mqttBuilderFiles)
			continue
		}
		switch {
		case p.shape != "":
			if !documented[p.shape] {
				t.Errorf("%s renders %q and no table row of docs/mqtt-topic-schema.md carries that "+
					"pattern.\n  Add the row (and classify it in mqttDocTopicPromises), or correct the "+
					"shape here if the topic changed.", name, p.shape)
			}
		case p.delegateOf != "":
			target, ok := mqttTopicProducers[p.delegateOf]
			if !ok || target.shape == "" {
				t.Errorf("%s delegates to %q, which is not an inventoried producer with a shape of "+
					"its own.", name, p.delegateOf)
			}
		case p.alias != "":
			target, ok := mqttTopicProducers[p.alias]
			if !ok || target.shape == "" {
				t.Errorf("%s aliases %q, which is not an inventoried producer with a shape of its own.",
					name, p.alias)
			}
		case p.segmentOf != "":
			target, ok := mqttTopicProducers[p.segmentOf]
			if !ok || target.shape == "" {
				t.Errorf("%s is a segment of %q, which is not an inventoried producer with a shape of "+
					"its own.", name, p.segmentOf)
				continue
			}
			// The fragment must not also be claiming a row of its own: a
			// producer that renders a whole topic belongs in `shape`, where
			// the document is checked, not in the class that excuses it from
			// having one. The behavioural half of the claim — that what it
			// returns cannot be published — is pinned by
			// [TestMQTTTopicSchemaDoc_DiscoveryNodeScopeIsNotATopic].
			if p.shape != "" || p.delegateOf != "" || p.alias != "" {
				t.Errorf("%s is classified as a segment of %q and also carries a shape, delegateOf or "+
					"alias — exactly one of the four may be set.", name, p.segmentOf)
			}
		default:
			t.Errorf("%s carries no shape, delegateOf, alias or segmentOf — say what it contributes to "+
				"docs/mqtt-topic-schema.md.", name)
		}
	}
}
