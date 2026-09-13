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
	// --- State topics ---------------------------------------------------
	"<base>/<central>/<iface>/<addr>/<ch>/values/<param>": {
		kind: promisePublished, producers: []string{"ParameterState", "SlotState"},
	},
	"<base>/<central>/<iface>/<addr>/<ch>/master/<param>": {
		kind: promisePublished, producers: []string{"ParameterState", "SlotState"},
	},
	"<base>/<central>/<iface>/<addr>/<ch>/calculated/<param>": {
		kind: promisePublished, producers: []string{"ParameterState", "SlotState"},
	},
	"<base>/<central>/<iface>/<addr>/<ch>/custom/<kind>": {
		kind: promisePublished, producers: []string{"SlotState"},
	},
	"<base>/<central>/<iface>/<addr>/<ch>/event": {
		kind: promisePublished, producers: []string{"ChannelEvent"},
	},
	"<base>/<central>/<iface>/<addr>/<ch>/impulse": {
		kind: promisePublished, producers: []string{"ChannelImpulse"},
	},
	"<base>/<central>/<iface>/<addr>/<ch>/device_error": {
		kind: promisePublished, producers: []string{"ChannelDeviceError"},
	},
	"<base>/<central>/<iface>/<addr>/availability": {
		kind: promisePublished, producers: []string{"DeviceAvailability"},
	},
	"<base>/<central>/<iface>/<addr>/info": {
		kind: promisePublished, producers: []string{"DeviceInfo"},
	},
	"<base>/<central>/<iface>/<addr>/diagnostics": {
		kind: promisePublished, producers: []string{"DeviceDiagnostics"},
	},
	"<base>/<central>/<iface>/<addr>/update": {
		kind: promisePublished, producers: []string{"DeviceUpdateState"},
	},
	"<base>/<central>/<iface>/<addr>/<ch>/values/<param>/config": {
		kind: promisePublished, producers: []string{"ParameterConfig", "SlotConfig"},
	},
	"<base>/<central>/<iface>/<addr>/<ch>/custom/<kind>/config": {
		kind: promisePublished, producers: []string{"SlotConfig"},
	},
	"<base>/<central>/<iface>/<addr>/<ch>/event/<type>": {
		kind: promisePublished, producers: []string{"DataPointEvent"},
	},
	"<base>/<central>/<iface>/<addr>/<ch>/combined/<kind>": {
		kind: promisePublished, producers: []string{"CombinedState"},
	},
	"<base>/<central>/<iface>/<addr>/<ch>/week_profile/state": {
		kind: promisePublished, producers: []string{"WeekProfileState"},
	},
	"<base>/<central>/<iface>/<addr>/<ch>/schedule/state": {
		kind: promisePublished, producers: []string{"ScheduleEntityState"},
	},
	"<base>/<central>/<iface>/<addr>/<ch>/schedule/attrs": {
		kind: promisePublished, producers: []string{"ScheduleEntityAttrs"},
	},
	"<base>/<central>/<iface>/<addr>/<ch>/schedule/<key>/state": {
		kind: promisePublished, producers: []string{"ScheduleSwitchState"},
	},
	"<base>/alarm/<zone>/state": {
		kind: promisePublished, producers: []string{"alarmStateTopic"},
	},
	"<base>/alarm/<zone>/availability": {
		kind: promisePublished, producers: []string{"alarmAvailabilityTopic"},
	},
	"<base>/alarm/<zone>/event": {
		kind: promisePublished, producers: []string{"alarmEventTopic"},
	},

	// --- Command (set) topics -------------------------------------------
	// The daemon subscribes to these with wildcard filters, so no builder
	// call site exists per topic — TopicBuilder.ParameterCommand has no
	// production caller at all and the shape is still honoured.
	"<base>/<central>/<iface>/<addr>/<ch>/values/<param>/set": {
		kind: promiseCommand,
		why:  "matched by the daemon's `<base>/+/+/+/+/+/set` wildcard filter",
	},
	"<base>/<central>/<iface>/<addr>/<ch>/master/<param>/set": {
		kind: promiseCommand,
		why:  "matched by the same wildcard filter as the VALUES form",
	},
	"<base>/<central>/<iface>/<addr>/<ch>/custom/<kind>/set/<method>": {
		kind: promiseCommand, producers: []string{"CustomDPServiceMethod"},
		why: "declared as command_topic in discovery, consumed by a wildcard filter",
	},
	"<base>/<central>/<iface>/<addr>/<ch>/combined/<kind>/set": {
		kind: promiseCommand, producers: []string{"CombinedCommand"},
		why: "declared as command_topic in combined-DP discovery; consumed by the " +
			"8-segment `<base>/+/+/+/+/+/+/set` filter, which the combined kind sits in",
	},
	"<base>/<central>/<iface>/<addr>/<ch>/week_profile/set": {
		kind: promiseCommand, producers: []string{"WeekProfileCommand"},
		why: "declared as command_topic in week-profile discovery; consumed by the " +
			"7-segment `<base>/+/+/+/+/+/set` filter, which it shares with the legacy bucket-less shape",
	},
	"<base>/<central>/<iface>/<addr>/<ch>/schedule/<key>/set": {
		kind: promiseCommand, producers: []string{"ScheduleSwitchCommand"},
		why: "declared as command_topic in schedule-switch discovery, consumed by the 8-segment filter",
	},
	"<base>/<central>/devices/<addr>/cdps/<name>/<op>/invoke": {
		kind: promiseCommand, producers: []string{"CustomDPInvoke", "MQTTCustomDPInvoke"},
		why: "consumed by the `<base>/+/devices/+/cdps/+/+/invoke` filter. Like " +
			"ParameterCommand the builder has zero production callers and the shape is honoured",
	},
	"<base>/<central>/hub/install_mode/<iface>/set": {
		kind: promiseCommand, producers: []string{"MQTTHubInstallModeCommand"},
		why: "declared as command_topic in install-mode discovery, consumed by a wildcard filter",
	},
	"<base>/system/addon_update/set": {
		kind: promiseCommand, producers: []string{"AddonUpdateCommand"},
		why: "declared as command_topic in add-on update discovery; consumed by the " +
			"one literal (non-wildcard) route the subscriber registers",
	},
	"<base>/alarm/<zone>/set": {
		kind: promiseCommand, producers: []string{"alarmCommandTopic"},
		why: "declared as command_topic in alarm discovery, consumed by a wildcard filter",
	},
	"<base>/<central>/hub/sysvars/<name>/set": {
		kind: promiseCommand, producers: []string{"MQTTHubSysvarCommand"},
		why: "declared as command_topic in hub discovery, consumed by a wildcard filter",
	},
	"<base>/<central>/hub/programs/<id>/set": {
		kind: promiseCommand, producers: []string{"MQTTHubProgramSet"},
		why: "declared as command_topic in hub discovery, consumed by a wildcard filter",
	},
	"<base>/<central>/hub/programs/<id>/trigger": {
		kind: promiseCommand, producers: []string{"MQTTHubProgramTrigger"},
		why: "declared as command_topic in hub discovery, consumed by a wildcard filter",
	},

	// --- HA Discovery ---------------------------------------------------
	"homeassistant/<component>/<node_id>/<object_id>/config": {
		kind: promisePublished, producers: []string{"DiscoveryConfig"},
	},

	// --- Bridge / hub status --------------------------------------------
	"<base>/bridge/status": {
		kind: promisePublished, producers: []string{"BridgeStatus"},
	},
	"<base>/bridge/health": {
		kind: promisePublished, producers: []string{"BridgeHealth"},
	},
	"<base>/<central>/hub/sysvars/<name>/state": {
		kind: promisePublished, producers: []string{"MQTTHubSysvarState"},
	},
	"<base>/<central>/hub/programs/<id>/state": {
		kind: promisePublished, producers: []string{"MQTTHubProgramState"},
	},
	"<base>/<central>/hub/programs/<id>/execute_available": {
		kind: promisePublished, producers: []string{"MQTTHubProgramExecuteAvailability"},
	},
	"<base>/<central>/hub/connectivity/<iface>": {
		kind: promisePublished, producers: []string{"MQTTHubConnectivity"},
	},
	"<base>/<central>/system/status": {
		kind: promisePublished, producers: []string{"SystemStatus"},
	},
	"<base>/<central>/hub/install_mode/<iface>": {
		kind: promisePublished, producers: []string{"MQTTHubInstallModeForInterface"},
	},
	"<base>/<central>/hub/update": {
		kind: promisePublished, producers: []string{"HubUpdate"},
	},
	"<base>/<central>/hub/alarm_messages": {
		kind: promisePublished, producers: []string{"MQTTHubAlarmMessages"},
	},
	"<base>/<central>/hub/service_messages": {
		kind: promisePublished, producers: []string{"MQTTHubServiceMessages"},
	},
	"<base>/<central>/hub/inbox": {
		kind: promisePublished, producers: []string{"MQTTHubInbox"},
	},
	"<base>/<central>/system/health_score": {
		kind: promisePublished, producers: []string{"HubSystemHealthScore"},
	},
	"<base>/<central>/system/latency": {
		kind: promisePublished, producers: []string{"HubConnectionLatency"},
	},
	"<base>/<central>/system/last_event_age": {
		kind: promisePublished, producers: []string{"HubLastEventAge"},
	},
	"<base>/system/addon_update/state": {
		kind: promisePublished, producers: []string{"AddonUpdateState"},
	},
	// Promoted from reserved to published on 2026-09-13: the per-CCU
	// availability gate the 2026-09-12 amendment named as the real gap
	// behind this shape. Both halves of the promotion are here — the row
	// moved AND HubStatus gained production call sites (the availability
	// list every CCU-scoped hub entity declares, and the publish path).
	"<base>/<central>/hub/status": {
		kind: promisePublished, producers: []string{"HubStatus"},
	},

	// --- Reserved `hub/` shapes — nothing publishes here ------------------
	// Documented as reserved after the 2026-09-12 amendment to ADR 0011.
	// Promoting one of these to a published class means giving it a
	// publisher AND moving its row into the table above; flipping the row
	// alone, or adding the publisher alone, fails this test.
	"<base>/<central>/hub/info": {
		kind: promiseReserved, producers: []string{"HubInfo", "MQTTHubInfo"},
		why: "its fields are in the HA discovery device block instead",
	},
	"<base>/<central>/hub/diagnostics": {
		kind: promiseReserved, producers: []string{"HubDiagnostics", "MQTTHubDiagnostics"},
		why: "per-device data points and the system/* metric topics carry this",
	},

	// --- Unwired command spelling ---------------------------------------
	"<base>/<central>/<iface>/<addr>/update/set": {
		kind: promiseUnwired, producers: []string{"DeviceUpdateCommand", "MQTTDeviceUpdateCommand"},
		why: "the update entity's topic layout answers with it, but declares no command_topic, " +
			"and no subscription filter has this shape — flashing firmware from a possibly " +
			"retained broker payload is unsafe",
	},

	// --- Security & Safety plane ----------------------------------------
	"<base>/security/state":   {kind: promisePublished, producers: []string{"securityStateTopic"}},
	"<base>/security/alarm":   {kind: promisePublished, producers: []string{"securityStateTopic"}},
	"<base>/security/problem": {kind: promisePublished, producers: []string{"securityStateTopic"}},
	"<base>/security/health":  {kind: promisePublished, producers: []string{"securityStateTopic"}},
	"<base>/security/class/<class>": {
		kind: promisePublished, producers: []string{"securityClassTopic"},
	},
	"<base>/security/zone/<slug>": {
		kind: promisePublished, producers: []string{"securityZoneTopic"},
	},
	"<base>/security/last_alarm": {kind: promisePublished, producers: []string{"securityStateTopic"}},
	"<base>/security/last_fault": {kind: promisePublished, producers: []string{"securityStateTopic"}},
	"<base>/security/event":      {kind: promisePublished, producers: []string{"securityStateTopic"}},
	"<base>/security/fault":      {kind: promisePublished, producers: []string{"securityStateTopic"}},
	"<base>/security/availability": {
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
// markdown table row — the `<base>/…` or `homeassistant/…` shape.
var docTopicPatternRe = regexp.MustCompile("`((?:<base>|homeassistant)/[^`]*)`")

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
// calls satisfies perfectly: `<base>/<central>/hub/status` was promised
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
type topicProducer struct {
	shape      string
	delegateOf string
	alias      string
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
	"BridgeStatus":    {shape: "<base>/bridge/status"},
	"BridgeHealth":    {shape: "<base>/bridge/health"},
	"DiscoveryConfig": {shape: "homeassistant/<component>/<node_id>/<object_id>/config"},

	"AddonUpdateState":   {shape: "<base>/system/addon_update/state"},
	"AddonUpdateCommand": {shape: "<base>/system/addon_update/set"},

	"ParameterState":   {shape: "<base>/<central>/<iface>/<addr>/<ch>/values/<param>"},
	"ParameterCommand": {shape: "<base>/<central>/<iface>/<addr>/<ch>/values/<param>/set"},
	"ParameterConfig":  {shape: "<base>/<central>/<iface>/<addr>/<ch>/values/<param>/config"},
	"DataPointState":   {alias: "ParameterState"},
	"DataPointCommand": {alias: "ParameterCommand"},
	"DataPointConfig":  {alias: "ParameterConfig"},

	"DataPointEvent":     {shape: "<base>/<central>/<iface>/<addr>/<ch>/event/<type>"},
	"ChannelEvent":       {shape: "<base>/<central>/<iface>/<addr>/<ch>/event"},
	"ChannelImpulse":     {shape: "<base>/<central>/<iface>/<addr>/<ch>/impulse"},
	"ChannelDeviceError": {shape: "<base>/<central>/<iface>/<addr>/<ch>/device_error"},

	"SlotState":             {shape: "<base>/<central>/<iface>/<addr>/<ch>/custom/<kind>"},
	"SlotConfig":            {shape: "<base>/<central>/<iface>/<addr>/<ch>/custom/<kind>/config"},
	"CustomDPServiceMethod": {shape: "<base>/<central>/<iface>/<addr>/<ch>/custom/<kind>/set/<method>"},
	"CustomDPInvoke":        {shape: "<base>/<central>/devices/<addr>/cdps/<name>/<op>/invoke"},

	"DeviceAvailability":  {shape: "<base>/<central>/<iface>/<addr>/availability"},
	"DeviceInfo":          {shape: "<base>/<central>/<iface>/<addr>/info"},
	"DeviceDiagnostics":   {shape: "<base>/<central>/<iface>/<addr>/diagnostics"},
	"DeviceUpdateState":   {shape: "<base>/<central>/<iface>/<addr>/update"},
	"DeviceUpdateCommand": {shape: "<base>/<central>/<iface>/<addr>/update/set"},

	"WeekProfileState":      {shape: "<base>/<central>/<iface>/<addr>/<ch>/week_profile/state"},
	"WeekProfileCommand":    {shape: "<base>/<central>/<iface>/<addr>/<ch>/week_profile/set"},
	"CombinedState":         {shape: "<base>/<central>/<iface>/<addr>/<ch>/combined/<kind>"},
	"CombinedCommand":       {shape: "<base>/<central>/<iface>/<addr>/<ch>/combined/<kind>/set"},
	"ScheduleEntityState":   {shape: "<base>/<central>/<iface>/<addr>/<ch>/schedule/state"},
	"ScheduleEntityAttrs":   {shape: "<base>/<central>/<iface>/<addr>/<ch>/schedule/attrs"},
	"ScheduleSwitchState":   {shape: "<base>/<central>/<iface>/<addr>/<ch>/schedule/<key>/state"},
	"ScheduleSwitchCommand": {shape: "<base>/<central>/<iface>/<addr>/<ch>/schedule/<key>/set"},

	"SystemStatus":         {shape: "<base>/<central>/system/status"},
	"HubStatus":            {shape: "<base>/<central>/hub/status"},
	"HubInfo":              {shape: "<base>/<central>/hub/info"},
	"HubDiagnostics":       {shape: "<base>/<central>/hub/diagnostics"},
	"HubSystemHealthScore": {shape: "<base>/<central>/system/health_score"},
	"HubConnectionLatency": {shape: "<base>/<central>/system/latency"},
	"HubLastEventAge":      {shape: "<base>/<central>/system/last_event_age"},
	"HubUpdate":            {shape: "<base>/<central>/hub/update"},

	// --- internal/model/naming/pathdata.go ----------------------------
	"MQTTState":                 {delegateOf: "ParameterState"},
	"MQTTCommand":               {delegateOf: "ParameterCommand"},
	"MQTTConfig":                {delegateOf: "ParameterConfig"},
	"MQTTDataPointEvent":        {delegateOf: "DataPointEvent"},
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
	"MQTTHubSysvarState":                {shape: "<base>/<central>/hub/sysvars/<name>/state"},
	"MQTTHubSysvarCommand":              {shape: "<base>/<central>/hub/sysvars/<name>/set"},
	"MQTTHubProgramState":               {shape: "<base>/<central>/hub/programs/<id>/state"},
	"MQTTHubProgramSet":                 {shape: "<base>/<central>/hub/programs/<id>/set"},
	"MQTTHubProgramTrigger":             {shape: "<base>/<central>/hub/programs/<id>/trigger"},
	"MQTTHubProgramExecuteAvailability": {shape: "<base>/<central>/hub/programs/<id>/execute_available"},
	"MQTTHubConnectivity":               {shape: "<base>/<central>/hub/connectivity/<iface>"},
	"MQTTHubInstallModeForInterface":    {shape: "<base>/<central>/hub/install_mode/<iface>"},
	"MQTTHubInstallModeCommand":         {shape: "<base>/<central>/hub/install_mode/<iface>/set"},
	"MQTTHubAlarmMessages":              {shape: "<base>/<central>/hub/alarm_messages"},
	"MQTTHubServiceMessages":            {shape: "<base>/<central>/hub/service_messages"},
	"MQTTHubInbox":                      {shape: "<base>/<central>/hub/inbox"},
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
		default:
			t.Errorf("%s carries no shape, delegateOf or alias — say what it contributes to "+
				"docs/mqtt-topic-schema.md.", name)
		}
	}
}
