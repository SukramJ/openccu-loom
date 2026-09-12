// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

//go:build integration

package integration

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"testing"

	hacatalog "github.com/SukramJ/go-ha-catalog"
	hadiscovery "github.com/SukramJ/go-hamqtt/discovery"
)

// fleetSnapshot is the captured discovery surface of a real installation.
type fleetSnapshot struct {
	CapturedAt string `json:"captured_at"`
	Entities   []struct {
		Topic     string         `json:"discovery_topic"`
		Component string         `json:"component"`
		UniqueID  string         `json:"unique_id"`
		Payload   map[string]any `json:"payload"`
	} `json:"entities"`
}

// TestFleetPayloadsSatisfyHomeAssistantsSchema checks every discovery payload
// of a captured fleet against the schemas extracted from Home Assistant
// itself.
//
// It exists because of the one property that makes these defects expensive:
// Home Assistant's MQTT schemas are extra=REMOVE_EXTRA. A key a platform does
// not declare is dropped on arrival — no error on the wire, no line in any
// log, and an entity that works except for the one thing that key was for.
// Nothing else in this repository would notice.
//
// The captured fleet is ~9,996 entities over ~400 devices, and it is the only
// corpus here that covers the long tail: the device with one odd parameter,
// the channel type nobody wrote a unit test for. The first run of this check
// over it found a device class Home Assistant was discarding on every
// read-only CUxD switch channel.
//
// translation_key is excused, deliberately and in one place. This daemon
// publishes it so the cross-stack parity tooling can compare against the
// Python integration it mirrors; Home Assistant declares it on no platform
// and drops it. The validator is right to report it, which is why the
// exception lives here rather than in the validator — a tool that did not
// make this daemon's judgement should still flag it.
func TestFleetPayloadsSatisfyHomeAssistantsSchema(t *testing.T) {
	snap := loadFleetSnapshot(t)
	if len(snap.Entities) == 0 {
		t.Fatal("the snapshot exists but holds no entities; every assertion below would pass vacuously")
	}
	t.Logf("checking %d entities captured at %s", len(snap.Entities), snap.CapturedAt)

	// See the doc comment. A named set, so adding a second exception is a
	// deliberate act with a reason beside it rather than a quiet append.
	excused := map[string]bool{
		"translation_key": true,
	}

	type finding struct{ entity, detail string }
	var blocking, advisory []finding

	for _, e := range snap.Entities {
		if e.Component == "" || len(e.Payload) == 0 {
			continue
		}
		err := hadiscovery.ValidateBodyIgnoring(hacatalog.Platform(e.Component), e.Payload, excused)
		if err == nil {
			continue
		}
		var ve *hadiscovery.ValidationError
		if !errors.As(err, &ve) {
			blocking = append(blocking, finding{e.UniqueID, err.Error()})
			continue
		}
		for _, issue := range ve.Issues {
			blocking = append(blocking, finding{e.UniqueID, issue})
		}
		for _, warning := range ve.Warnings {
			advisory = append(advisory, finding{e.UniqueID, warning})
		}
	}

	// Advisories are values Home Assistant accepts and then rewrites — the
	// legacy micro sign, say. Worth seeing, not a failure.
	if len(advisory) > 0 {
		t.Logf("%d advisory finding(s); first: %s: %s",
			len(advisory), advisory[0].entity, advisory[0].detail)
	}
	if len(blocking) == 0 {
		return
	}

	// Grouped, so a failure names the shape of the problem instead of
	// scrolling ten thousand lines of the same sentence.
	byDetail := map[string][]string{}
	for _, f := range blocking {
		byDetail[f.detail] = append(byDetail[f.detail], f.entity)
	}
	details := make([]string, 0, len(byDetail))
	for d := range byDetail {
		details = append(details, d)
	}
	sort.Strings(details)

	t.Errorf("%d payload(s) carry something Home Assistant would reject or strip, in %d distinct shapes",
		len(blocking), len(details))
	for _, d := range details {
		entities := byDetail[d]
		sort.Strings(entities)
		shown := entities
		if len(shown) > 3 {
			shown = shown[:3]
		}
		t.Errorf("  %d× %s\n      e.g. %v", len(entities), d, shown)
	}
}

func loadFleetSnapshot(t *testing.T) fleetSnapshot {
	t.Helper()
	path := filepath.Join("testdata", "discovery_snapshot_openccu-loom.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		// The capture is a local artefact — gitignored, ~24 MB, written by
		// TestDiscoverySnapshotDumpAgainstGodevccu. Skipping rather than
		// failing is the convention its sibling tests already follow: a
		// machine that has not produced the capture cannot say anything
		// about it, and pretending otherwise would turn "nobody generated
		// it" into "the fleet is broken".
		t.Skipf("openccu-loom snapshot not available (%v); run TestDiscoverySnapshotDumpAgainstGodevccu first", err)
	}
	var snap fleetSnapshot
	if err := json.Unmarshal(raw, &snap); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return snap
}
