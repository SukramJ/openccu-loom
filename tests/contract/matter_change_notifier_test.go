// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package contract

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestMatterEndpointSourcesAreChangeNotifiers guarantees that every type
// asserted as an interfaces.MatterEndpointSource also asserts
// interfaces.MatterChangeNotifier in the same file.
//
// A bridged Matter endpoint whose source cannot notify the bridge of a
// CCU-confirmed value change never propagates external state onto a
// controller's Subscribe: the accessory reflects only the commands the
// controller itself sent, never a change made at the wall switch or by a CCU
// program. That is exactly the failure that hid a bridged dimmer's and
// thermostat's external changes from Apple Home. The bridge wires a change
// listener via ep.Source.(interfaces.MatterChangeNotifier)
// (internal/north/matter/bridge/subscribe.go, wireMeasurementListenersLocked),
// so a source that does not implement OnMatterValueChanged is silently
// skipped.
//
// Requiring the sibling assertion forces each endpoint-source type to
// implement OnMatterValueChanged (directly, or by inheriting it from an
// embedded *generic.Float / *generic.Switch), and fails the build the moment
// a new device type reopens the gap.
func TestMatterEndpointSourcesAreChangeNotifiers(t *testing.T) {
	root := filepath.Join("..", "..", "internal")
	endpointRe := regexp.MustCompile(`interfaces\.MatterEndpointSource\s*=\s*\(\*(\w+)\)\(nil\)`)
	notifierRe := regexp.MustCompile(`interfaces\.MatterChangeNotifier\s*=\s*\(\*(\w+)\)\(nil\)`)

	type miss struct{ file, typ string }
	var (
		misses  []miss
		checked int
	)

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		src := string(data)
		endpoints := endpointRe.FindAllStringSubmatch(src, -1)
		if len(endpoints) == 0 {
			return nil
		}
		notifiers := make(map[string]bool)
		for _, m := range notifierRe.FindAllStringSubmatch(src, -1) {
			notifiers[m[1]] = true
		}
		for _, m := range endpoints {
			checked++
			if !notifiers[m[1]] {
				misses = append(misses, miss{file: path, typ: m[1]})
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	if checked == 0 {
		t.Fatal("found no MatterEndpointSource assertions — the walk or regex is broken")
	}

	for _, m := range misses {
		t.Errorf("%s: %s is a MatterEndpointSource but the file has no "+
			"`_ interfaces.MatterChangeNotifier = (*%s)(nil)` assertion; "+
			"external CCU changes will not reach controllers via Subscribe",
			m.file, m.typ, m.typ)
	}
	if len(misses) > 0 {
		t.Fatalf("%d/%d MatterEndpointSource type(s) missing a MatterChangeNotifier assertion", len(misses), checked)
	}
}
