// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

//go:build e2e

package e2e

import (
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/tests/e2e/harness"
)

type projectionAnswer struct {
	Address string `json:"address"`
	Central string `json:"central"`
	Planes  []struct {
		Plane   string            `json:"plane"`
		State   string            `json:"state"`
		Reason  string            `json:"reason"`
		Entries []json.RawMessage `json:"entries"`
	} `json:"planes"`
}

func getProjection(t *testing.T, h *harness.Harness, path string) (int, projectionAnswer) {
	t.Helper()
	req, err := h.REST().NewRequest(http.MethodGet, path, nil)
	if err != nil {
		t.Fatalf("request %s: %v", path, err)
	}
	resp, err := h.REST().Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var out projectionAnswer
	if resp.StatusCode == http.StatusOK {
		if err := json.Unmarshal(body, &out); err != nil {
			t.Fatalf("decode %s: %v (%s)", path, err, body)
		}
	}
	return resp.StatusCode, out
}

// TestDeviceProjectionAnswersThroughTheDaemon boots the built daemon and
// asks for a device's projection over REST. The harness runs without the
// Matter bridge, so the Matter plane must say so instead of listing
// entries: the answer crosses the route, the composition root's projector
// and the plane the Matter wiring hands it.
func TestDeviceProjectionAnswersThroughTheDaemon(t *testing.T) {
	t.Parallel()
	h := harness.Start(t, harness.Options{AuthMode: harness.AuthSession})
	if err := h.REST().LoginSession(harness.AdminUser, harness.AdminPass); err != nil {
		t.Fatalf("login: %v", err)
	}
	devices := getJSONArray(t, h, "/api/v1/devices", "items")
	first, _ := devices[0].(map[string]any)
	addr, _ := first["address"].(string)
	if addr == "" {
		t.Fatalf("no device address in %v", devices[0])
	}

	status, out := getProjection(t, h, "/api/v1/devices/"+addr+"/projection")
	if status != http.StatusOK {
		t.Fatalf("status %d", status)
	}
	if out.Address != addr || out.Central == "" {
		t.Errorf("answer names %q on %q; want %q on its central", out.Address, out.Central, addr)
	}
	want := []string{"matter", "measurements", "ha_discovery", "mqtt_raw"}
	got := make([]string, 0, len(out.Planes))
	for _, p := range out.Planes {
		got = append(got, p.Plane)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("planes = %v; want %v", got, want)
	}
	for _, p := range out.Planes {
		if p.State != "inactive" || p.Reason != "plane_disabled" || len(p.Entries) != 0 {
			t.Errorf("%s plane = %+v; want inactive, plane_disabled, no entries", p.Plane, p)
		}
	}

	if status, out := getProjection(t, h, "/api/v1/devices/"+addr+"/projection?plane=nope"); status != http.StatusOK || len(out.Planes) != 0 {
		t.Errorf("plane=nope: status %d, planes %+v; want 200 and none", status, out.Planes)
	}
	if status, _ := getProjection(t, h, "/api/v1/devices/NO-SUCH-DEVICE/projection"); status != http.StatusNotFound {
		t.Errorf("unknown device: status %d; want 404", status)
	}
}

type measurementEntry struct {
	Channel  int               `json:"channel"`
	Key      string            `json:"key"`
	Verdict  string            `json:"verdict"`
	Reason   string            `json:"reason"`
	Identity map[string]string `json:"identity"`
}

// measurementEntries is the measurements plane of the rig's device,
// narrowed to its measuring channel and keyed by parameter.
func measurementEntries(t *testing.T, rig *forwardingRig) (state, reason string, byParam map[string]measurementEntry) {
	t.Helper()
	addr, _, _ := strings.Cut(rig.channel, ":")
	status, out := getProjection(t, rig.h, "/api/v1/devices/"+addr+"/projection?plane=measurements")
	if status != http.StatusOK || len(out.Planes) != 1 {
		t.Fatalf("projection: status %d, planes %+v", status, out.Planes)
	}
	byParam = map[string]measurementEntry{}
	for _, raw := range out.Planes[0].Entries {
		var e measurementEntry
		if err := json.Unmarshal(raw, &e); err != nil {
			t.Fatalf("decode entry: %v", err)
		}
		if e.Channel == measurementChannelNo {
			byParam[e.Key] = e
		}
	}
	return out.Planes[0].State, out.Planes[0].Reason, byParam
}

// TestMeasurementProjectionIsWhatTheTargetReceives is the round trip of
// the measurements plane: for the parameters the simulated CCU reports,
// the projection calls published exactly those that arrive at the target,
// names them by the tags they arrive under, and gives the rule for the
// one that does not arrive.
func TestMeasurementProjectionIsWhatTheTargetReceives(t *testing.T) {
	t.Parallel()
	rig := bootForwardingRig(t, &harness.Measurements{Enabled: true, Exclude: []string{paramCurrent}})
	first := rig.emitFor(t, measurementWindow, paramPower, paramCurrent)
	arrived := rig.seen(t, first)

	state, _, entries := measurementEntries(t, rig)
	if state != "active" {
		t.Fatalf("plane state %q; want active", state)
	}
	var shown []string
	for _, p := range []string{paramPower, paramCurrent} {
		e, ok := entries[p]
		if !ok {
			t.Fatalf("the projection has no entry for %s on channel %d", p, measurementChannelNo)
		}
		if e.Verdict == "published" {
			shown = append(shown, p)
		}
	}
	slices.Sort(shown)
	if !slices.Equal(shown, arrived) {
		t.Fatalf("projection publishes %v, the target received %v", shown, arrived)
	}
	if len(arrived) == 0 {
		t.Fatal("nothing arrived at the target; the comparison would hold vacuously")
	}
	if e := entries[paramCurrent]; e.Verdict != "withheld" || e.Reason != "pattern_excludes" {
		t.Errorf("%s: %s / %q; want withheld / pattern_excludes", paramCurrent, e.Verdict, e.Reason)
	}

	// The identity is the tag set of the line the target received.
	id := entries[paramPower].Identity
	matched := false
	for _, s := range rig.target.samples(t) {
		if s.channel != rig.channel || s.parameter != paramPower {
			continue
		}
		matched = true
		if id["central"] != s.central || id["interface_id"] != s.iface || id["channel"] != s.channel || id["parameter"] != s.parameter {
			t.Errorf("identity %v does not name the received line (central=%s interface_id=%s channel=%s parameter=%s)",
				id, s.central, s.iface, s.channel, s.parameter)
		}
		break
	}
	if !matched {
		t.Fatalf("no %s line at the target to compare the identity with", paramPower)
	}
}

// With forwarding off, the plane says so.
func TestMeasurementProjectionOfADaemonThatForwardsNothingIsInactive(t *testing.T) {
	t.Parallel()
	rig := bootForwardingRig(t, nil)
	state, reason, entries := measurementEntries(t, rig)
	if state != "inactive" || reason != "plane_disabled" || len(entries) != 0 {
		t.Fatalf("plane = %s / %q with %d entries; want inactive / plane_disabled / none", state, reason, len(entries))
	}
}

// discoveryTopicSet collects the Home Assistant config topics the broker
// holds, following retractions: an empty payload removes the topic.
type discoveryTopicSet struct {
	mu     sync.Mutex
	topics map[string]bool
	last   time.Time
}

func watchDiscoveryTopics(t *testing.T, h *harness.Harness) *discoveryTopicSet {
	t.Helper()
	s := &discoveryTopicSet{topics: map[string]bool{}, last: time.Now()}
	if err := h.MQTT().Subscribe("homeassistant/+/+/+/config", func(topic string, payload []byte, _ bool) {
		s.mu.Lock()
		defer s.mu.Unlock()
		if len(payload) == 0 {
			delete(s.topics, topic)
		} else {
			s.topics[topic] = true
		}
		s.last = time.Now()
	}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	return s
}

// settled waits until no config topic has moved for quiet, and returns
// the set held then.
func (s *discoveryTopicSet) settled(t *testing.T, quiet, deadline time.Duration) map[string]bool {
	t.Helper()
	end := time.Now().Add(deadline)
	for {
		s.mu.Lock()
		idle, n := time.Since(s.last), len(s.topics)
		s.mu.Unlock()
		if n > 0 && idle >= quiet {
			break
		}
		if time.Now().After(end) {
			t.Fatalf("the discovery plane did not settle within %s (%d topics, idle %s)", deadline, n, idle)
		}
		time.Sleep(250 * time.Millisecond)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]bool, len(s.topics))
	for k := range s.topics {
		out[k] = true
	}
	return out
}

// nodeIDOf is the node id segment of a config topic
// (homeassistant/<component>/<node_id>/<object_id>/config).
func nodeIDOf(topic string) string {
	parts := strings.Split(topic, "/")
	if len(parts) != 5 {
		return ""
	}
	return parts[2]
}

// TestDiscoveryProjectionIsWhatTheBrokerHolds is the round trip of the
// Home Assistant discovery plane. The daemon boots with the CCU not
// ready, the CCU is flipped, and once the broker's config topics have
// settled the projections of all devices are compared with them:
//
//   - every entity a projection calls published is a config topic the
//     broker holds, under the topic the projection names;
//   - every config topic the broker holds under a device's node id is an
//     entity some projection calls published.
//
// Topics under a node id no projection names (the hub, the bridge's own
// entities) are not device entities and are left out of the second
// comparison.
func TestDiscoveryProjectionIsWhatTheBrokerHolds(t *testing.T) {
	t.Parallel()
	h := harness.Start(t, harness.Options{EnableMQTT: true, StartCCUNotReady: true, AuthMode: harness.AuthSession})
	if err := h.REST().LoginSession(harness.AdminUser, harness.AdminPass); err != nil {
		t.Fatalf("login: %v", err)
	}
	held := watchDiscoveryTopics(t, h)
	h.CCU().V().SetReady(true)
	awaitCentralReady(t, h, harness.CentralName)
	onBroker := held.settled(t, 5*time.Second, 90*time.Second)

	shown := map[string]bool{}
	nodeIDs := map[string]bool{}
	withheld := 0
	for _, raw := range getJSONArray(t, h, "/api/v1/devices", "items") {
		dev, _ := raw.(map[string]any)
		addr, _ := dev["address"].(string)
		status, out := getProjection(t, h, "/api/v1/devices/"+addr+"/projection?plane=ha_discovery")
		if status != http.StatusOK || len(out.Planes) != 1 {
			t.Fatalf("%s: status %d, planes %+v", addr, status, out.Planes)
		}
		if out.Planes[0].State != "active" {
			t.Fatalf("%s: plane %s / %q; want active", addr, out.Planes[0].State, out.Planes[0].Reason)
		}
		for _, rawEntry := range out.Planes[0].Entries {
			var e measurementEntry
			if err := json.Unmarshal(rawEntry, &e); err != nil {
				t.Fatalf("decode entry: %v", err)
			}
			switch e.Verdict {
			case "published":
				topic := e.Identity["discovery_topic"]
				if topic == "" {
					t.Fatalf("%s: published entry %d/%s without a discovery topic: %v", addr, e.Channel, e.Key, e.Identity)
				}
				shown[topic] = true
				nodeIDs[nodeIDOf(topic)] = true
			case "withheld":
				withheld++
				if e.Reason == "" {
					t.Errorf("%s: entry %d/%s is withheld without a reason", addr, e.Channel, e.Key)
				}
			}
		}
	}
	if len(shown) == 0 {
		t.Fatal("no projection calls anything published; the comparison would hold vacuously")
	}
	if withheld == 0 {
		t.Error("no projection withholds anything; the fleet does not exercise a single reason")
	}

	var missing, unexplained []string
	for topic := range shown {
		if !onBroker[topic] {
			missing = append(missing, topic)
		}
	}
	for topic := range onBroker {
		if nodeIDs[nodeIDOf(topic)] && !shown[topic] {
			unexplained = append(unexplained, topic)
		}
	}
	slices.Sort(missing)
	slices.Sort(unexplained)
	if len(missing) > 0 {
		t.Errorf("%d entities are called published and are not on the broker:\n  %s", len(missing), strings.Join(missing, "\n  "))
	}
	if len(unexplained) > 0 {
		t.Errorf("%d config topics of projected devices are on the broker and in no projection:\n  %s",
			len(unexplained), strings.Join(unexplained, "\n  "))
	}
	other := map[string]int{}
	for topic := range onBroker {
		if !nodeIDs[nodeIDOf(topic)] {
			other[nodeIDOf(topic)]++
		}
	}
	t.Logf("%d entities shown, %d config topics on the broker, %d withheld entries; node ids outside the projections: %v",
		len(shown), len(onBroker), withheld, other)
}

// TestRawProjectionIsWhatTheBrokerHolds is the round trip of the raw MQTT
// plane, in the shape of the discovery one: the status topics the broker
// holds for each device are compared with what the device's projection
// calls published, in both directions.
func TestRawProjectionIsWhatTheBrokerHolds(t *testing.T) {
	t.Parallel()
	h := harness.Start(t, harness.Options{EnableMQTT: true, StartCCUNotReady: true, AuthMode: harness.AuthSession})
	if err := h.REST().LoginSession(harness.AdminUser, harness.AdminPass); err != nil {
		t.Fatalf("login: %v", err)
	}
	held := &discoveryTopicSet{topics: map[string]bool{}, last: time.Now()}
	if err := h.MQTT().Subscribe("openccu-loom/status/"+harness.CentralName+"/#", func(topic string, payload []byte, _ bool) {
		held.mu.Lock()
		defer held.mu.Unlock()
		if len(payload) == 0 {
			delete(held.topics, topic)
		} else {
			held.topics[topic] = true
		}
		held.last = time.Now()
	}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	h.CCU().V().SetReady(true)
	awaitCentralReady(t, h, harness.CentralName)
	onBroker := held.settled(t, 5*time.Second, 90*time.Second)

	shown := map[string]bool{}
	devices := getJSONArray(t, h, "/api/v1/devices", "items")
	addrs := make([]string, 0, len(devices))
	withheld := 0
	for _, raw := range devices {
		dev, _ := raw.(map[string]any)
		addr, _ := dev["address"].(string)
		addrs = append(addrs, addr)
		status, out := getProjection(t, h, "/api/v1/devices/"+addr+"/projection?plane=mqtt_raw")
		if status != http.StatusOK || len(out.Planes) != 1 || out.Planes[0].State != "active" {
			t.Fatalf("%s: status %d, planes %+v", addr, status, out.Planes)
		}
		for _, rawEntry := range out.Planes[0].Entries {
			var e measurementEntry
			if err := json.Unmarshal(rawEntry, &e); err != nil {
				t.Fatalf("decode entry: %v", err)
			}
			switch e.Verdict {
			case "published":
				if e.Identity["topic"] == "" {
					t.Fatalf("%s: published entry %d/%s without a topic", addr, e.Channel, e.Key)
				}
				shown[e.Identity["topic"]] = true
			case "withheld":
				withheld++
				if e.Reason == "" {
					t.Errorf("%s: entry %d/%s is withheld without a reason", addr, e.Channel, e.Key)
				}
			}
		}
	}
	if len(shown) == 0 || withheld == 0 {
		t.Fatalf("%d shown, %d withheld; the comparison needs both", len(shown), withheld)
	}
	ofADevice := func(topic string) bool {
		for _, a := range addrs {
			if strings.Contains(topic, "/"+a+"/") {
				return true
			}
		}
		return false
	}
	var missing, unexplained []string
	for topic := range shown {
		if !onBroker[topic] {
			missing = append(missing, topic)
		}
	}
	for topic := range onBroker {
		if ofADevice(topic) && !shown[topic] {
			unexplained = append(unexplained, topic)
		}
	}
	slices.Sort(missing)
	slices.Sort(unexplained)
	if len(missing) > 0 {
		t.Errorf("%d topics are called published and are not on the broker:\n  %s", len(missing), strings.Join(missing, "\n  "))
	}
	if len(unexplained) > 0 {
		t.Errorf("%d status topics of the devices are on the broker and in no projection:\n  %s",
			len(unexplained), strings.Join(unexplained, "\n  "))
	}
	t.Logf("%d topics shown, %d status topics on the broker, %d withheld entries", len(shown), len(onBroker), withheld)
}
