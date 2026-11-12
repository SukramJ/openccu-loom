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
	"testing"

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
	if len(out.Planes) != 2 || out.Planes[0].Plane != "matter" || out.Planes[1].Plane != "measurements" {
		t.Fatalf("planes = %+v; want matter, then measurements", out.Planes)
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
