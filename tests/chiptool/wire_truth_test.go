// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

//go:build chiptool

// wire_truth_test.go — End-to-end wire-truth validation of the
// Matter Subscribe-Initial stream against the matter.js schema pin.
//
// Spins up the shared bridge + chip-tool fabric, then for every
// mandatory Root-endpoint cluster reads back its ClusterRevision
// (0xFFFD) and asserts the value matches the revision recorded in
// docs/parity/matter/matter-schema-snapshot.json. The point of the
// test is not to exercise every cluster comprehensively — the
// per-cluster parity_matterjs_test.go files cover that statically —
// but to prove the actual on-the-wire bytes a chip-tool peer sees
// match the static parity pin.
//
// The test self-skips when chip-tool is not on PATH (via
// requireBridge) so CI hosts without the Matter SDK stay green.
package chiptool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/tests/chiptool/harness"
)

// matterSchemaCluster mirrors the relevant subset of the
// matter-schema-snapshot.json `clusters[]` entries.
type matterSchemaCluster struct {
	ID         uint32 `json:"id"`
	Name       string `json:"name"`
	Revision   uint16 `json:"revision"`
	Attributes []struct {
		ID   uint32 `json:"id"`
		Name string `json:"name"`
	} `json:"attributes"`
}

type matterSchemaSnapshot struct {
	Clusters []matterSchemaCluster `json:"clusters"`
}

// loadSchemaSnapshot reads the matter.js HEAD schema pin from the
// repo. The file is hand-checked into git so the test target does
// not need a chip-tool / npm toolchain to know the expected
// revisions.
func loadSchemaSnapshot(t *testing.T) matterSchemaSnapshot {
	t.Helper()
	_, thisFile, _, _ := runtime.Caller(0)
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", ".."))
	path := filepath.Join(repoRoot, "docs", "parity", "matter", "matter-schema-snapshot.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("schema snapshot %s: %v", path, err)
	}
	var snap matterSchemaSnapshot
	if err := json.Unmarshal(raw, &snap); err != nil {
		t.Fatalf("decode schema snapshot: %v", err)
	}
	return snap
}

// findClusterByID looks the cluster up by its 32-bit cluster ID.
func findClusterByID(snap matterSchemaSnapshot, id uint32) (matterSchemaCluster, bool) {
	for _, c := range snap.Clusters {
		if c.ID == id {
			return c, true
		}
	}
	return matterSchemaCluster{}, false
}

// rootMandatoryClusters lists the cluster IDs every Matter Root
// endpoint must advertise, paired with the chip-tool argument name
// used to address the cluster on the cli. The pairs are stable
// across every released Matter version since 1.0.
//
// Each entry is what the bridge claims it serves on EP0; the test
// reads cluster-revision back via chip-tool and cross-references
// the value against the schema pin.
var rootMandatoryClusters = []struct {
	ID       uint32
	ChipName string
}{
	{0x0028, "basicinformation"},
	{0x001D, "descriptor"},
	{0x001F, "accesscontrol"},
	{0x003E, "operationalcredentials"},
	{0x003C, "administratorcommissioning"},
	{0x0030, "generalcommissioning"},
	{0x0031, "networkcommissioning"},
	{0x0033, "generaldiagnostics"},
	{0x003F, "groupkeymanagement"},
}

// TestMatterWireTruth_RootClusterRevisionsMatchSchema reads
// ClusterRevision (attribute 0xFFFD) from every mandatory Root-
// endpoint cluster via chip-tool and asserts the value equals the
// revision recorded in matter-schema-snapshot.json. A drift here
// means the bridge advertises a different version than matter.js
// HEAD — exactly the silent failure Apple Home / chip-tool keep
// running into when our pin lags behind the SDK.
//
// Skips when chip-tool is unavailable so CI without the Matter SDK
// still passes.
func TestMatterWireTruth_RootClusterRevisionsMatchSchema(t *testing.T) {
	b := requireBridge(t)
	snap := loadSchemaSnapshot(t)

	for _, cl := range rootMandatoryClusters {
		t.Run(cl.ChipName, func(t *testing.T) {
			want, ok := findClusterByID(snap, cl.ID)
			if !ok {
				t.Skipf("cluster id 0x%04X not in schema snapshot (matter.js HEAD doesn't ship it)", cl.ID)
			}

			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()

			out, err := b.SharedCtl.ReadAttr(ctx, t, cl.ChipName, "cluster-revision", 0)
			if err != nil {
				t.Fatalf("chip-tool read %s.ClusterRevision: %v\n%s", cl.ChipName, err, out)
			}

			got, ok := harness.FindAttrUint(out, "ClusterRevision")
			if !ok {
				t.Fatalf("chip-tool output for %s did not contain ClusterRevision:\n%s", cl.ChipName, out)
			}
			if got != int64(want.Revision) {
				t.Errorf("%s ClusterRevision drift: schema=%d  wire=%d", want.Name, want.Revision, got)
			}
		})
	}
}

// TestMatterWireTruth_RootDescriptorServerListMatchesMandatorySet
// reads Descriptor.ServerList on EP0 and asserts every cluster ID
// from rootMandatoryClusters is advertised. A missing entry here
// means the bridge silently dropped a cluster the schema pin
// declares mandatory — the exact INVENTORY-DERIVATION-DRIFT class
// the L9 audit asked the wire-truth lane to find.
func TestMatterWireTruth_RootDescriptorServerListMatchesMandatorySet(t *testing.T) {
	b := requireBridge(t)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	out, err := b.SharedCtl.ReadAttr(ctx, t, "descriptor", "server-list", 0)
	if err != nil {
		t.Fatalf("chip-tool read Descriptor.ServerList: %v\n%s", err, out)
	}

	for _, cl := range rootMandatoryClusters {
		clusterHex := fmt.Sprintf("0x%04X", cl.ID)
		clusterHexLower := strings.ToLower(clusterHex)
		clusterDec := fmt.Sprintf(" %d ", cl.ID)
		hit := strings.Contains(out, clusterHex) ||
			strings.Contains(out, clusterHexLower) ||
			strings.Contains(out, clusterDec)
		if !hit {
			t.Errorf("Descriptor.ServerList missing mandatory cluster 0x%04X (%s):\n%s",
				cl.ID, cl.ChipName, out)
		}
	}
}

// TestMatterWireTruth_BridgedNodeDeviceTypeRevision reads the
// DeviceTypeList from a bridged endpoint and asserts the
// BridgedNode (0x0013) revision matches the schema snapshot.
// Bridged-endpoint INVENTORY-DERIVATION-DRIFT manifests here when
// the materialize layer hands a stale revision to the wire.
func TestMatterWireTruth_BridgedNodeDeviceTypeRevision(t *testing.T) {
	b := requireBridge(t)
	snap := loadSchemaSnapshot(t)

	// Discover bridged endpoints via the Aggregator on EP1.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	parts, err := b.SharedCtl.ReadAttr(ctx, t, "descriptor", "parts-list", 1)
	if err != nil {
		t.Fatalf("read aggregator parts-list: %v\n%s", err, parts)
	}
	eps := harness.EndpointsInPartsList(parts)
	if len(eps) == 0 {
		t.Skip("no bridged endpoints to probe")
	}

	// Look the BridgedNode revision up in the schema (deviceTypes
	// array, not clusters).
	type deviceTypeEntry struct {
		ID       uint32 `json:"id"`
		Name     string `json:"name"`
		Revision uint16 `json:"revision"`
	}
	var raw struct {
		DeviceTypes []deviceTypeEntry `json:"deviceTypes"`
	}
	// re-decode raw because matterSchemaSnapshot only carries clusters
	_, thisFile, _, _ := runtime.Caller(0)
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", ".."))
	path := filepath.Join(repoRoot, "docs", "parity", "matter", "matter-schema-snapshot.json")
	rawBytes, _ := os.ReadFile(path)
	_ = json.Unmarshal(rawBytes, &raw)
	_ = snap

	var wantRev uint16
	var found bool
	for _, d := range raw.DeviceTypes {
		if d.ID == 0x0013 {
			wantRev = d.Revision
			found = true
			break
		}
	}
	if !found {
		t.Skip("BridgedNode device-type missing from schema snapshot")
	}

	// chip-tool prints the DeviceTypeList as "Endpoint <n>" then
	// nested DeviceType + Revision pairs. We only assert that the
	// expected revision number appears in the output for at least one
	// bridged endpoint — a stronger per-endpoint parse would need
	// chip-tool's structured TLV dump which the current harness does
	// not capture.
	dtList, err := b.SharedCtl.ReadAttr(ctx, t, "descriptor", "device-type-list", eps[0])
	if err != nil {
		t.Fatalf("read bridged device-type-list ep=%d: %v\n%s", eps[0], err, dtList)
	}

	wantRevStr := fmt.Sprintf("%d", wantRev)
	if !strings.Contains(dtList, wantRevStr) {
		t.Errorf("bridged DeviceTypeList does not advertise BridgedNode revision %d:\n%s", wantRev, dtList)
	}
}
