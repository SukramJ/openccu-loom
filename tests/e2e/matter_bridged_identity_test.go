// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

//go:build e2e

package e2e

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
	"time"

	bdbidef "thingfabric.dev/go-fabric/cluster/spec/bridgeddevicebasicinformation"
	powersourcedef "thingfabric.dev/go-fabric/cluster/spec/powersource"

	"github.com/SukramJ/openccu-loom/tests/e2e/harness"
)

// TestE2EMatterBridgedDeviceStatesItsCCUIdentity holds what a running bridge
// serves of each bridged device against what the daemon's REST API reports
// of the same device: SerialNumber is the device address, ProductName and
// ProductLabel are the model, SoftwareVersionString is the installed
// firmware. The node serves none of them unless the host states them, so the
// statement has to survive the whole path from the device model through the
// assembler, the reconciler and the node to the wire. A battery power source
// says it does not know how its battery is replaced
// (BatReplaceability Unspecified): the CCU does not report it.
func TestE2EMatterBridgedDeviceStatesItsCCUIdentity(t *testing.T) {
	t.Parallel()
	h := harness.Start(t, harness.Options{EnableMatter: true, StartCCUNotReady: true, AuthMode: harness.AuthSession})
	if err := h.REST().LoginSession(harness.AdminUser, harness.AdminPass); err != nil {
		t.Fatalf("login: %v", err)
	}
	h.CCU().V().SetReady(true)
	awaitCentralReady(t, h, harness.CentralName)
	if n := h.EnableAllExposures(t); n == 0 {
		t.Fatal("the node offers no source to expose; the comparison would hold vacuously")
	}
	settledBridgeEndpoints(t, h, 5*time.Second, 90*time.Second)

	type identity struct{ model, firmware string }
	devices := map[string]identity{}
	endpointsOf := map[string][]uint16{}
	for _, raw := range getJSONArray(t, h, "/api/v1/devices", "items") {
		dev, _ := raw.(map[string]any)
		addr, _ := dev["address"].(string)
		model, _ := dev["model"].(string)
		fw, _ := dev["firmware"].(map[string]any)
		current, _ := fw["Current"].(string)
		devices[addr] = identity{model: model, firmware: current}

		status, out := getProjection(t, h, "/api/v1/devices/"+addr+"/projection?plane=matter")
		if status != http.StatusOK || len(out.Planes) != 1 {
			t.Fatalf("%s: projection status %d, planes %+v", addr, status, out.Planes)
		}
		for _, rawEntry := range out.Planes[0].Entries {
			var e measurementEntry
			if err := json.Unmarshal(rawEntry, &e); err != nil {
				t.Fatalf("decode entry: %v", err)
			}
			if e.Verdict != "published" {
				continue
			}
			id, err := strconv.ParseUint(e.Identity["endpoint_id"], 10, 16)
			if err != nil {
				t.Fatalf("%s: published entry %d/%s names endpoint %q: %v", addr, e.Channel, e.Key, e.Identity["endpoint_id"], err)
			}
			endpointsOf[addr] = append(endpointsOf[addr], uint16(id))
		}
	}
	if len(endpointsOf) == 0 {
		t.Fatal("no device has a published endpoint; the comparison would hold vacuously")
	}

	stringAttr := func(ep uint16, attr uint32) (string, bool) {
		raw, ok := h.EndpointAttribute(t, ep, bdbidef.ClusterID, attr)
		if !ok {
			return "", false
		}
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			t.Fatalf("endpoint %d: attribute 0x%04X is not a string: %s", ep, attr, raw)
		}
		return s, true
	}
	checked, batteries := 0, 0
	for addr, eps := range endpointsOf {
		want := devices[addr]
		if want.model == "" || want.firmware == "" {
			t.Fatalf("%s: the REST API reports model %q, firmware %q; the fleet must carry both for this check",
				addr, want.model, want.firmware)
		}
		for _, ep := range eps {
			for _, c := range []struct {
				name string
				attr uint32
				want string
			}{
				{"SerialNumber", bdbidef.AttrSerialNumber, addr},
				{"ProductName", bdbidef.AttrProductName, want.model},
				{"ProductLabel", bdbidef.AttrProductLabel, want.model},
				{"SoftwareVersionString", bdbidef.AttrSoftwareVersionString, want.firmware},
			} {
				got, ok := stringAttr(ep, c.attr)
				switch {
				case !ok:
					t.Errorf("%s endpoint %d: serves no %s, want %q", addr, ep, c.name, c.want)
				case got != c.want:
					t.Errorf("%s endpoint %d: %s = %q, want %q", addr, ep, c.name, got, c.want)
				}
			}
			checked++
			if raw, ok := h.EndpointAttribute(t, ep, powersourcedef.ClusterID, powersourcedef.AttrBatReplaceability); ok {
				batteries++
				var got int64
				if err := json.Unmarshal(raw, &got); err != nil {
					t.Fatalf("endpoint %d: BatReplaceability is not a number: %s", ep, raw)
				}
				if got != int64(powersourcedef.BatReplaceabilityUnspecified) {
					t.Errorf("%s endpoint %d: BatReplaceability = %d, want %d (Unspecified)",
						addr, ep, got, powersourcedef.BatReplaceabilityUnspecified)
				}
			}
		}
	}
	if batteries == 0 {
		t.Error("no bridged endpoint serves a battery power source; the BatReplaceability check held vacuously")
	}
	t.Logf("%d bridged endpoints of %d devices checked, %d with a battery power source", checked, len(endpointsOf), batteries)
}
