//go:build integration

// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package integration

import (
	"encoding/json"
	"fmt"
	"sort"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/parity"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// Conformance inside a cluster, one level below the device-type check in
// matter_endpoint_cluster_conformance_test.go. That guard asks whether an
// endpoint may mount a cluster at all; these ask whether the mounted cluster
// presents the surface its own definition requires:
//
//   - every attribute the cluster marks conformance "M" answers a read
//   - every attribute gated on a feature answers one when the server
//     advertises that feature bit
//   - every command the cluster marks conformance "M" appears in the
//     server's AcceptedCommandList
//
// A commissioner reads the mandatory set during pairing. Apple's HAP-service
// rebuild aborts on a missing mandatory attribute (HAPErrorDomain Code=24) and
// wires HomeKit characteristics from the command lists, so a gap here does not
// degrade gracefully — it costs the pairing or the control surface.
//
// The oracle is the matter.js HEAD snapshot embedded by internal/north/matter/
// parity, unmarshalled here into the shapes these checks need. Only plain "M"
// and bare feature-name conformances are evaluated: the conformance grammar
// also has expressions ("PIN | RID", "[ALIRO]", "O.a+") whose evaluation this
// does not implement, and a half-implemented grammar would produce confident
// wrong answers rather than fewer answers.

// conformanceAttr is one attribute of a cluster definition.
type conformanceAttr struct {
	ID          uint32 `json:"id"`
	Name        string `json:"name"`
	Conformance string `json:"conformance"`
}

// conformanceCmd is one command of a cluster definition. Direction
// "response" marks a server-to-client command, which belongs in
// GeneratedCommandList rather than AcceptedCommandList.
type conformanceCmd struct {
	ID          uint32 `json:"id"`
	Name        string `json:"name"`
	Direction   string `json:"direction"`
	Conformance string `json:"conformance"`
}

// conformanceFeature is one FeatureMap bit. Bit is the position recorded from
// the element's `constraint`, never the field's index: feature bits are sparse
// (DoorLock has no bit 3 and no bit 9), so an index-derived bit mislabels
// every feature after the first gap. A feature without a recorded bit is
// skipped rather than guessed at.
type conformanceFeature struct {
	Name        string `json:"name"`
	Conformance string `json:"conformance"`
	Bit         *uint  `json:"bit"`
}

// conformanceCluster is a cluster definition from the matter.js snapshot.
type conformanceCluster struct {
	ID         uint32               `json:"id"`
	Name       string               `json:"name"`
	Attributes []conformanceAttr    `json:"attributes"`
	Commands   []conformanceCmd     `json:"commands"`
	Features   []conformanceFeature `json:"features"`
}

// matterGlobalAttributeFloor is the start of the global-attribute range
// (FeatureMap, ClusterRevision, AttributeList, …). The dispatcher answers
// those centrally for every cluster, so a cluster server is not expected to.
const matterGlobalAttributeFloor uint32 = 0xFFF0

// commandsWithoutAServer records mandatory commands that a mounted cluster
// does not accept. These are known gaps, not exemptions: each entry says what
// the cluster is and why the command is missing.
//
// Emptying this map means implementing the cluster, not deleting the entry.
// Key format: "0x%04X/0x%02X" (cluster, command).
var commandsWithoutAServer = map[string]string{
	"0x0004/0x04": "Groups.RemoveAllGroups — wire.Groups is a stub. Groups (0x0004) is conformance M " +
		"for OnOffPlugInUnit and the light device types, so it must be mounted, but HomeMatic has no " +
		"group concept for it to map onto and the stub rejects every command. Matter groups are a node " +
		"concept rather than a device one (matter.js implements them in GroupsServer against its own " +
		"table), so closing this means the bridge keeping a group table of its own, not finding a CCU " +
		"feature to bind to. The empty AcceptedCommandList is at least honest about it today",
	"0x0004/0x05": "Groups.AddGroupIfIdentifying — same stub, same fix as 0x0004/0x04",
	"0x0062/0x05": "ScenesManagement.RecallScene — wire.ScenesManagement is a stub for the same reason: " +
		"the cluster is conformance M on the light and plug device types, and scene storage is a node " +
		"concept the bridge would have to keep itself",
}

// loadConformanceClusters returns the snapshot's cluster definitions by ID.
func loadConformanceClusters(t *testing.T) map[uint32]conformanceCluster {
	t.Helper()
	var schema struct {
		Clusters []conformanceCluster `json:"clusters"`
	}
	if err := json.Unmarshal(parity.SchemaJSON(), &schema); err != nil {
		t.Fatalf("unmarshal matter.js schema snapshot: %v", err)
	}
	if len(schema.Clusters) == 0 {
		t.Fatal("the embedded schema snapshot carries no clusters — every check over it would pass vacuously")
	}
	out := make(map[uint32]conformanceCluster, len(schema.Clusters))
	for _, c := range schema.Clusters {
		out[c.ID] = c
	}
	return out
}

// mountedCluster pairs a live cluster server with its snapshot definition and
// a witness naming where it was found.
type mountedCluster struct {
	server  interfaces.MatterClusterServer
	spec    conformanceCluster
	witness string
}

// walkMountedClusters yields every cluster server the fleet's endpoint sources
// mount, paired with its definition. Fails when the walk finds nothing, since
// every check built on it would then pass vacuously.
func walkMountedClusters(t *testing.T) []mountedCluster {
	t.Helper()
	specs := loadConformanceClusters(t)
	devices := hydrateFleetForMatterConformance(t)

	out := make([]mountedCluster, 0, 512)
	for _, dev := range devices {
		for _, ch := range dev.Channels() {
			cdp := ch.CustomDataPoint()
			if cdp == nil {
				continue
			}
			src, ok := cdp.(interfaces.MatterEndpointSource)
			if !ok {
				continue
			}
			for _, server := range src.MatterClusterServers() {
				if server == nil {
					continue
				}
				spec, known := specs[server.MatterClusterID()]
				if !known {
					t.Errorf("%s ch%d %T mounts cluster 0x%04X, absent from the matter.js HEAD schema "+
						"snapshot — wrong id, or the snapshot is stale (`make generate-matter-schema`)",
						dev.Model, ch.Number, cdp, server.MatterClusterID())
					continue
				}
				out = append(out, mountedCluster{
					server:  server,
					spec:    spec,
					witness: fmt.Sprintf("%s ch%d %T", dev.Model, ch.Number, cdp),
				})
			}
		}
	}
	if len(out) == 0 {
		t.Fatal("the fleet mounted no cluster servers at all — the walk is broken and every check " +
			"over it would pass vacuously")
	}
	return out
}

// TestEveryMandatoryAttributeIsAnswered asserts that each mounted cluster
// answers a read for every attribute its definition marks conformance "M".
func TestEveryMandatoryAttributeIsAnswered(t *testing.T) {
	gaps := map[string]string{}
	checked := 0

	for _, mc := range walkMountedClusters(t) {
		for _, attr := range mc.spec.Attributes {
			if attr.Conformance != "M" || attr.ID >= matterGlobalAttributeFloor {
				continue
			}
			checked++
			if _, ok := mc.server.MatterRead(attr.ID); !ok {
				gaps[fmt.Sprintf("0x%04X/0x%04X", mc.spec.ID, attr.ID)] = fmt.Sprintf(
					"%s.%s is conformance M but MatterRead does not answer it — seen on %s",
					mc.spec.Name, attr.Name, mc.witness,
				)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no mandatory attribute was probed — the snapshot's conformance data or the walk is " +
			"broken and this guard would pass vacuously")
	}
	reportConformanceGaps(t, gaps, "mandatory attribute")
	t.Logf("checked %d mandatory-attribute reads", checked)
}

// TestFeatureGatedAttributesAreAnsweredWhenAdvertised asserts the other
// direction: an attribute whose conformance is exactly a feature name must be
// answered when the server advertises that feature's bit.
//
// Advertising a feature is a promise about the surface. A controller that
// reads the FeatureMap and then finds the attribute unsupported sees a
// contradiction, which is what makes this worth its own check rather than a
// note on the mandatory one.
func TestFeatureGatedAttributesAreAnsweredWhenAdvertised(t *testing.T) {
	gaps := map[string]string{}
	checked, withFeatures := 0, 0

	for _, mc := range walkMountedClusters(t) {
		if len(mc.spec.Features) == 0 {
			continue
		}
		raw, ok := mc.server.MatterRead(0xFFFC) // FeatureMap
		if !ok {
			continue // answered centrally by the dispatcher; nothing claimed here
		}
		featureMap, ok := raw.(uint32)
		if !ok {
			t.Errorf("%s: FeatureMap read returned %T, want uint32 — the wire type must be map32 "+
				"(spec §7.13.2), and a wrong Go type reaches the encoder, not this test", mc.witness, raw)
			continue
		}
		withFeatures++

		advertised := make(map[string]bool, len(mc.spec.Features))
		for _, f := range mc.spec.Features {
			if f.Bit == nil {
				continue // no recorded position; never derive one from order
			}
			if featureMap&(1<<*f.Bit) != 0 {
				advertised[f.Name] = true
			}
		}
		for _, attr := range mc.spec.Attributes {
			if attr.ID >= matterGlobalAttributeFloor || !advertised[attr.Conformance] {
				continue
			}
			checked++
			if _, ok := mc.server.MatterRead(attr.ID); !ok {
				gaps[fmt.Sprintf("0x%04X/0x%04X", mc.spec.ID, attr.ID)] = fmt.Sprintf(
					"%s.%s is conformance %q and the server advertises that feature (FeatureMap=0x%X), "+
						"but MatterRead does not answer it — seen on %s",
					mc.spec.Name, attr.Name, attr.Conformance, featureMap, mc.witness,
				)
			}
		}
	}
	if withFeatures == 0 {
		t.Fatal("no mounted cluster answered a FeatureMap — the walk or the read path is broken and " +
			"this guard would pass vacuously")
	}
	reportConformanceGaps(t, gaps, "feature-gated attribute")
	t.Logf("checked %d feature-gated attribute reads across %d clusters advertising features",
		checked, withFeatures)
}

// TestEveryMandatoryCommandIsAccepted asserts that a mounted cluster lists
// every command its definition marks conformance "M" in AcceptedCommandList.
//
// The list is what a controller derives control capability from: Apple's
// HAP-service rebuild wires characteristics from it, so a cluster that accepts
// a command without listing it is uninvokable in practice, and one that lists
// a command it rejects fails on use.
func TestEveryMandatoryCommandIsAccepted(t *testing.T) {
	gaps := map[string]string{}
	used := map[string]bool{}
	checked := 0

	for _, mc := range walkMountedClusters(t) {
		accepted := map[uint32]bool{}
		if lister, ok := mc.server.(interfaces.MatterClusterCommandLister); ok {
			for _, id := range lister.MatterAcceptedCommands() {
				accepted[id] = true
			}
		}
		for _, cmd := range mc.spec.Commands {
			// Only client-to-server commands belong in AcceptedCommandList.
			if cmd.Conformance != "M" || cmd.Direction == "response" {
				continue
			}
			checked++
			if accepted[cmd.ID] {
				continue
			}
			key := fmt.Sprintf("0x%04X/0x%02X", mc.spec.ID, cmd.ID)
			if _, declared := commandsWithoutAServer[key]; declared {
				used[key] = true
				continue
			}
			gaps[key] = fmt.Sprintf(
				"%s.%s is conformance M but absent from AcceptedCommandList — seen on %s. A cluster "+
					"mounted without its mandatory commands is uninvokable for the controller that "+
					"reads the list",
				mc.spec.Name, cmd.Name, mc.witness,
			)
		}
	}
	if checked == 0 {
		t.Fatal("no mandatory command was probed — the snapshot's command data or the walk is broken " +
			"and this guard would pass vacuously")
	}
	reportConformanceGaps(t, gaps, "mandatory command")

	// A declared gap the fleet no longer reaches is fixed or unreachable;
	// either way the entry must go, so a regression is not absorbed by it.
	for key, note := range commandsWithoutAServer {
		if !used[key] {
			t.Errorf("commandsWithoutAServer[%q] was not hit by any device in the fleet — the cluster "+
				"now accepts the command, or no fleet device mounts it any more. Delete the entry. "+
				"Recorded gap: %s", key, note)
		}
	}
	t.Logf("checked %d mandatory commands, %d declared gaps", checked, len(commandsWithoutAServer))
}

// reportConformanceGaps emits one error per distinct gap, in stable order so a
// failing run reads the same way twice.
func reportConformanceGaps(t *testing.T, gaps map[string]string, kind string) {
	t.Helper()
	if len(gaps) == 0 {
		return
	}
	keys := make([]string, 0, len(gaps))
	for k := range gaps {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		t.Errorf("%s gap %s: %s", kind, k, gaps[k])
	}
}
