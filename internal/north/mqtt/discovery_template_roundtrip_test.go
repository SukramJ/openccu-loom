// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/model/combined"
	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/naming"
	pload "github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/internal/testsupport/hajinja"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// The template round trip: every entity kind this daemon discovers, its
// discovery body from the production builder, the payload of every topic the
// body reads from the production publisher, and each of the body's own
// templates rendered over that payload with Jinja2's semantics
// ([hajinja]). The goldens pin what a builder emits and the unit tests pin
// what a publisher emits; neither proves the two agree, which is how a
// `number` whose template was missing from its golden kept a state Home
// Assistant could not parse.
//
// The assertion per platform is what Home Assistant's own platform accepts
// for the rendered state (homeassistant/components/mqtt/ in the 2026.10
// core checkout): a number parses as a float (number.py:176-188), a select
// value is one of its options (select.py:118-132), a binary sensor or switch
// value is one of its two payloads, an event renders a JSON object whose
// `event_type` is announced (event.py:128-160), an alarm panel value is a
// panel state, attributes render a JSON object, and every availability entry
// renders one of its two payloads.

// readerTemplates maps every discovery key that names a topic Home Assistant
// READS to the key of the template it reads it through ("" = no template
// key exists for it on any platform this daemon declares).
var readerTemplates = map[string]string{
	"state_topic":               "value_template",
	"json_attributes_topic":     "json_attributes_template",
	"position_topic":            "position_template",
	"tilt_status_topic":         "tilt_status_template",
	"current_temperature_topic": "current_temperature_template",
	"current_humidity_topic":    "current_humidity_template",
	"temperature_state_topic":   "temperature_state_template",
	"mode_state_topic":          "mode_state_template",
	"preset_mode_state_topic":   "preset_mode_value_template",
	"action_topic":              "action_template",
	"latest_version_topic":      "latest_version_template",
	"brightness_state_topic":    "brightness_value_template",
	"percentage_state_topic":    "percentage_value_template",
}

// stateTemplateFor is the template that reads `state_topic` on platform.
func stateTemplateFor(body map[string]any) string {
	for _, k := range []string{"value_template", "state_value_template"} {
		if s, ok := body[k].(string); ok {
			return s
		}
	}
	return ""
}

// rtRig is one bridge with a recording publisher, plus the helpers that
// feed every topic a body reads from the production publish path.
type rtRig struct {
	t      *testing.T
	ctx    context.Context
	obs    *observedPlane
	bridge *Bridge
	base   string
}

func newRTRig(t *testing.T, base, central string) *rtRig {
	t.Helper()
	obs := newObservedPlane()
	b := NewBridge(BridgeConfig{Base: base, CentralName: central, RawEnabled: true, HADiscoveryEnabled: true}, obs)
	return &rtRig{t: t, ctx: context.Background(), obs: obs, bridge: b, base: b.topics.Base}
}

func (r *rtRig) last(topic string) (string, bool) {
	found, payload := false, ""
	for _, rec := range r.obs.records() {
		if rec.topic == topic {
			found, payload = true, rec.payload
		}
	}
	return payload, found
}

// addr is a [pload.MQTTAddressable] that names one state topic: the hub
// publishers take the model object only to resolve its topic, and the
// rendered body already holds that topic.
type addr struct{ state string }

func (a addr) MQTTTopics(string, string) pload.MQTTTopicSet {
	return pload.MQTTTopicSet{State: a.state}
}

func (a addr) MQTTTopicsForInterface(string, string, string) pload.MQTTTopicSet {
	return pload.MQTTTopicSet{State: a.state}
}

var mapFirstKeyRe = regexp.MustCompile(`\{% set m = \{'((?:[^'\\]|\\.)*)'`)

// wireToken is the token the state plane publishes for an entity that shows
// options: the first key of its token→label map, or its first option
// spelled the way the CCU spells tokens.
func wireToken(body map[string]any) string {
	if m := mapFirstKeyRe.FindStringSubmatch(stateTemplateFor(body)); m != nil {
		return m[1]
	}
	if opts := options(body); len(opts) > 0 {
		if strings.Contains(stateTemplateFor(body), "| lower") {
			return strings.ToUpper(opts[0])
		}
		return opts[0]
	}
	return "TOKEN"
}

func options(body map[string]any) []string {
	raw, _ := body["options"].([]any)
	out := make([]string, 0, len(raw))
	for _, o := range raw {
		s, _ := o.(string)
		out = append(out, s)
	}
	return out
}

// scalarFor is the value the per-data-point state plane publishes for an
// entity of the body's platform: a JSON boolean or number for the boolean
// and numeric platforms, the CCU token for an enum, a string otherwise.
func scalarFor(platform string, body map[string]any) any {
	intOf := func(s string) (any, bool) {
		if i, err := strconv.ParseInt(s, 10, 64); err == nil {
			return i, true
		}
		return nil, false
	}
	switch platform {
	case "switch", "binary_sensor":
		on, _ := body["payload_on"].(string)
		if v, ok := intOf(on); ok {
			return v
		}
		return true
	case "lock":
		locked, _ := body["state_locked"].(string)
		if v, ok := intOf(locked); ok {
			return v
		}
		return true
	case "select":
		return wireToken(body)
	case "text":
		return "Hallo"
	case "sensor":
		if len(options(body)) > 0 {
			return wireToken(body)
		}
		return 21.5
	case "event":
		return true
	}
	return 0.5
}

// itemOf strips `<base>/status/` and splits the item path, or returns nil.
func (r *rtRig) itemOf(topic string) []string {
	rest, ok := strings.CutPrefix(topic, naming.StatusTopic(r.base))
	if !ok {
		return nil
	}
	return strings.Split(rest, "/")
}

// feed publishes, through the production publisher for its shape, one
// payload on topic for an entity declared by body. It reports false for a
// topic shape it does not know, which the caller turns into a failure.
func (r *rtRig) feed(topic string, body map[string]any, platform string) bool {
	t, ctx, b := r.t, r.ctx, r.bridge
	t.Helper()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("publish %s: %v", topic, err)
		}
	}
	if topic == b.topics.Connected() {
		must(b.AnnounceOnline(ctx))
		_, err := b.SetConnected(ctx, 2)
		must(err)
		return true
	}
	if strings.HasPrefix(topic, naming.MetaTopic(r.base)) {
		item := strings.Split(strings.TrimPrefix(topic, naming.MetaTopic(r.base)), "/")
		if len(item) == 6 {
			ch, _ := strconv.Atoi(item[3])
			slot := pload.TopicSlot{Address: item[2], Channel: ch, Bucket: bucketOf(item[4]), Parameter: item[5]}
			must(b.PublishSlotConfig(ctx, item[0], item[1], slot, map[string]any{"type": "FLOAT", "unit": "°C"}))
			return true
		}
		return false
	}
	it := r.itemOf(topic)
	if it == nil {
		return false
	}
	switch {
	case len(it) == 2 && it[1] == "online":
		must(b.PublishHubReachability(ctx, it[0], true))
		return true
	case len(it) == 4 && it[3] == "online":
		must(b.PublishAvailability(ctx, it[0], it[1], it[2], true))
		return true
	case len(it) == 4 && it[3] == "update":
		must(b.PublishUpdateState(ctx, it[0], it[1], it[2], map[string]any{
			"firmware": "1.0", "latest_firmware": "1.1", "in_progress": false, "firmware_update_state": "UP_TO_DATE",
		}))
		return true
	case len(it) == 6 && (it[4] == "values" || it[4] == "master" || it[4] == "calculated"):
		ch, _ := strconv.Atoi(it[3])
		slot := pload.TopicSlot{Address: it[2], Channel: ch, Bucket: bucketOf(it[4]), Parameter: it[5]}
		must(b.PublishSlotState(ctx, it[0], it[1], slot, pload.PerDPState{Value: scalarFor(platform, body), Available: true}))
		return true
	case len(it) == 5 && (it[4] == "event" || it[4] == "impulse" || it[4] == "device_error"):
		ch, _ := strconv.Atoi(it[3])
		types, _ := body["event_types"].([]any)
		typ := "press_short"
		if len(types) > 0 {
			typ, _ = types[0].(string)
		}
		switch it[4] {
		case "event":
			must(b.PublishChannelEventState(ctx, it[0], it[1], it[2], ch, "", typ))
		case "impulse":
			must(b.PublishChannelImpulseState(ctx, it[0], it[1], it[2], ch, typ))
		default:
			must(b.PublishChannelDeviceErrorState(ctx, it[0], it[1], it[2], ch, typ))
		}
		return true
	case len(it) == 5 && it[4] == segWeekProfile:
		ch, _ := strconv.Atoi(it[3])
		must(b.PublishWeekProfileState(ctx, it[0], it[1], it[2], ch, wireToken(body)))
		return true
	case len(it) == 6 && it[4] == segSchedule && it[5] == "active_entries":
		ch, _ := strconv.Atoi(it[3])
		must(b.PublishScheduleEntityState(ctx, it[0], it[1], it[2], ch, 3))
		return true
	case len(it) == 6 && it[4] == segSchedule && it[5] == "attributes":
		ch, _ := strconv.Atoi(it[3])
		must(b.PublishScheduleEntityAttrs(ctx, it[0], it[1], it[2], ch, map[string]any{"schedule_enabled": true}))
		return true
	case len(it) == 7 && it[4] == segSchedule:
		ch, _ := strconv.Atoi(it[3])
		must(b.PublishScheduleSwitchState(ctx, it[0], it[1], it[2], ch, it[6], true))
		return true
	case len(it) >= 2 && it[0] == "system" && it[1] == "addon_update":
		must(b.PublishAddonUpdateState(ctx, "1.0.0", "1.1.0", false))
		return true
	case len(it) == 3 && it[1] == "system":
		must(b.PublishHubSystemHealthScore(ctx, it[0], 97.5))
		must(b.PublishHubConnectionLatency(ctx, it[0], 42))
		must(b.PublishHubLastEventAge(ctx, it[0], 3))
		return true
	case len(it) == 3 && it[1] == "hub" && it[2] == "update":
		must(b.PublishHubUpdate(ctx, it[0], "1.0.0", "1.1.0", false))
		return true
	case len(it) == 3 && it[1] == "hub":
		items := []any{map[string]any{"id": "1", "name": "Meldung"}}
		must(b.PublishAlarmMessages(ctx, it[0], addr{topic}, items))
		return true
	case len(it) == 4 && it[1] == "hub" && it[2] == "sysvars":
		v := scalarFor(platform, body)
		if platform == "sensor" && len(options(body)) == 0 {
			v = 21.5
		}
		must(b.PublishSysvar(ctx, it[0], addr{topic}, v))
		return true
	case len(it) == 5 && it[1] == "hub" && it[2] == "programs" && it[4] == "active":
		must(b.PublishProgram(ctx, it[0], addr{topic}, true))
		return true
	case len(it) == 5 && it[1] == "hub" && it[2] == "programs" && it[4] == "execute_available":
		must(b.PublishRoleAvailability(ctx, &pload.MQTTRole{Topics: pload.MQTTTopicSet{Availability: topic}}, true))
		return true
	case len(it) == 4 && it[1] == "hub" && it[2] == "connectivity":
		must(b.PublishConnectivity(ctx, it[0], addr{topic}, it[3], true))
		return true
	case len(it) == 4 && it[1] == "hub" && it[2] == "install_mode":
		must(b.PublishInstallMode(ctx, it[0], it[3], 120))
		return true
	}
	return false
}

// rtCase is one discovered entity: its body and, for the shapes whose state
// the rig cannot derive from the topic alone, the payloads already
// published for it.
type rtCase struct {
	name string
	body map[string]any
	// platform is the HA component the config was published under.
	platform string
}

// checkCase feeds every topic the body reads that nothing published yet,
// renders every reader template over it, and asserts the platform accepts
// the result.
func (r *rtRig) checkCase(c rtCase) {
	t := r.t
	t.Helper()
	body := c.body
	render := func(tmpl, payload string) (string, error) {
		if tmpl == "" {
			return strings.TrimSpace(payload), nil
		}
		return hajinja.RenderValue(tmpl, payload)
	}
	payloadOf := func(topic string) (string, bool) {
		if p, ok := r.last(topic); ok {
			return p, true
		}
		if !r.feed(topic, body, c.platform) {
			return "", false
		}
		return r.last(topic)
	}
	for _, key := range hajinja.SortedKeys(body) {
		// Command topics are written, not read; `set_position_topic` is
		// the cover's one command topic not spelled `*command*`.
		if !strings.HasSuffix(key, "_topic") || strings.Contains(key, "command") || strings.HasPrefix(key, "set_") {
			continue
		}
		topic, _ := body[key].(string)
		tmplKey, known := readerTemplates[key]
		if !known {
			t.Errorf("%s: %s is a topic Home Assistant reads, and this test does not know its template key — extend readerTemplates", c.name, key)
			continue
		}
		payload, ok := payloadOf(topic)
		if !ok {
			t.Errorf("%s: no production publisher wrote %s (%s)", c.name, key, topic)
			continue
		}
		tmpl, _ := body[tmplKey].(string)
		if key == "state_topic" {
			tmpl = stateTemplateFor(body)
		}
		got, err := render(tmpl, payload)
		if err != nil {
			t.Errorf("%s: %s %q over %s fails in Jinja: %v", c.name, tmplKey, tmpl, payload, err)
			continue
		}
		if key == "json_attributes_topic" {
			var obj map[string]any
			if json.Unmarshal([]byte(got), &obj) != nil {
				t.Errorf("%s: json_attributes_template renders %q, not a JSON object — HA drops the attributes", c.name, got)
			}
			continue
		}
		if key == "state_topic" {
			if msg := acceptsState(c.platform, body, got); msg != "" {
				t.Errorf("%s: state_topic payload %s renders %q through %q: %s", c.name, payload, got, tmpl, msg)
			}
			continue
		}
		if got == "" {
			t.Errorf("%s: %s renders nothing over %s", c.name, tmplKey, payload)
		}
	}
	avail, _ := body["availability"].([]any)
	for i, raw := range avail {
		entry, _ := raw.(map[string]any)
		topic, _ := entry["topic"].(string)
		payload, ok := payloadOf(topic)
		if !ok {
			t.Errorf("%s: availability[%d] %s has no production publisher", c.name, i, topic)
			continue
		}
		tmpl, _ := entry["value_template"].(string)
		got, err := render(tmpl, payload)
		on, _ := entry["payload_available"].(string)
		off, _ := entry["payload_not_available"].(string)
		if on == "" {
			on, off = "online", "offline"
		}
		if err != nil || (got != on && got != off) {
			t.Errorf("%s: availability[%d] %s renders %q (%v) over %s, want %q or %q", c.name, i, topic, got, err, payload, on, off)
		}
	}
}

var alarmPanelStates = []string{
	"disarmed", "armed_home", "armed_away", "armed_night", "armed_vacation",
	"armed_custom_bypass", "pending", "triggered", "arming", "disarming",
}

// acceptsState is the platform's own acceptance of a rendered state, "" when
// it is accepted.
func acceptsState(platform string, body map[string]any, got string) string {
	str := func(k, def string) string {
		if s, ok := body[k].(string); ok {
			return s
		}
		return def
	}
	switch platform {
	case "number":
		if _, err := strconv.ParseFloat(got, 64); err != nil {
			return "not a number (number.py float(payload))"
		}
	case "select":
		if !slices.Contains(options(body), got) {
			return fmt.Sprintf("not one of options %v (select.py Invalid option)", options(body))
		}
	case "binary_sensor":
		if got != str("payload_on", "ON") && got != str("payload_off", "OFF") {
			return "neither payload_on nor payload_off"
		}
	case "switch":
		on := str("state_on", str("payload_on", "ON"))
		off := str("state_off", str("payload_off", "OFF"))
		if got != on && got != off {
			return "neither state_on nor state_off"
		}
	case "lock":
		ok := false
		for _, k := range []string{"state_locked", "state_unlocked", "state_locking", "state_unlocking", "state_jammed", "state_open", "state_opening"} {
			if got == str(k, strings.ToUpper(strings.TrimPrefix(k, "state_"))) {
				ok = true
			}
		}
		if !ok {
			return "no lock state matches"
		}
	case "event":
		var doc map[string]any
		if json.Unmarshal([]byte(got), &doc) != nil {
			return "not a JSON event document (event.py)"
		}
		types, _ := body["event_types"].([]any)
		if !slices.Contains(types, doc["event_type"]) {
			return fmt.Sprintf("event_type %v is not announced in %v", doc["event_type"], types)
		}
	case "alarm_control_panel":
		if !slices.Contains(alarmPanelStates, got) {
			return "not an alarm panel state"
		}
	case "sensor":
		if opts := options(body); len(opts) > 0 && !slices.Contains(opts, got) {
			return fmt.Sprintf("not one of options %v", opts)
		}
		if got == "" {
			return "renders nothing"
		}
	case "update", "text", "cover", "valve", "siren", "climate":
		if got == "" && platform != "text" {
			return "renders nothing"
		}
	}
	return ""
}

func rtDecodeBody(t *testing.T, name string, buf []byte) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(buf, &body); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return body
}

// TestEveryDiscoveredTemplateReadsWhatItsPublisherWrites is the round trip
// described at the top of this file, over every fixture family the goldens
// pin: per-data-point entities of each platform, the custom-DP aggregates
// with their model-computed state, channel events, the combined
// projections, the hub plane, the week profile, the schedule, the device
// and add-on update entities, and — through their real publishers — the
// alarm and Security & Safety planes.
//
// Known gaps, named rather than skipped:
//   - the per-data-point `event` fallback (an event parameter on a channel
//     the channel-event aggregate does not claim) reads the data point's
//     own boolean status item, whose `val` is no event type; the aggregate
//     channel event is what production declares for press parameters, and
//     it is covered below;
//   - the JSON-schema light parses its state document natively, with no
//     template to render; [checkJSONLight] checks instead that it reads its
//     `ha` twin, that the twin is HA's document, and that the `status` twin
//     is a status object carrying the same document.
func TestEveryDiscoveredTemplateReadsWhatItsPublisherWrites(t *testing.T) {
	t.Parallel()

	t.Run("per-data-point", func(t *testing.T) {
		t.Parallel()
		db := NewDefaultDiscoveryBuilder(NewTopicBuilder("gh"), "ccu-01")
		db.SetHubInfoFor("ccu-01", HubInfo{Serial: "3014F711A0001234"})
		db.SetHubInfoFor("ccu-02", HubInfo{Serial: "3014F711B0005678"})
		for _, c := range append(goldenCases(), goldenSubDeviceCases()...) {
			if c.ev.Category == hmenum.DataPointCategoryEvent {
				continue // the documented per-data-point event gap
			}
			component, _, _, buf, ok := db.Build(c.ev)
			if !ok {
				continue
			}
			r := newRTRig(t, "gh", c.ev.Central)
			r.checkCase(rtCase{name: c.name, body: rtDecodeBody(t, c.name, buf), platform: component})
		}
	})

	t.Run("custom-dp aggregates and channel events", func(t *testing.T) {
		t.Parallel()
		for _, c := range aggregateGoldenCases() {
			component, _, _, buf, ok := newAggregateGoldenBuilder(c.subDevices).buildGoldenCase(t, c)
			if !ok {
				t.Fatalf("%s: no entity", c.name)
			}
			body := rtDecodeBody(t, c.name, buf)
			r := newRTRig(t, aggregateGoldenBase, aggregateGoldenCentral)
			if c.source != nil {
				src := c.source(t)
				st, _ := body["state_topic"].(string)
				if st == "" {
					st, _ = body["mode_state_topic"].(string)
				}
				// The light reads its `ha` twin; the publisher is addressed
				// by the status item both come from.
				if rest, isHA := strings.CutPrefix(st, naming.HATopic(r.base)); isHA {
					st = naming.StatusTopic(r.base) + rest
				}
				if it := r.itemOf(st); len(it) == 6 && it[4] == "custom" {
					ch, _ := strconv.Atoi(it[3])
					slot := pload.TopicSlot{Address: it[2], Channel: ch, Bucket: pload.BucketCustom, Parameter: it[5]}
					if err := r.bridge.PublishCustomDPState(r.ctx, it[0], it[1], slot, src.State()); err != nil {
						t.Fatalf("%s: publish aggregate: %v", c.name, err)
					}
				}
			}
			if component == "light" {
				checkJSONLight(t, r, c.name, body)
				delete(body, "state_topic")
			}
			r.checkCase(rtCase{name: c.name, body: body, platform: component})
		}
	})

	t.Run("combined projections", func(t *testing.T) {
		t.Parallel()
		for _, c := range combinedGoldenCases(t) {
			db := NewDefaultDiscoveryBuilder(NewTopicBuilder(secGoldenBase), "ccu-01")
			db.SetHubInfoFor("ccu-01", HubInfo{Serial: "3014F711A0001234"})
			db.SetHubInfoFor("ccu-02", HubInfo{Serial: "3014F711B0005678"})
			item := db.BuildCombinedDiscovery(c.ev.Central, c.ev)
			if !item.OK {
				continue
			}
			body := rtDecodeBody(t, c.name, item.Payload)
			r := newRTRig(t, secGoldenBase, c.ev.Central)
			if err := r.bridge.PublishCombinedState(r.ctx, c.ev.Central, c.ev.Interface, c.ev.DeviceAddress,
				c.ev.ChannelNo, c.ev.Kind, combinedStateFor(c.ev.Kind, body)); err != nil {
				t.Fatalf("%s: publish combined: %v", c.name, err)
			}
			r.checkCase(rtCase{name: c.name, body: body, platform: item.Component})
		}
	})

	t.Run("hub", func(t *testing.T) {
		t.Parallel()
		for _, c := range hubGoldenCases() {
			if !c.item.OK || len(c.item.Payload) == 0 {
				continue
			}
			r := newRTRig(t, "gh", "ccu-01")
			r.checkCase(rtCase{name: c.name, body: rtDecodeBody(t, c.name, c.item.Payload), platform: c.item.Component})
		}
	})

	t.Run("schedule, week profile and update", func(t *testing.T) {
		t.Parallel()
		for name, item := range scheduleGoldenBuilt(t) {
			r := newRTRig(t, "gh", "ccu-01")
			r.checkCase(rtCase{name: name, body: rtDecodeBody(t, name, item.Payload), platform: item.Component})
		}
		wdb := scheduleGoldenBuilder()
		for _, c := range wpGoldenCases() {
			item := wdb.BuildWeekProfileDiscovery(c.ev.Central, c.ev)
			if !item.OK {
				continue
			}
			r := newRTRig(t, "gh", "ccu-01")
			r.checkCase(rtCase{name: c.name, body: rtDecodeBody(t, c.name, item.Payload), platform: item.Component})
		}
		addon := newHubBuilder().BuildAddonUpdateDiscovery()
		r := newRTRig(t, "openccu-loom", "ccu-01")
		r.checkCase(rtCase{name: "addon_update", body: rtDecodeBody(t, "addon", addon.Payload), platform: addon.Component})
	})

	t.Run("alarm and security through their publishers", func(t *testing.T) {
		t.Parallel()
		for label, obs := range map[string]*observedPlane{
			"alarm": runAlarmPlane(t, "gh"), "security": runSecurityPlane(t, "gh"),
		} {
			r := &rtRig{
				t: t, ctx: context.Background(), obs: obs, base: "gh",
				bridge: NewBridge(BridgeConfig{Base: "gh", CentralName: "ccu-01", RawEnabled: true}, obs),
			}
			checked := 0
			for _, rec := range obs.records() {
				if !isDiscoveryConfigTopic(rec.topic) || rec.payload == "" {
					continue
				}
				parts := strings.Split(rec.topic, "/")
				r.checkCase(rtCase{name: label + " " + rec.topic, body: rtDecodeBody(t, rec.topic, []byte(rec.payload)), platform: parts[1]})
				checked++
			}
			if checked == 0 {
				t.Errorf("%s: the plane declared no entity — the round trip would be vacuous", label)
			}
		}
	})
}

// checkJSONLight is the light's round trip. Its entity has no template to
// render: Home Assistant's JSON-schema light parses the state topic's
// document natively (light/schema_json.py, `_state_received`), so the state
// topic must be the `ha` twin and carry that document bare — an object with
// `state` ON/OFF — while the `status` twin carries the same document as a
// well-formed status object's `val`.
func checkJSONLight(t *testing.T, r *rtRig, name string, body map[string]any) {
	t.Helper()
	st, _ := body["state_topic"].(string)
	rest, isHA := strings.CutPrefix(st, naming.HATopic(r.base))
	if !isHA {
		t.Errorf("%s: the JSON-schema light reads %s, not its `ha` twin", name, st)
		return
	}
	payload, ok := r.last(st)
	var doc map[string]any
	if !ok || json.Unmarshal([]byte(payload), &doc) != nil || (doc["state"] != "ON" && doc["state"] != "OFF") {
		t.Errorf("%s: %s carries %q, not HA's light document with `state` ON/OFF", name, st, payload)
		return
	}
	status, ok := r.last(naming.StatusTopic(r.base) + rest)
	var obj map[string]any
	if !ok || json.Unmarshal([]byte(status), &obj) != nil {
		t.Errorf("%s: the `status` twin carries %q, not a JSON object", name, status)
		return
	}
	_, hasTS := obj["ts"].(float64)
	_, hasLC := obj["lc"].(float64)
	if !hasTS || !hasLC || !reflect.DeepEqual(obj["val"], doc) {
		t.Errorf("%s: the `status` twin %s is not a status object whose `val` is the `ha` document %s", name, status, payload)
	}
}

// combinedStateFor is the state the projection of kind publishes, in the
// encoding its own CombinedStatePayload uses.
func combinedStateFor(kind string, body map[string]any) string {
	switch kind {
	case combined.KindDuration:
		return "30"
	case combined.KindLevelCombined:
		return combined.EncodeLevelCompositeJSON(combined.LevelComposite{
			Level: custom.NewPosition(0.4), SlatsLevel: custom.NewPosition(0.2),
		})
	case combined.KindHSColor:
		return combined.EncodeHSJSON(combined.HS{Hue: 120, Saturation: 0.5})
	}
	if opts := options(body); len(opts) > 0 {
		return wireToken(body)
	}
	return "1"
}

// bucketOf is the paramset bucket an item path segment names.
func bucketOf(seg string) pload.Bucket {
	for _, b := range []pload.Bucket{pload.BucketValues, pload.BucketMaster, pload.BucketCalculated, pload.BucketCustom} {
		if b.String() == seg {
			return b
		}
	}
	return pload.BucketUnset
}

// TestPulsesNeverCarryANullExtension pins that a pulse or status published
// without facets omits `hm` rather than spelling it `null`. The event
// template survives either (`value_json.hm or {}`), but a typed nil map
// reaching the shared renderer used to marshal as `"hm":null`.
func TestPulsesNeverCarryANullExtension(t *testing.T) {
	t.Parallel()
	r := newRTRig(t, "gh", "ccu-01")
	var none map[string]any
	if err := r.bridge.PublishSecurityEvent(r.ctx, "gh/status/security/event", "triggered", none, time.Time{}); err != nil {
		t.Fatal(err)
	}
	if err := r.bridge.PublishSystemStatus(r.ctx, "ccu-01", "ping", none, time.Time{}); err != nil {
		t.Fatal(err)
	}
	if err := r.bridge.PublishSecurityState(r.ctx, "gh/status/security/state", "disarmed", none); err != nil {
		t.Fatal(err)
	}
	recs := r.obs.records()
	if len(recs) < 3 {
		t.Fatalf("published %d messages, want 3", len(recs))
	}
	for _, rec := range recs {
		if strings.Contains(rec.payload, `"hm":null`) {
			t.Errorf("%s carries a null extension: %s", rec.topic, rec.payload)
		}
		got, err := hajinja.RenderValue(eventValueTemplate, rec.payload)
		if err != nil || !strings.Contains(got, `"event_type"`) {
			t.Errorf("%s: the event template renders %q (%v) over %s", rec.topic, got, err, rec.payload)
		}
	}
}

// TestTextCommandsKeepEmptyAndBraceLeadingStrings renders every discovered
// `text` entity's command_template over the two strings the `set` grammar
// cannot carry bare — the empty one and one opening with `{` — and delivers
// the result on its command topic. Before ADR 0083 the handler received both:
// `""` as a nil value and `{foo` verbatim (parseCommandPayload). Sent bare,
// the first is now dropped as an eviction and the second rejected as
// malformed JSON; the template is what keeps them.
func TestTextCommandsKeepEmptyAndBraceLeadingStrings(t *testing.T) {
	t.Parallel()
	db := NewDefaultDiscoveryBuilder(NewTopicBuilder("gh"), "ccu-01")
	db.SetHubInfoFor("ccu-01", HubInfo{Serial: "3014F711A0001234"})
	db.SetHubInfoFor("ccu-02", HubInfo{Serial: "3014F711B0005678"})
	var bodies []map[string]any
	for _, c := range goldenCases() {
		if component, _, _, buf, ok := db.Build(c.ev); ok && component == "text" {
			bodies = append(bodies, rtDecodeBody(t, c.name, buf))
		}
	}
	for _, c := range hubGoldenCases() {
		if c.item.OK && c.item.Component == "text" {
			bodies = append(bodies, rtDecodeBody(t, c.name, c.item.Payload))
		}
	}
	if len(bodies) < 2 {
		t.Fatalf("found %d text entities, want the per-data-point and the sysvar one", len(bodies))
	}
	for _, body := range bodies {
		tmpl, _ := body["command_template"].(string)
		topic, _ := body["command_topic"].(string)
		for _, tc := range []struct {
			in   string
			want any
		}{{"", nil}, {"{foo", "{foo"}, {"Hallo Welt", "Hallo Welt"}} {
			wire, err := hajinja.Render(tmpl, map[string]any{"value": tc.in})
			if err != nil {
				t.Fatalf("%s: command_template %q: %v", topic, tmpl, err)
			}
			noop := NewNoopClient()
			sink := &fakeSink{}
			sub := NewCommandSubscriber(noop, NewTopicBuilder("gh"), sink, nil)
			if err := sub.Start(context.Background()); err != nil {
				t.Fatal(err)
			}
			filter := "gh/set/+/+/+/+/+/+"
			if strings.Contains(topic, "/hub/sysvars/") {
				filter = "gh/set/+/hub/sysvars/+"
			}
			if !noop.DeliverInbound(filter, topic, []byte(wire)) {
				t.Fatalf("%s: no route", topic)
			}
			sub.WaitIdle()
			calls, got := sink.setValues.Load(), sink.lastVal.value
			switch {
			case strings.Contains(topic, "/hub/sysvars/"):
				calls, got = sink.setSysvars.Load(), sink.lastSysvar.value
			case strings.Contains(topic, "/master/"):
				calls, got = sink.masterValues.Load(), sink.lastMaster.value
			}
			if calls != 1 || got != tc.want {
				t.Errorf("%s: HA sends %q for %q; the handler got %d call(s) with %#v, want one with %#v",
					topic, wire, tc.in, calls, got, tc.want)
			}
			sub.Close()
		}
	}
}

// TestSelfReportingEntitiesStayAvailableWithoutACentral renders the
// `connected` availability entry of every hub and add-on entity over level 1
// — the daemon is up and no central is reachable. The entities that report
// that situation must stay available through it; every other one must not.
func TestSelfReportingEntitiesStayAvailableWithoutACentral(t *testing.T) {
	t.Parallel()
	selfReporting := func(name string) bool { return strings.HasPrefix(name, "connectivity/") || name == "addon_update" }
	items := map[string]DiscoveryItem{"addon_update": newHubBuilder().BuildAddonUpdateDiscovery()}
	for _, c := range hubGoldenCases() {
		items[c.name] = c.item
	}
	for name, item := range items {
		if !item.OK {
			continue
		}
		avail, _ := rtDecodeBody(t, name, item.Payload)["availability"].([]any)
		for _, raw := range avail {
			entry, _ := raw.(map[string]any)
			if topic, _ := entry["topic"].(string); !strings.HasSuffix(topic, "/connected") {
				continue
			}
			tmpl, _ := entry["value_template"].(string)
			got, err := hajinja.RenderValue(tmpl, "1")
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			if want := map[bool]string{true: "online", false: "offline"}[selfReporting(name)]; got != want {
				t.Errorf("%s: at connected=1 the entity is %s, want %s", name, got, want)
			}
		}
	}
}
