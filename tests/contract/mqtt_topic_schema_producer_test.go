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

// mqttBuilderOnlyFiles are the files whose call sites do NOT count as a
// production producer: they define the builders and delegate among
// themselves, so a builder called only from here is called only by
// itself. This is the exact hole the doctest fell into —
// naming.MQTTHubStatus had one non-test caller, TopicBuilder.HubStatus,
// which had none.
var mqttBuilderOnlyFiles = []string{
	"internal/north/mqtt/topics.go",
	"internal/model/naming/pathdata.go",
}

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
			for _, skip := range mqttBuilderOnlyFiles {
				if rel == skip {
					return nil
				}
			}

			file := parseGoFileOrRecord(fset, path, rel, &unparsed)
			if file == nil {
				return nil
			}
			ast.Inspect(file, func(n ast.Node) bool {
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
					"from a non-test file outside %v.\n"+
					"  Either the publisher is missing (then the daemon does not keep this promise — "+
					"implement it, or move the row to the reserved table and amend ADR 0011),\n"+
					"  or the producer was renamed (then update mqttDocTopicPromises).",
					topic, promise.producers, mqttBuilderOnlyFiles)
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
		case promiseCommand:
			if promise.why == "" {
				t.Errorf("command topic %q carries no recorded reason; say how the daemon consumes it", topic)
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
