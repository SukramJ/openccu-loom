// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

//go:build e2e

package e2e

import (
	"bytes"
	"context"
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

// The tests in this file pin one behaviour of the MQTT planes: a central that
// is torn down and loaded again under the same name (a restart after an edit
// of the central row, the cache-reset re-pull) must not retract the retained
// discovery configs of what it loads again. A retracted config deletes the
// entity in Home Assistant together with its registry entry, so a renamed
// entity id falls back to the default and a helper built on the entity is
// deleted with it. What the reloaded model no longer declares is retracted
// once the model is whole again, and a removal of the central retracts
// everything that is still held.
//
// Every "nothing is retracted" assertion has its negative control in the same
// run, on the same subscription: a later step that must produce retractions.
// A retraction is an empty payload on a topic whose last payload was not
// empty; the first publish of an empty value onto a topic that never carried
// one (an unset week profile) is a state, not a retraction.

const (
	// holdNodePrefix is the node-id prefix of every discovery config the
	// adopted central owns (the discovery slug of its name plus "_").
	holdNodePrefix = adoptedCentralName + "_"
	// holdRawSegment locates the central in a raw-plane topic
	// (`<base>/<plane>/<central>/...`).
	holdRawSegment = "/" + adoptedCentralName + "/"
	// holdQuiet is the window a "nothing happens" assertion keeps watching
	// after the restarted central has republished everything it holds.
	holdQuiet = 4 * time.Second
	// holdBudget bounds every wait for an observable effect.
	holdBudget = 90 * time.Second
)

// holdFrame is one frame the broker saw.
type holdFrame struct {
	topic   string
	payload string
}

// holdRecorder records every frame under the discovery and the raw prefix and
// derives the retractions among them.
type holdRecorder struct {
	mu          sync.Mutex
	frames      []holdFrame
	last        map[string]string
	retractions []int // indexes into frames
}

func newHoldRecorder(t *testing.T, h *harness.Harness) *holdRecorder {
	t.Helper()
	r := &holdRecorder{last: map[string]string{}}
	for _, filter := range []string{"homeassistant/#", "openccu-loom/#"} {
		if err := h.MQTT().Subscribe(filter, r.record); err != nil {
			t.Fatalf("subscribe %s: %v", filter, err)
		}
	}
	return r
}

func (r *holdRecorder) record(topic string, payload []byte, _ bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	p := string(payload)
	if p == "" && r.last[topic] != "" {
		r.retractions = append(r.retractions, len(r.frames))
	}
	r.last[topic] = p
	r.frames = append(r.frames, holdFrame{topic: topic, payload: p})
}

// mark returns the position after the frames seen so far.
func (r *holdRecorder) mark() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.frames)
}

// retractedSince lists the topics retracted after mark that match keep.
func (r *holdRecorder) retractedSince(mark int, keep func(topic string) bool) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, i := range r.retractions {
		if i >= mark && keep(r.frames[i].topic) {
			out = append(out, r.frames[i].topic)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// retained lists the topics that currently carry a non-empty payload and match
// keep.
func (r *holdRecorder) retained(keep func(topic string) bool) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for topic, payload := range r.last {
		if payload != "" && keep(topic) {
			out = append(out, topic)
		}
	}
	slices.Sort(out)
	return out
}

// publishedSince reports whether a non-empty payload matching ok arrived on a
// topic matching keep after mark.
func (r *holdRecorder) publishedSince(mark int, keep func(topic string) bool, ok func(payload string) bool) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, f := range r.frames[mark:] {
		if f.payload != "" && keep(f.topic) && ok(f.payload) {
			return true
		}
	}
	return false
}

// isCentralConfig reports whether topic is a discovery config of the adopted
// central: `homeassistant/<component>/<node>/<object>/config`.
func isCentralConfig(topic string) bool {
	parts := strings.Split(topic, "/")
	return len(parts) == 5 && parts[0] == "homeassistant" && parts[4] == "config" &&
		strings.HasPrefix(parts[2], holdNodePrefix)
}

// isCentralRaw reports whether topic belongs to the adopted central's raw
// plane (`openccu-loom/<plane>/<central>/...`).
func isCentralRaw(topic string) bool {
	return strings.HasPrefix(topic, "openccu-loom/") && strings.Contains(topic, holdRawSegment)
}

func isCentralTopic(topic string) bool { return isCentralConfig(topic) || isCentralRaw(topic) }

// configNode and configObject split a central config topic.
func configNode(topic string) string   { return strings.Split(topic, "/")[2] }
func configObject(topic string) string { return strings.Split(topic, "/")[3] }

// deviceNode is the discovery node id of one device of the adopted central.
func deviceNode(address string) string { return holdNodePrefix + strings.ToLower(address) }

// holdDevice is one device of the adopted central as the REST API lists it.
type holdDevice struct {
	Address   string `json:"address"`
	Interface string `json:"interface"`
	Model     string `json:"model"`
}

// holdFixture is a daemon with MQTT, a second simulated CCU and the adopted
// central on it, settled.
type holdFixture struct {
	t     *testing.T
	h     *harness.Harness
	ccu   *harness.MockCCU
	rec   *holdRecorder
	row   map[string]any
	ifs   []string
	fleet []holdDevice
	ports map[string]int
}

// newHoldFixture adopts a central over the REST API the way the operator does.
// With interfaces it runs one interface process per name on the simulator and
// lists each in the central row; without, the central uses the single HmIP-RF
// endpoint.
func newHoldFixture(t *testing.T, devices, interfaces []string) *holdFixture {
	t.Helper()
	h := harness.Start(t, harness.Options{EnableMQTT: true})
	if h.MQTT() == nil {
		t.Fatal("MQTT broker not started")
	}
	if err := h.REST().LoginSession(harness.AdminUser, harness.AdminPass); err != nil {
		t.Fatalf("login: %v", err)
	}
	f := &holdFixture{t: t, h: h, rec: newHoldRecorder(t, h), ports: map[string]int{}}
	if len(interfaces) > 0 {
		f.ccu = harness.StartCCUWithInterfaces(t, devices, interfaces)
		f.ifs = interfaces
	} else {
		f.ccu = harness.StartCCU(t, devices)
		f.ifs = []string{"HmIP-RF"}
	}
	specs := make([]map[string]any, 0, len(f.ifs))
	for _, name := range f.ifs {
		spec := map[string]any{"name": name}
		if len(interfaces) > 0 {
			spec["port"] = f.ccu.InterfacePort(name)
		}
		specs = append(specs, spec)
	}
	f.row = map[string]any{
		"name":          adoptedCentralName,
		"host":          "127.0.0.1",
		"port":          f.ccu.XMLRPCPort(),
		"json_rpc_port": f.ccu.JSONRPCPort(),
		"username":      "Admin",
		"interfaces":    specs,
		"enabled":       true,
	}
	if status, body := f.do(http.MethodPost, "/api/v1/centrals", f.row); status != http.StatusCreated {
		t.Fatalf("POST /api/v1/centrals: status %d, body %s", status, body)
	}
	f.awaitReady("the adopted central never became ready")
	f.fleet = nil
	f.await("the adopted central listing its devices", func() bool {
		f.fleet = f.devices()
		return len(f.fleet) > 0
	})
	f.awaitSettled(0, f.fleet, true)
	return f
}

// do performs one authenticated REST call and returns status and body.
func (f *holdFixture) do(method, path string, body any) (status int, respBody []byte) {
	f.t.Helper()
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			f.t.Fatalf("marshal %s body: %v", path, err)
		}
		rd = bytes.NewReader(b)
	}
	req, err := f.h.REST().NewRequest(method, path, rd)
	if err != nil {
		f.t.Fatalf("build %s %s: %v", method, path, err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := f.h.REST().Do(req)
	if err != nil {
		f.t.Fatalf("%s %s: %v", method, path, err)
	}
	b, readErr := io.ReadAll(resp.Body)
	if closeErr := resp.Body.Close(); readErr != nil || closeErr != nil {
		f.t.Fatalf("%s %s: read body: %v / %v", method, path, readErr, closeErr)
	}
	return resp.StatusCode, b
}

// put sends the central row, as changed by mutate, to the live central.
func (f *holdFixture) put(mutate func(row map[string]any)) {
	f.t.Helper()
	mutate(f.row)
	if status, body := f.do(http.MethodPut, "/api/v1/centrals/"+adoptedCentralName, f.row); status != http.StatusOK && status != http.StatusNoContent {
		f.t.Fatalf("PUT central: status %d, body %s", status, body)
	}
}

// toggleTLSSkip flips a restart-relevant field that leaves the fleet
// alone: the central is reached the same way, but the row differs.
func (f *holdFixture) toggleTLSSkip(v bool) func(map[string]any) {
	return func(row map[string]any) { row["tls_insecure_skip_verify"] = v }
}

// dropInterface returns a mutation that removes one interface from the row.
func dropInterface(name string) func(map[string]any) {
	return func(row map[string]any) {
		specs, _ := row["interfaces"].([]map[string]any)
		kept := make([]map[string]any, 0, len(specs))
		for _, s := range specs {
			if s["name"] != name {
				kept = append(kept, s)
			}
		}
		row["interfaces"] = kept
	}
}

// ready reports the readiness the daemon itself serves for the central.
func (f *holdFixture) ready() bool {
	status, body := f.do(http.MethodGet, "/api/v1/system/ccu", nil)
	if status != http.StatusOK {
		return false
	}
	var resp struct {
		Entries []struct {
			Name      string `json:"name"`
			Readiness struct {
				Ready bool `json:"ready"`
			} `json:"readiness"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		f.t.Fatalf("decode /system/ccu: %v", err)
	}
	for _, e := range resp.Entries {
		if e.Name == adoptedCentralName {
			return e.Readiness.Ready
		}
	}
	return false
}

// devices lists the adopted central's devices from the REST API.
func (f *holdFixture) devices() []holdDevice {
	f.t.Helper()
	status, body := f.do(http.MethodGet, "/api/v1/devices?central="+adoptedCentralName, nil)
	if status != http.StatusOK {
		f.t.Fatalf("GET devices: status %d, body %s", status, body)
	}
	var resp struct {
		Items []holdDevice `json:"items"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		f.t.Fatalf("decode devices: %v", err)
	}
	return resp.Items
}

// await polls cond until it holds or the budget runs out.
func (f *holdFixture) await(what string, cond func() bool) {
	f.t.Helper()
	deadline := time.Now().Add(holdBudget)
	for !cond() {
		if time.Now().After(deadline) {
			f.t.Fatalf("timed out after %s waiting for: %s", holdBudget, what)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func (f *holdFixture) awaitReady(what string) {
	f.t.Helper()
	f.await(what, f.ready)
}

// onlineTopic matches the raw availability topic of one device of the central.
func onlineTopic(address string) func(string) bool {
	suffix := "/" + address + "/online"
	return func(topic string) bool { return isCentralRaw(topic) && strings.HasSuffix(topic, suffix) }
}

func payloadTrue(p string) bool { return strings.Contains(p, `"val":true`) }

// awaitSettled waits until, after mark, every device the REST API lists has
// published its availability as true again and the central's own gate is
// true (hubGate; a cache-reset re-pull leaves the hub plane alone, so it does
// not wait for it): the snapshot of the restarted model is out. It then keeps watching for
// holdQuiet so a reconcile that runs behind the snapshot would be inside the
// observed window.
func (f *holdFixture) awaitSettled(mark int, devs []holdDevice, hubGate bool) {
	f.t.Helper()
	for _, d := range devs {
		f.await("availability of "+d.Address+" republished as true",
			func() bool { return f.rec.publishedSince(mark, onlineTopic(d.Address), payloadTrue) })
	}
	if hubGate {
		f.await("the central's availability gate published as true", func() bool {
			return f.rec.publishedSince(mark, func(topic string) bool {
				return topic == "openccu-loom/status/"+adoptedCentralName+"/online"
			}, payloadTrue)
		})
	}
	time.Sleep(holdQuiet)
}

// retainedConfigs lists the central's retained discovery configs.
func (f *holdFixture) retainedConfigs() []string { return f.rec.retained(isCentralConfig) }

// configsOfDevices selects, from topics, the configs of the given devices.
func configsOfDevices(topics []string, devs []holdDevice) []string {
	nodes := map[string]bool{}
	for _, d := range devs {
		nodes[deviceNode(d.Address)] = true
	}
	var out []string
	for _, topic := range topics {
		if isCentralConfig(topic) && nodes[configNode(topic)] {
			out = append(out, topic)
		}
	}
	return out
}

// requireNone fails when topics is not empty.
func requireNone(t *testing.T, what string, topics []string) {
	t.Helper()
	if len(topics) > 0 {
		t.Fatalf("%s: %d topics were retracted, want none:\n  %s", what, len(topics), strings.Join(topics, "\n  "))
	}
}

// requireSameSet fails when got and want differ, naming the difference.
func requireSameSet(t *testing.T, what string, got, want []string) {
	t.Helper()
	got = slices.Compact(slices.Sorted(slices.Values(got)))
	want = slices.Compact(slices.Sorted(slices.Values(want)))
	var missing, extra []string
	for _, w := range want {
		if !slices.Contains(got, w) {
			missing = append(missing, w)
		}
	}
	for _, g := range got {
		if !slices.Contains(want, g) {
			extra = append(extra, g)
		}
	}
	if len(missing) > 0 || len(extra) > 0 {
		t.Fatalf("%s: retracted set differs from the expected one\n  missing (%d):\n    %s\n  unexpected (%d):\n    %s",
			what, len(missing), strings.Join(missing, "\n    "), len(extra), strings.Join(extra, "\n    "))
	}
}

// awaitRetracted waits until every topic of want was retracted after mark.
func (f *holdFixture) awaitRetracted(mark int, what string, want []string, keep func(string) bool) {
	f.t.Helper()
	f.await(what, func() bool {
		got := f.rec.retractedSince(mark, keep)
		for _, w := range want {
			if !slices.Contains(got, w) {
				return false
			}
		}
		return true
	})
}

// droppedInterfaceSet derives the configs a drop of one interface must
// retract: those of its devices, its connectivity sensor and its install-mode
// entities. Each part must be non-empty, so a derivation that matched nothing
// fails instead of weakening the assertion.
func droppedInterfaceSet(t *testing.T, retained []string, devs []holdDevice, iface string) []string {
	t.Helper()
	var dropped []holdDevice
	for _, d := range devs {
		if d.Interface == iface {
			dropped = append(dropped, d)
		}
	}
	if len(dropped) == 0 {
		t.Fatalf("no device on interface %s", iface)
	}
	set := configsOfDevices(retained, dropped)
	if len(set) == 0 {
		t.Fatalf("no retained config for the devices of %s", iface)
	}
	ifSlug := strings.ToLower(iface)
	installPrefix := "install_mode_" + strings.ToLower(strings.SplitN(iface, "-", 2)[0])
	var connectivity, installMode int
	for _, topic := range retained {
		switch configNode(topic) {
		case holdNodePrefix + "connectivity":
			if strings.Contains(configObject(topic), ifSlug) {
				set = append(set, topic)
				connectivity++
			}
		case holdNodePrefix + "central":
			if strings.HasPrefix(configObject(topic), installPrefix) {
				set = append(set, topic)
				installMode++
			}
		}
	}
	if connectivity == 0 || installMode == 0 {
		t.Fatalf("hub configs of %s not found among the retained ones (connectivity %d, install mode %d)", iface, connectivity, installMode)
	}
	return set
}

// removeCentral deletes the central the way the operator does.
func (f *holdFixture) removeCentral() {
	f.t.Helper()
	if status, body := f.do(http.MethodDelete, "/api/v1/centrals/"+adoptedCentralName, nil); status != http.StatusNoContent && status != http.StatusOK {
		f.t.Fatalf("DELETE central: status %d, body %s", status, body)
	}
}

var twoInterfaceFleet = []string{"HmIP-BSM", "HmIP-BROLL", "HM-LC-Sw1-Pl"}

// TestE2ECentralRestartWithUnchangedFleetRetractsNoDiscoveryConfig restarts a
// central whose fleet stays as it is and asserts that no discovery config of
// it is retracted, before or after the restarted central published its
// snapshot. The control removes the central afterwards: the same subscription
// must then see the configs retracted.
func TestE2ECentralRestartWithUnchangedFleetRetractsNoDiscoveryConfig(t *testing.T) {
	t.Parallel()
	f := newHoldFixture(t, nil, nil)
	before := f.retainedConfigs()
	if len(before) == 0 {
		t.Fatal("the central retains no discovery config; the test measures nothing")
	}

	mark := f.rec.mark()
	f.put(f.toggleTLSSkip(true))
	f.awaitReady("the restarted central never became ready")
	f.awaitSettled(mark, f.fleet, true)

	requireNone(t, "restart with an unchanged fleet", f.rec.retractedSince(mark, isCentralConfig))
	requireSameSet(t, "retained configs after the restart", f.retainedConfigs(), before)

	// Control: a removal retracts, on the same subscription.
	mark = f.rec.mark()
	f.removeCentral()
	f.awaitRetracted(mark, "the removal retracting the central's configs", before, isCentralConfig)
}

// TestE2ECentralRestartRetractsExactlyTheDroppedInterface drops one of two
// interfaces by editing the central row. The configs retracted once the model
// is whole again are exactly those of that interface's devices plus its
// connectivity sensor and install-mode entities.
func TestE2ECentralRestartRetractsExactlyTheDroppedInterface(t *testing.T) {
	t.Parallel()
	f := newHoldFixture(t, twoInterfaceFleet, []string{"HmIP-RF", "BidCos-RF"})
	retained := f.retainedConfigs()
	want := droppedInterfaceSet(t, retained, f.fleet, "BidCos-RF")

	mark := f.rec.mark()
	f.put(dropInterface("BidCos-RF"))
	f.awaitReady("the restarted central never became ready")
	f.awaitRetracted(mark, "the dropped interface's configs being retracted", want, isCentralConfig)
	time.Sleep(holdQuiet)

	requireSameSet(t, "drop of BidCos-RF", f.rec.retractedSince(mark, isCentralConfig), want)
}

// TestE2ECentralRestartAgainstANotReadyCCURetractsNothingUntilItIsReady
// restarts a central while its CCU is not ready, with an interface dropped.
// Nothing is retracted for as long as the CCU cannot answer; once it is ready
// the dropped interface's set is retracted, and only that.
func TestE2ECentralRestartAgainstANotReadyCCURetractsNothingUntilItIsReady(t *testing.T) {
	t.Parallel()
	f := newHoldFixture(t, twoInterfaceFleet, []string{"HmIP-RF", "BidCos-RF"})
	want := droppedInterfaceSet(t, f.retainedConfigs(), f.fleet, "BidCos-RF")

	mark := f.rec.mark()
	f.ccu.V().SetReady(false)
	f.put(dropInterface("BidCos-RF"))
	time.Sleep(2 * holdQuiet)
	if f.ready() {
		t.Fatal("the central reports ready while its CCU is not; the wait did not happen")
	}
	requireNone(t, "restart against a CCU that is not ready", f.rec.retractedSince(mark, isCentralTopic))

	f.ccu.V().SetReady(true)
	f.awaitReady("the restarted central never became ready")
	f.awaitRetracted(mark, "the dropped interface's configs being retracted", want, isCentralConfig)
	time.Sleep(holdQuiet)
	requireSameSet(t, "drop of BidCos-RF after the CCU became ready", f.rec.retractedSince(mark, isCentralConfig), want)
}

// TestE2ECacheResetRepullRetractsNoConfigOfAnUnchangedDevice clears the cache
// of a central: the re-pull finds the same devices and retracts no config. The
// control removes one device on the simulator without telling the daemon and
// clears again: exactly that device's configs are then retracted.
func TestE2ECacheResetRepullRetractsNoConfigOfAnUnchangedDevice(t *testing.T) {
	t.Parallel()
	f := newHoldFixture(t, []string{"HmIP-BSM", "HmIP-BROLL"}, nil)
	clearCache := func() {
		status, body := f.do(http.MethodPost, "/api/v1/admin/cache/clear",
			map[string]string{"kind": "central", "central": adoptedCentralName})
		if status != http.StatusOK {
			t.Fatalf("cache clear: status %d, body %s", status, body)
		}
	}
	before := f.retainedConfigs()
	if len(before) == 0 {
		t.Fatal("the central retains no discovery config; the test measures nothing")
	}

	mark := f.rec.mark()
	clearCache()
	f.awaitSettled(mark, f.fleet, false)
	requireNone(t, "cache-reset re-pull of an unchanged fleet", f.rec.retractedSince(mark, isCentralConfig))
	requireSameSet(t, "retained configs after the re-pull", f.retainedConfigs(), before)

	// Control: the CCU loses a device; the push to the daemon is not sent (the
	// context is cancelled), so only the re-pull can find out.
	var gone holdDevice
	for _, d := range f.fleet {
		if d.Model == "HmIP-BROLL" {
			gone = d
		}
	}
	if gone.Address == "" {
		t.Fatal("no HmIP-BROLL among the devices")
	}
	want := configsOfDevices(f.retainedConfigs(), []holdDevice{gone})
	if len(want) == 0 {
		t.Fatal("the removed device has no retained config")
	}
	silent, cancel := context.WithCancel(context.Background())
	cancel()
	f.ccu.V().RPC().RemoveDevices(silent, []string{gone.Model})

	mark = f.rec.mark()
	clearCache()
	f.awaitRetracted(mark, "the re-pull retracting the removed device's configs", want, isCentralConfig)
	time.Sleep(holdQuiet)
	requireSameSet(t, "re-pull after a device was removed", f.rec.retractedSince(mark, isCentralConfig), want)
}

// TestE2ECentralRestartKeepsTheRawPlaneOfUnchangedDevices restarts a central
// with an unchanged fleet and asserts that no raw-plane topic of it is
// emptied: a consumer of the raw plane keeps its last value and the
// availability gate goes false, then true. The control removes the central.
func TestE2ECentralRestartKeepsTheRawPlaneOfUnchangedDevices(t *testing.T) {
	t.Parallel()
	f := newHoldFixture(t, nil, nil)
	before := f.rec.retained(isCentralRaw)
	if len(before) == 0 {
		t.Fatal("the central retains no raw topic; the test measures nothing")
	}

	mark := f.rec.mark()
	f.put(f.toggleTLSSkip(true))
	f.awaitReady("the restarted central never became ready")
	f.awaitSettled(mark, f.fleet, true)

	requireNone(t, "restart with an unchanged fleet (raw plane)", f.rec.retractedSince(mark, isCentralRaw))
	for _, topic := range before {
		if !slices.Contains(f.rec.retained(isCentralRaw), topic) {
			t.Fatalf("raw topic %s no longer carries a value after the restart", topic)
		}
	}

	// Control: a removal empties raw topics, on the same subscription.
	mark = f.rec.mark()
	f.removeCentral()
	f.await("the removal emptying raw topics of the central", func() bool {
		return len(f.rec.retractedSince(mark, isCentralRaw)) > 0
	})
}

// TestE2ERemovingAHeldCentralRetractsEverythingItHeld restarts a central
// against a CCU that is not ready, so its configs stay held, and then removes
// it: every config of it, device and hub plane, is retracted.
func TestE2ERemovingAHeldCentralRetractsEverythingItHeld(t *testing.T) {
	t.Parallel()
	f := newHoldFixture(t, nil, nil)
	before := f.retainedConfigs()
	var hub int
	for _, topic := range before {
		if configNode(topic) == holdNodePrefix+"programs" || configNode(topic) == holdNodePrefix+"sysvars" {
			hub++
		}
	}
	if hub == 0 {
		t.Fatal("the central retains no hub config; the hub plane is not measured")
	}

	mark := f.rec.mark()
	f.ccu.V().SetReady(false)
	f.put(f.toggleTLSSkip(true))
	time.Sleep(2 * holdQuiet)
	if f.ready() {
		t.Fatal("the central reports ready while its CCU is not; the wait did not happen")
	}
	requireNone(t, "restart against a CCU that is not ready", f.rec.retractedSince(mark, isCentralTopic))

	f.removeCentral()
	f.awaitRetracted(mark, "the removal retracting everything the restart held", before, isCentralConfig)
	requireNone(t, "configs still retained after the removal",
		f.retainedConfigs())
}

// TestE2EASecondRestartKeepsTheFirstBaseline restarts a central twice against
// a CCU that is not ready and lets it come up: the outcome is that of one
// restart with an unchanged fleet, nothing retracted.
func TestE2EASecondRestartKeepsTheFirstBaseline(t *testing.T) {
	t.Parallel()
	f := newHoldFixture(t, nil, nil)
	before := f.retainedConfigs()
	if len(before) == 0 {
		t.Fatal("the central retains no discovery config; the test measures nothing")
	}

	mark := f.rec.mark()
	f.ccu.V().SetReady(false)
	f.put(f.toggleTLSSkip(true))
	f.put(f.toggleTLSSkip(false))
	time.Sleep(2 * holdQuiet)
	if f.ready() {
		t.Fatal("the central reports ready while its CCU is not; the wait did not happen")
	}
	requireNone(t, "two restarts against a CCU that is not ready", f.rec.retractedSince(mark, isCentralTopic))

	f.ccu.V().SetReady(true)
	f.awaitReady("the restarted central never became ready")
	f.awaitSettled(mark, f.fleet, true)
	requireNone(t, "second restart after the CCU became ready", f.rec.retractedSince(mark, isCentralConfig))
	requireSameSet(t, "retained configs after two restarts", f.retainedConfigs(), before)

	// Control: removal retracts on the same subscription.
	mark = f.rec.mark()
	f.removeCentral()
	f.awaitRetracted(mark, "the removal retracting the central's configs", before, isCentralConfig)
}
