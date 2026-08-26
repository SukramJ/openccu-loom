// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package contract

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// uniqueIDBuildersWithTheirOwnGate lists the files in the MQTT package
// allowed to call routingkey.CanonicalUniqueID directly, with the gate
// they apply instead of the shared one.
var uniqueIDBuildersWithTheirOwnGate = map[string]string{
	"internal/north/mqtt/discovery.go": "defines scopedUniqueID, the shared gate itself",
	"internal/north/mqtt/hub_discovery.go": "hub payloads gate on hubSerial() before building anything — " +
		"pinned by TestHubDiscoverySkipsWithoutSerial",
}

// TestDeviceDiscoveryUniqueIDsGoThroughTheScopeGate asserts that no
// device-bound discovery payload builds a unique_id without passing the
// address through the serial-scope check.
//
// A few address classes are identical on every CCU — the virtual-remote
// buses, INT000*, the hub pseudo-addresses — so their unique_id is only
// unique once the CCU's serial is prepended. The serial arrives with the
// hub bring-up, on a different path than the devices, so there is a
// window where a payload can be built without it. Publishing then hands
// two CCUs the same id, and Home Assistant keeps whichever arrived
// first: the second CCU's entities are lost, and because the payload is
// retained, they stay lost until someone clears the topic by hand. That
// is what `loom__bidcos_rf_10_event` was, published under two different
// CCUs on a production broker.
//
// The gate makes the builder publish nothing instead. A missing entity
// is visible and recovers on the next snapshot; a collision is silent
// and does not.
func TestDeviceDiscoveryUniqueIDsGoThroughTheScopeGate(t *testing.T) {
	t.Parallel()
	root := repoRootForHelpers(t)
	dir := filepath.Join(root, "internal", "north", "mqtt")

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	checked := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		rel := filepath.ToSlash(filepath.Join("internal", "north", "mqtt", e.Name()))
		src, readErr := os.ReadFile(filepath.Join(dir, e.Name()))
		if readErr != nil {
			t.Fatalf("read %s: %v", rel, readErr)
		}
		checked++
		if _, exempt := uniqueIDBuildersWithTheirOwnGate[rel]; exempt {
			continue
		}
		for _, line := range strings.Split(string(src), "\n") {
			code, _, _ := strings.Cut(line, "//")
			if strings.Contains(code, "routingkey.CanonicalUniqueID") ||
				strings.Contains(code, "routingkey.CalculatedUniqueID") {
				t.Errorf("%s builds a unique_id directly:\n  %s\n"+
					"  Use DefaultDiscoveryBuilder.scopedUniqueID instead and skip the payload when it "+
					"reports the address cannot be scoped — an unscoped id collides across CCUs and the "+
					"collision is retained on the broker.", rel, strings.TrimSpace(line))
			}
		}
	}
	if checked == 0 {
		t.Fatal("no production files were scanned — the walk is broken and this test would pass vacuously")
	}
	for rel := range uniqueIDBuildersWithTheirOwnGate {
		if _, err := os.Stat(filepath.Join(root, rel)); err != nil {
			t.Errorf("uniqueIDBuildersWithTheirOwnGate names %q, which no longer exists — a stale "+
				"exemption silently allows the next ungated builder", rel)
		}
	}
}
