// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package schema_test

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/schema"
)

// loomUsedClusters is the set of Matter cluster IDs OpenCCU-Loom advertises on
// its root, aggregator, or bridged endpoints. The staleness guard below only
// diffs these against a live matter.js checkout — a revision bump on a cluster
// the bridge never exposes is not actionable for us. The application entries
// mirror the cluster sets each internal/model/custom/<cat> projection emits
// (see that package's matter.go MatterClusterServers); the infrastructure
// entries are the root/aggregator/bridged-node clusters wired in
// internal/north/matter/cluster/core and internal/north/matter/endpoint.
var loomUsedClusters = map[uint32]string{
	// Root / Aggregator / BridgedNode infrastructure.
	0x0003: "Identify",
	0x0004: "Groups",
	0x001D: "Descriptor",
	0x001E: "Binding",
	0x001F: "AccessControl",
	0x0028: "BasicInformation",
	0x002A: "OtaSoftwareUpdateRequestor",
	0x0030: "GeneralCommissioning",
	0x0031: "NetworkCommissioning",
	0x0032: "DiagnosticLogs",
	0x0033: "GeneralDiagnostics",
	0x0038: "TimeSynchronization",
	0x0039: "BridgedDeviceBasicInformation",
	0x003B: "Switch",
	0x003C: "AdministratorCommissioning",
	0x003E: "OperationalCredentials",
	0x003F: "GroupKeyManagement",
	0x0046: "IcdManagement",
	0x0062: "ScenesManagement",

	// Application-cluster projections (internal/model/custom/<cat>).
	0x0006: "OnOff",                                // switch, light, siren
	0x0008: "LevelControl",                         // dimmable light
	0x002F: "PowerSource",                          // switch (metering)
	0x0045: "BooleanState",                         // contact / boolean sensors
	0x005C: "SmokeCoAlarm",                         // siren (smoke)
	0x0090: "ElectricalPowerMeasurement",           // switch (metering)
	0x0091: "ElectricalEnergyMeasurement",          // switch (metering)
	0x0101: "DoorLock",                             // lock
	0x0102: "WindowCovering",                       // cover / blind / garage
	0x0201: "Thermostat",                           // climate
	0x0204: "ThermostatUserInterfaceConfiguration", // climate
	0x0300: "ColorControl",                         // color light
	0x0400: "IlluminanceMeasurement",               // light sensor
	0x0402: "TemperatureMeasurement",               // temperature sensor / climate
	0x0403: "PressureMeasurement",                  // pressure sensor
	0x0405: "RelativeHumidityMeasurement",          // humidity sensor / climate
	0x0406: "OccupancySensing",                     // occupancy sensor
}

// matterJSElementsDir returns the standard-elements directory of a sibling
// matter.js checkout (../matter.js relative to the repo root) and whether it
// exists. The extractor at notes/parity/matter/extract-from-matter-js.ts walks
// the same MatterDefinition tree these files build.
func matterJSElementsDir() (string, bool) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		return "", false
	}
	// This file lives at internal/north/matter/schema/staleness_test.go, so the
	// repo root is four directories up and the checkout is its ../ sibling.
	repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..", "..", "..")
	dir := filepath.Join(repoRoot, "..", "matter.js", "packages", "model", "src", "standard", "elements")
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return "", false
	}
	return dir, true
}

var (
	// reClusterHeader captures the cluster name + id from the element header,
	// e.g. `= Cluster(\n    { name: "OnOff", id: 0x6, ...`.
	reClusterHeader = regexp.MustCompile(`Cluster\(\s*\{\s*name:\s*"([^"]+)",\s*id:\s*(0x[0-9a-fA-F]+)`)
	// reClusterRevision captures the ClusterRevision (0xFFFD) default, e.g.
	// `Attribute({ name: "ClusterRevision", id: 0xfffd, type: "ClusterRevision", default: 6 })`.
	reClusterRevision = regexp.MustCompile(`name:\s*"ClusterRevision",\s*id:\s*0xfffd[^}]*?default:\s*(\d+)`)
)

// parseLiveClusterRevisions reads every *.element.ts in dir and returns the
// live ClusterRevision default per cluster ID. Type-inheriting clusters that
// declare no inline ClusterRevision (a handful in the ConcentrationMeasurement /
// ResourceMonitoring families) are skipped — none are in loomUsedClusters, so
// the staleness diff is unaffected.
func parseLiveClusterRevisions(dir string) (map[uint32]uint16, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	live := make(map[uint32]uint16, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".element.ts") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		hdr := reClusterHeader.FindSubmatch(raw)
		if hdr == nil {
			continue // not a cluster element (datatype, device type, ...)
		}
		id64, err := strconv.ParseUint(string(hdr[2]), 0, 32)
		if err != nil {
			continue
		}
		rev := reClusterRevision.FindSubmatch(raw)
		if rev == nil {
			continue // revision inherited from a base type — not inline here
		}
		r64, err := strconv.ParseUint(string(rev[1]), 10, 16)
		if err != nil {
			continue
		}
		live[uint32(id64)] = uint16(r64)
	}
	return live, nil
}

// pinnedSnapshotProvenance reads the matter block of the master snapshot so the
// staleness report can name the pinned spec revision + matter.js source commit.
func pinnedSnapshotProvenance() (revision, commit string) {
	revision, commit = "unknown", "unknown"
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		return revision, commit
	}
	repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..", "..", "..")
	raw, err := os.ReadFile(filepath.Join(repoRoot, "notes", "parity", "matter", "matter-schema-snapshot.json"))
	if err != nil {
		return revision, commit
	}
	var s struct {
		Matter struct {
			Revision     string `json:"revision"`
			SourceCommit string `json:"sourceCommit"`
		} `json:"matter"`
	}
	if err := json.Unmarshal(raw, &s); err != nil {
		return revision, commit
	}
	if s.Matter.Revision != "" {
		revision = s.Matter.Revision
	}
	if s.Matter.SourceCommit != "" {
		commit = s.Matter.SourceCommit
	}
	return revision, commit
}

// gitHead returns the HEAD commit of the checkout containing dir, or "unknown".
func gitHead(dir string) string {
	out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(out))
}

func shortCommit(c string) string {
	if len(c) > 12 {
		return c[:12]
	}
	return c
}

// TestMatterJSSchemaStalenessAgainstLiveCheckout makes upstream schema
// staleness visible. It compares the pinned per-cluster ClusterRevision
// (schema.ClusterRevisions, generated from
// notes/parity/matter/matter-schema-snapshot.json) against a live ../matter.js
// checkout's element sources and reports every loom-used cluster whose
// revision has increased upstream — the drift the pin would otherwise hide.
//
// The snapshot bump (e.g. Matter 1.5.x -> 1.6.x) is a MANUAL decision: a
// revision increment can carry attribute, constraint, or command changes that
// need code review, so this guard only surfaces the drift, it does not perform
// the bump. Behaviour:
//
//   - ../matter.js absent -> Skip (this is a local / scheduled tooling check;
//     CI has no live checkout, only the pinned snapshot).
//   - No loom-used cluster drifted -> Pass (the pin is current).
//   - Drift found -> advisory Skip listing exactly which clusters drifted,
//     UNLESS OPENCCU_LOOM_MATTERJS_STALENESS_STRICT=1 is set, in which case it
//     is a hard Fail. The strict mode is the shape a dedicated scheduled job
//     that checks out matter.js HEAD would run to raise a signal.
//
// Mirrors the extraction the parity snapshot performs in
// notes/parity/matter/extract-from-matter-js.ts: the ClusterRevision (attribute
// 0xFFFD) default in
// ../matter.js/packages/model/src/standard/elements/*.element.ts.
func TestMatterJSSchemaStalenessAgainstLiveCheckout(t *testing.T) {
	t.Parallel()

	dir, ok := matterJSElementsDir()
	if !ok {
		t.Skip("../matter.js checkout not present — schema staleness is a local/scheduled tooling guard")
	}

	live, err := parseLiveClusterRevisions(dir)
	if err != nil {
		t.Fatalf("parse live matter.js element revisions: %v", err)
	}
	if len(live) == 0 {
		t.Skipf("no cluster revisions parsed from %s — matter.js element layout may have changed", dir)
	}

	pinnedRev, pinnedCommit := pinnedSnapshotProvenance()
	liveCommit := gitHead(dir)

	type drift struct {
		id            uint32
		name          string
		pinned, upsts uint16
	}
	var drifts []drift
	ids := make([]uint32, 0, len(loomUsedClusters))
	for id := range loomUsedClusters {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	for _, id := range ids {
		liveRev, present := live[id]
		if !present {
			// Cluster inherits its revision from a base type (not inline) —
			// cannot be compared from the element source. None of these are
			// revision-drifting loom-used clusters today.
			continue
		}
		pinnedR, known := schema.ClusterRevisions[id]
		if !known {
			t.Errorf("loom-used cluster 0x%04X (%s) is missing from the pinned schema.ClusterRevisions map", id, loomUsedClusters[id])
			continue
		}
		if liveRev > pinnedR {
			drifts = append(drifts, drift{id: id, name: loomUsedClusters[id], pinned: pinnedR, upsts: liveRev})
		}
	}

	if len(drifts) == 0 {
		t.Logf("matter.js schema pin is current for all %d loom-used clusters (pinned matter %s, commit %s)",
			len(loomUsedClusters), pinnedRev, shortCommit(pinnedCommit))
		return
	}

	var b strings.Builder
	fmt.Fprintf(&b, "matter.js HEAD advanced %d loom-used cluster revision(s) past the pinned snapshot\n", len(drifts))
	fmt.Fprintf(&b, "  pinned snapshot: matter %s (commit %s)\n", pinnedRev, shortCommit(pinnedCommit))
	fmt.Fprintf(&b, "  live checkout:   %s (commit %s)\n", dir, shortCommit(liveCommit))
	for _, d := range drifts {
		fmt.Fprintf(&b, "  0x%04X %-40s pinned=%d  upstream=%d\n", d.id, d.name, d.pinned, d.upsts)
	}
	b.WriteString("Adopting the newer schema is a manual review: run `make generate-matter-schema` after " +
		"deciding to bump, then reconcile the per-cluster revision constants and re-run the parity tests.")

	if strings.TrimSpace(os.Getenv("OPENCCU_LOOM_MATTERJS_STALENESS_STRICT")) == "1" {
		t.Fatalf("%s", b.String())
	}
	t.Log(b.String())
	t.Skipf("matter.js schema pin is stale: %d loom-used cluster(s) drifted upstream "+
		"(set OPENCCU_LOOM_MATTERJS_STALENESS_STRICT=1 to fail)", len(drifts))
}
