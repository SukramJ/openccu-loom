// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/internal/north/mqtt"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

var updatePerDPStateGolden = flag.Bool("update-per-dp-state-golden", false,
	"rewrite the pinned per-datapoint state payloads")

var perDPStateGoldenPath = filepath.Join("testdata", "state_golden_per_dp.json")

// perDPGoldenEntry is one pinned state message: the topic and the payload,
// together.
//
// The topic is pinned alongside the body for the same reason the discovery
// goldens do it — a changed topic is invisible in the body, and a state
// message on a moved topic is not a degraded entity but an entity that never
// updates again, with nothing in any log. Here it also pins the bucket
// decision: [slotBucket] chooses between `values`, `master` and `calculated`,
// and that choice appears only in the topic.
//
// The payload is stored DECODED so a reviewer can read the diff; comparison
// is on the canonical re-encoding of both sides, exact for every key and
// value, with only whitespace — which no consumer sees — out of scope.
type perDPGoldenEntry struct {
	Topic   string         `json:"topic"`
	Payload map[string]any `json:"payload"`
}

// perDPCanonical is the form both sides of the comparison are reduced to.
func perDPCanonical(t *testing.T, body map[string]any) string {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(raw)
}

// The two instants every fixture is stamped with.
//
// This is the answer to the wall-clock problem, and it is worth stating
// explicitly because the alternative was tempting and wrong.
// [payload.PerDPState] carries two epoch-second fields, `modified_at` and
// `refreshed_at`, and a payload carrying a wall clock cannot be byte-pinned.
// The clock is therefore INJECTED rather than normalised: both fields are
// populated from the EVENT's timestamp (`hmevent.Base`, set through
// [hmevent.NewBaseAt]), never from `time.Now()`, so a fixture can choose the
// instant and the pinned bytes are exact — no field is blanked, no field is
// excluded from the comparison, and a regression that started reading the
// wall clock on this path would fail the pin rather than be normalised away.
//
// The values are deliberately not round: 1767225600.5 exercises the
// float64-seconds encoding (a sub-second fraction, which an integer-seconds
// regression would silently truncate) and is far enough from zero that the
// `omitempty` elision cannot be confused with a real value.
//
// [goldenRestampAt] is chosen so the pinned bytes also show a wart worth
// seeing: .25 s past a non-round second renders as `1767225723.2499998`,
// because [payload.EpochSeconds] divides `UnixNano()` by 1e9 in float64 and
// the quotient is not exactly representable. Every timestamp on this plane is
// approximate at roughly the 100 ns level. That is harmless for the field's
// documented purpose and it is pinned rather than smoothed, because a
// migration to an integer-milliseconds encoding would be a payload break and
// this row is where it would show up.
var (
	goldenObservedAt = time.Unix(1767225600, 500_000_000).UTC()
	goldenRestampAt  = time.Unix(1767225723, 250_000_000).UTC()
)

// perDPGoldenFixture is the harness every row is produced through: a real
// [central.Registry] with a real [device.Device], a real [mqtt.Bridge] over a
// recording client, and a real [EventBridge]. Nothing about the payload is
// restated — every pinned byte comes out of [EventBridge.publishSlotState],
// which is the production assembly site, and every pinned topic comes out of
// [mqtt.Bridge.PublishSlotState].
type perDPGoldenFixture struct {
	dev    *device.Device
	client *mqtt.NoopClient
	eb     *EventBridge
}

func newPerDPGoldenFixture(t *testing.T) *perDPGoldenFixture {
	t.Helper()
	reg, dev := registryWithDevice(t)
	client := mqtt.NewNoopClient()
	bridge := mqtt.NewBridge(mqtt.BridgeConfig{
		Base:        "openccu-loom",
		CentralName: "ccu-01",
		RawEnabled:  true,
	}, client)
	return &perDPGoldenFixture{
		dev:    dev,
		client: client,
		eb:     NewEventBridge(reg, nil, mqtt.NewWiring(bridge, nil)),
	}
}

// publish drives one value-changed event through the real slot-state path and
// returns the single message it produced.
func (f *perDPGoldenFixture) publish(
	t *testing.T, name string, at time.Time, key hmtypes.DataPointKey, value any, ch *device.Channel,
) perDPGoldenEntry {
	t.Helper()
	before := len(f.client.Published())
	pv, err := hmtypes.NewParamValue(value)
	if err != nil {
		t.Fatalf("%s: NewParamValue(%v): %v", name, value, err)
	}
	ev := hmevent.DataPointValueChangedEvent{
		Base:     hmevent.NewBaseAt(at),
		Key:      key,
		NewValue: pv,
	}
	f.eb.publishSlotState(context.Background(), "ccu-01", "HmIP-RF", "0001ABCD", 1, ev, ch)

	// publishSlotState writes the state topic and, when the data point
	// resolves to a [payload.Source], the retained `/config` companion
	// beside it. Only the state message is pinned here (see the scope note
	// on TestPerDPStatePayloadsArePinned), so it is selected by excluding the
	// `/config` suffix rather than by position — a positional pick would
	// silently follow the config write if the two ever swapped order.
	//
	// Note the state topic carries NO trailing `/state` segment, contrary to
	// [payload.PerDPState]'s own doc comment ("`values/<param>/state`"). The
	// golden pins what the code does.
	var picked []mqtt.Publication
	for _, p := range f.client.Published()[before:] {
		if !strings.HasSuffix(p.Topic, "/config") {
			picked = append(picked, p)
		}
	}
	if len(picked) != 1 {
		t.Fatalf("%s: publishSlotState produced %d state messages, want exactly 1 — "+
			"the fixture no longer reaches the slot-state path", name, len(picked))
	}
	var body map[string]any
	if err := json.Unmarshal(picked[0].Payload, &body); err != nil {
		t.Fatalf("%s: state payload is not JSON: %v", name, err)
	}
	return perDPGoldenEntry{Topic: picked[0].Topic, Payload: body}
}

// valuesKey / masterKey build the DataPointKey the two non-calculated buckets
// are selected by. [slotBucket] reads ParamsetKey only, so the bucket segment
// in the pinned topic is a function of this and nothing else.
func valuesKey(param string) hmtypes.DataPointKey {
	return hmtypes.DataPointKey{
		InterfaceID:    "HmIP-RF",
		ChannelAddress: "0001ABCD:1",
		ParamsetKey:    hmenum.ParamsetKeyValues,
		Parameter:      param,
	}
}

func masterKey(param string) hmtypes.DataPointKey {
	k := valuesKey(param)
	k.ParamsetKey = hmenum.ParamsetKeyMaster
	return k
}

// putSwitchDP attaches a real [generic.Switch] to the channel's VALUES
// paramset and, when wire is non-nil, feeds it through the production wire
// ingest [generic.DataPoint.OnWireValue] so the data point is OBSERVED.
//
// Feeding the value matters: the availability gate mirrors the reference
// is_valid rule (refreshed + acceptable STATUS + value type + range), so an
// attached-but-never-observed data point reports `available: false`. A
// fixture that only attached the DP would pin every row as unavailable and
// the gate would be untestable in both directions.
func putSwitchDP(ch *device.Channel, param string, wire any) {
	dp := generic.NewSwitch(generic.Spec{
		Key: valuesKey(param),
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeBool,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite,
		},
		CentralName: "ccu-01",
	})
	if wire != nil {
		dp.OnWireValue(wire)
	}
	ch.Put(dp)
}

// putEnumDP attaches a real [generic.Select] carrying an ENUM descriptor with
// a VALUE_LIST, which is what the label coercion reads.
func putEnumDP(ch *device.Channel, param string, valueList []string, wire any) {
	dp := generic.NewSelect(generic.Spec{
		Key: valuesKey(param),
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeEnum,
			Operations: hmenum.OperationsRead,
			ValueList:  valueList,
		},
		CentralName: "ccu-01",
	})
	if wire != nil {
		dp.OnWireValue(wire)
	}
	ch.Put(dp)
}

// TestPerDPStatePayloadsArePinned pins the topic and the payload of every
// shape the per-datapoint state plane puts on the wire.
//
// This is the plane the message volume is on — one retained topic per
// datapoint per channel per device, roughly ten thousand of them on a real
// CCU — and before this test **no state message in this repository was
// byte-pinned anywhere that runs in `make test`**. The only state-payload
// coverage was key PRESENCE (`payload_format_test.go` and
// `internal/payload/wrapper_test.go` assert that `value` and `available`
// exist) plus the nine exact float cases `TestRenderValueFloatPrecision`
// added. Adding, removing or reordering a field of [payload.PerDPState], or
// changing any non-float value encoding, passed the whole suite. The two
// tests that would have noticed —
// `tests/integration/mqtt_roundtrip_test.go` and
// `tests/e2e/mqtt_binary_sensor_payload_test.go` — are behind
// `//go:build integration` / `e2e` and do not run in `make test`.
//
// That matters now specifically because the shared library's
// `publisher.StatePublisher` is the next thing this plane is proposed to move
// onto, and the library's own `publisher.Envelope` is two keys — `value` and
// `available`. Moving this plane onto it would silently drop `modified_at`,
// `refreshed_at` and `additional_information` from every datapoint payload.
// This pin is what turns that from a quiet regression into a failing test.
//
// Why the payload is byte-stable. See the comment on [goldenObservedAt]: the
// clock is injected through the event, not read at publish time, so nothing
// is normalised and nothing is excluded from the comparison. That is only
// possible because [EventBridge.publishSlotState] populates both timestamps
// from `e.Timestamp()`. The sibling legacy-alias mirror does read
// `time.Now()` at publish time and therefore cannot be pinned this way; it is
// out of scope here and named in the scope note below.
//
// Pinned rows that are defects:
//
//   - **F1**, row `values/f1-unchanged-value-restamps-modified-at`.
//     [payload.PerDPState.ModifiedAt]'s doc comment reads "Updated only when
//     the new value differs from the previous one".
//     [EventBridge.publishSlotState] assigns it the SAME epoch as
//     `RefreshedAt` unconditionally, with no last-value comparison anywhere
//     on the path. The pinned row is the second of two emissions of an
//     UNCHANGED value, and its `modified_at` has advanced — so a datapoint
//     re-reporting the same reading advertises a new modification time on
//     every poll, and every consumer reading `modified_at` to mean "last
//     change" is wrong. Pinned, not fixed: the fix is a behaviour change on
//     every entity and belongs in its own change, where this row moving is
//     the visible proof it worked.
//   - **F1**, second consequence, pinned by the same row: this is also the
//     direct reason a byte-level dedup gate is impossible on this plane. A
//     gate would be possible if the doc comment were true.
//   - Row `values/no-channel-object` pins that an unresolvable data point
//     reports `available: true`. That is deliberate (an unclassifiable entry
//     must not be greyed out) but it is indistinguishable on the wire from a
//     confirmed reading, which is why it is pinned rather than left to
//     inference.
//
// Scope. This pins what CI can reproduce: one row per way the single
// [payload.PerDPState] shape resolves differently — the three buckets, the
// value encodings, the availability gate, the `omitempty` elisions, the
// enum-label coercion and the additional-information block. It is NOT a
// fleet: a real CCU's datapoint catalogue lives on a CCU and a pin that
// looked complete would be worse than one that admits it is not. It does not
// pin the custom-DP (`custom/<kind>`) plane, whose payload is a caller-built
// `pload.StatePayload` map with no shared schema to pin; it does not pin the
// retained `/config` companion, which is already byte-gated by hand against
// `Bridge.configCache`; and it does not pin the legacy-alias mirror, which
// reads the wall clock at publish time, is reachable only through a config
// field no operator can set, and is scheduled for deletion.
//
// A diff here is a regression until someone shows otherwise. Refresh with:
//
//	go test ./internal/central/adapter/ -run TestPerDPStatePayloadsArePinned -update-per-dp-state-golden
func TestPerDPStatePayloadsArePinned(t *testing.T) {
	got := map[string]perDPGoldenEntry{}
	record := func(name string, e perDPGoldenEntry) {
		if _, dup := got[name]; dup {
			t.Fatalf("%s: duplicate row name", name)
		}
		got[name] = e
	}

	// --- The three buckets. The bucket appears only in the topic, so these
	// three rows are what pins [slotBucket]'s decision. ---

	// VALUES, a bool off a real switch descriptor. The canonical shape: the
	// overwhelming majority of the plane's traffic looks exactly like this.
	{
		f := newPerDPGoldenFixture(t)
		ch := f.dev.AddChannel("0001ABCD:1", 1, "TEST", hmenum.ParamsetKeyValues)
		putSwitchDP(ch, "STATE", true)
		record("values/bool", f.publish(t, "values/bool", goldenObservedAt, valuesKey("STATE"), true, ch))
	}

	// MASTER. The bucket is chosen from the paramset key alone, and MASTER is
	// exempt from the availability gate — configuration is not a runtime
	// reading, and a sleeping battery device may never deliver a fresh MASTER
	// read. So `available` is true here even though the DP is unobserved,
	// which is the opposite of the VALUES row below.
	{
		f := newPerDPGoldenFixture(t)
		ch := f.dev.AddChannel("0001ABCD:1", 1, "TEST", hmenum.ParamsetKeyValues)
		record("master/integer", f.publish(t, "master/integer", goldenObservedAt,
			masterKey("TEMPERATURE_MINIMUM"), 12, ch))
	}

	// CALCULATED. Routed by the channel's calculated-DP list, not by the
	// paramset key, and gated like VALUES — a derived value is only as good as
	// the sources it was computed from.
	{
		f := newPerDPGoldenFixture(t)
		ch := f.dev.AddChannel("0001ABCD:1", 1, "TEST", hmenum.ParamsetKeyValues)
		ch.AttachCalculatedDataPoint(&fakeCalcDP{
			key:      valuesKey("DEW_POINT"),
			category: hmenum.DataPointCategorySensor,
			value:    11.5,
			observed: true,
		})
		record("calculated/float", f.publish(t, "calculated/float", goldenObservedAt,
			valuesKey("DEW_POINT"), 11.5, ch))
	}

	// --- Value encodings. `Value` is `any`, marshalled by encoding/json
	// directly, so each Go type is its own wire shape. ---
	for _, vc := range []struct {
		name  string
		param string
		value any
	}{
		// A fractional float. JSON has one number type, so an integer-valued
		// float and an int are indistinguishable on the wire — which is why
		// the fraction is the interesting case and why it is pinned here as
		// well as in TestRenderValueFloatPrecision.
		{"values/float-fraction", "ACTUAL_TEMPERATURE", 21.45},
		// An int. Pinned separately because a change that routed every
		// numeric through a float formatter would turn this into "42.0" and
		// break every consumer templating an integer.
		{"values/int", "LEVEL_INT", 42},
		// A string. The shape an ENUM resolves to after label coercion, and
		// the shape a text datapoint carries natively.
		{"values/string", "DIRECTION_TEXT", "UP"},
		// A string list. Home Assistant cannot template a JSON array through
		// `value_json.value` without an index, so this shape is load-bearing
		// for the consumers that read it raw.
		{"values/string-list", "PARTY_MODE_LIST", []string{"P1", "P2"}},
		// nil. Documented as rare but reachable — an unobserved calculated
		// binary sensor publishes it at registration so the entity exists in
		// Home Assistant as `unknown` rather than not at all. `value` has NO
		// omitempty, so the key must survive as an explicit null; an
		// omitempty added to it would make this payload `{"available":…}` and
		// every consumer's `value_json.value` template would fail.
		{"values/null", "SMOKE_ALARM", nil},
	} {
		f := newPerDPGoldenFixture(t)
		ch := f.dev.AddChannel("0001ABCD:1", 1, "TEST", hmenum.ParamsetKeyValues)
		record(vc.name, f.publish(t, vc.name, goldenObservedAt, valuesKey(vc.param), vc.value, ch))
	}

	// --- The availability gate and the omitempty elisions. ---

	// An unobserved VALUES data point. `available` flips to false while
	// `value` still carries whatever the event reported — the two fields are
	// independent, and a consumer that reads `value` without checking
	// `available` shows a stale reading as live.
	{
		f := newPerDPGoldenFixture(t)
		ch := f.dev.AddChannel("0001ABCD:1", 1, "TEST", hmenum.ParamsetKeyValues)
		ch.Put(generic.NewFloatSensor(generic.Spec{
			Key: valuesKey("HUMIDITY"),
			Descriptor: hmproto.ParameterData{
				Type: hmenum.ParameterTypeFloat, Operations: hmenum.OperationsRead,
			},
			CentralName: "ccu-01",
		}))
		record("values/unobserved-unavailable", f.publish(t, "values/unobserved-unavailable",
			goldenObservedAt, valuesKey("HUMIDITY"), 55.0, ch))
	}

	// No channel object at all — the defensive path. Both metadata reads
	// resolve to nil, so `additional_information` is elided and `available`
	// defaults to TRUE: an unclassifiable entry must not be greyed out on
	// missing information. On the wire that is indistinguishable from a
	// confirmed reading, which is why it is pinned.
	{
		f := newPerDPGoldenFixture(t)
		record("values/no-channel-object", f.publish(t, "values/no-channel-object",
			goldenObservedAt, valuesKey("STATE"), true, nil))
	}

	// A zero event timestamp. Both time fields carry `omitempty`, so the two
	// keys vanish entirely rather than appearing as 0. A consumer reading
	// `value_json.modified_at` gets `undefined`, not an epoch at 1970.
	{
		f := newPerDPGoldenFixture(t)
		ch := f.dev.AddChannel("0001ABCD:1", 1, "TEST", hmenum.ParamsetKeyValues)
		record("values/zero-timestamp-elides-both", f.publish(t, "values/zero-timestamp-elides-both",
			time.Time{}, valuesKey("STATE"), false, ch))
	}

	// --- Enum-label coercion. ENUM wire values arrive as int indices; the
	// discovery config declares `options` from the same VALUE_LIST, so the
	// state has to carry the label or the entity shows a number the options
	// list does not contain. ---
	{
		f := newPerDPGoldenFixture(t)
		ch := f.dev.AddChannel("0001ABCD:1", 1, "TEST", hmenum.ParamsetKeyValues)
		putEnumDP(ch, "WINDOW_STATE", []string{"CLOSED", "TILTED", "OPEN"}, 2)
		record("values/enum-coerced-to-label", f.publish(t, "values/enum-coerced-to-label",
			goldenObservedAt, valuesKey("WINDOW_STATE"), 2, ch))
	}

	// --- additional_information. Enriched model metadata (battery type /
	// quantity / low-voltage limits) when the data point provides it, elided
	// otherwise. Additive by contract, so its presence is the thing to pin:
	// a consumer reading it must not have the block vanish, and a plain
	// scalar datapoint's payload must stay byte-identical to one from before
	// the field existed. ---
	{
		f := newPerDPGoldenFixture(t)
		ch := f.dev.AddChannel("0001ABCD:1", 1, "TEST", hmenum.ParamsetKeyValues)
		ch.Put(&enrichedDP{info: map[string]any{
			"Battery Type": "LR03",
			"Battery Qty":  2,
		}})
		record("values/additional-information", f.publish(t, "values/additional-information",
			goldenObservedAt, valuesKey("OPERATING_VOLTAGE"), 2.9, ch))
	}

	// --- F1. Two emissions of an UNCHANGED value at two different instants.
	// The pinned row is the SECOND one; see the finding note on this test. ---
	{
		f := newPerDPGoldenFixture(t)
		ch := f.dev.AddChannel("0001ABCD:1", 1, "TEST", hmenum.ParamsetKeyValues)
		putSwitchDP(ch, "STATE", true)
		first := f.publish(t, "F1 first", goldenObservedAt, valuesKey("STATE"), true, ch)
		second := f.publish(t, "F1 second", goldenRestampAt, valuesKey("STATE"), true, ch)

		// The defect, asserted rather than only pinned, so the finding is
		// legible in the failure message and not only in the golden diff.
		if first.Payload["value"] != second.Payload["value"] {
			t.Fatalf("F1: the two emissions do not carry the same value (%v vs %v) — "+
				"the fixture no longer exercises an unchanged reading",
				first.Payload["value"], second.Payload["value"])
		}
		if first.Payload["modified_at"] == second.Payload["modified_at"] {
			t.Errorf("F1: modified_at did NOT advance across two emissions of an unchanged value. " +
				"That is what PerDPState.ModifiedAt's doc comment promises, and it is not what the " +
				"code did when this pin was written — if this is now a deliberate fix, refresh the " +
				"golden and retire finding F1 from this test's doc comment")
		}
		if second.Payload["modified_at"] != second.Payload["refreshed_at"] {
			t.Errorf("F1: modified_at (%v) != refreshed_at (%v); publishSlotState assigned them the "+
				"same epoch unconditionally when this pin was written",
				second.Payload["modified_at"], second.Payload["refreshed_at"])
		}
		record("values/f1-unchanged-value-restamps-modified-at", second)
	}

	if *updatePerDPStateGolden {
		writePerDPStateGolden(t, got)
		t.Logf("rewrote %s with %d payloads", perDPStateGoldenPath, len(got))
		return
	}

	want := readPerDPStateGolden(t)
	names := make([]string, 0, len(got))
	for name := range got {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		w, pinned := want[name]
		if !pinned {
			t.Errorf("%s: not in the pin — a new state shape appeared; refresh the pin deliberately", name)
			continue
		}
		if got[name].Topic != w.Topic {
			t.Errorf("%s: topic\n got %s\nwant %s\n(a state message on a moved topic never updates its entity again, silently)",
				name, got[name].Topic, w.Topic)
		}
		if g, wantBody := perDPCanonical(t, got[name].Payload), perDPCanonical(t, w.Payload); g != wantBody {
			t.Errorf("%s: payload\n got %s\nwant %s", name, g, wantBody)
		}
	}
	for name := range want {
		if _, still := got[name]; !still {
			t.Errorf("%s: in the pin but no longer produced — a state shape disappeared", name)
		}
	}
}

func readPerDPStateGolden(t *testing.T) map[string]perDPGoldenEntry {
	t.Helper()
	raw, err := os.ReadFile(perDPStateGoldenPath)
	if err != nil {
		t.Fatalf("read %s: %v (create it with -update-per-dp-state-golden)", perDPStateGoldenPath, err)
	}
	var out map[string]perDPGoldenEntry
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("parse %s: %v", perDPStateGoldenPath, err)
	}
	return out
}

func writePerDPStateGolden(t *testing.T, entries map[string]perDPGoldenEntry) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(perDPStateGoldenPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	raw, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(perDPStateGoldenPath, append(raw, '\n'), 0o600); err != nil {
		t.Fatalf("write %s: %v", perDPStateGoldenPath, err)
	}
}
