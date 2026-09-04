// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// ha_component_non_empty_test.go keeps one branch of
// internal/model/device/channel.go unreachable.
//
// Channel.HasSinglePrimaryCustomDP groups a device's primary custom data
// points by the HA component their profile declares, and returns false
// when that component is the empty string. False there means "not a
// single primary in this category", which the name builders read as
// "there are several" — so a profile returning "" silently collects a
// ch<N> suffix on both the MQTT discovery names and the REST
// custom-data-point names, and nothing reports it.
//
// Every profile shipped today returns a non-empty component, so the
// branch never fires. These tests hold that: one asserts the value each
// implementer returns, the other re-derives the implementer set from the
// source so a new profile cannot join without joining the table too.
package contract

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/custom/climate"
	"github.com/SukramJ/openccu-loom/internal/model/custom/cover"
	"github.com/SukramJ/openccu-loom/internal/model/custom/light"
	"github.com/SukramJ/openccu-loom/internal/model/custom/lock"
	"github.com/SukramJ/openccu-loom/internal/model/custom/siren"
	switchdp "github.com/SukramJ/openccu-loom/internal/model/custom/switch"
	"github.com/SukramJ/openccu-loom/internal/model/custom/textdisplay"
	"github.com/SukramJ/openccu-loom/internal/model/custom/valve"
)

// haComponentProvider repeats the private contract
// internal/model/device/channel.go type-asserts custom data points
// against. It cannot be imported: the device package keeps it unexported
// so it stays free of a payload import.
type haComponentProvider interface {
	HAComponent() string
}

// haComponentImplementers lists every custom data point that satisfies
// [haComponentProvider], keyed by "<package>.<type>" — the same spelling
// TestHAComponentTableCoversEverySourceImplementer re-derives from the
// source, so the two halves fail independently.
func haComponentImplementers() map[string]haComponentProvider {
	return map[string]haComponentProvider{
		"climate.Climate":         &climate.Climate{},
		"cover.Cover":             &cover.Cover{},
		"cover.Blind":             &cover.Blind{},
		"cover.Garage":            &cover.Garage{},
		"light.Light":             &light.Light{},
		"lock.Lock":               &lock.Lock{},
		"siren.Siren":             &siren.Siren{},
		"siren.SmokeSiren":        &siren.SmokeSiren{},
		"siren.SoundPlayer":       &siren.SoundPlayer{},
		"switch.Switch":           &switchdp.Switch{},
		"switch.AccessPermission": &switchdp.AccessPermission{},
		"textdisplay.TextDisplay": &textdisplay.TextDisplay{},
		"valve.Irrigation":        &valve.Irrigation{},
		"valve.Modulating":        &valve.Modulating{},
	}
}

// TestEveryCustomDataPointDeclaresAnHAComponent asserts no shipped
// profile returns the empty component that would make the ch<N> branch
// in Channel.HasSinglePrimaryCustomDP reachable.
func TestEveryCustomDataPointDeclaresAnHAComponent(t *testing.T) {
	t.Parallel()
	for name, dp := range haComponentImplementers() {
		if got := dp.HAComponent(); got == "" {
			t.Errorf("%s.HAComponent() is empty — Channel.HasSinglePrimaryCustomDP "+
				"returns false for it, so every channel carrying it collects a ch<N> "+
				"name suffix on MQTT discovery and REST instead of the collapsed name",
				name)
		}
	}
}

// haComponentMethodRE matches a method declaration whose name is
// HAComponent and whose receiver is a pointer, capturing the receiver
// type. Deliberately loose on the receiver identifier so a renamed
// receiver variable does not hide an implementer.
var haComponentMethodRE = regexp.MustCompile(`func \(\w+ \*(\w+)\) HAComponent\(\) string`)

// TestHAComponentTableCoversEverySourceImplementer re-derives the
// implementer set by reading internal/model/custom and compares it with
// the table above.
//
// Without this, adding a fifteenth custom data point would leave the
// value assertion silently narrower than the code it guards: the new
// type would be free to return "" and no test would look at it.
func TestHAComponentTableCoversEverySourceImplementer(t *testing.T) {
	t.Parallel()
	root := filepath.Join("..", "..", "internal", "model", "custom")

	var fromSource []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		src, readErr := os.ReadFile(path) //nolint:gosec // fixed repo-relative walk
		if readErr != nil {
			return readErr
		}
		pkg := filepath.Base(filepath.Dir(path))
		for _, m := range haComponentMethodRE.FindAllStringSubmatch(string(src), -1) {
			fromSource = append(fromSource, pkg+"."+m[1])
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}
	if len(fromSource) == 0 {
		t.Fatal("found no HAComponent implementers in the source at all — " +
			"the scan is broken, not the code")
	}

	table := haComponentImplementers()
	sort.Strings(fromSource)
	for _, name := range fromSource {
		if _, ok := table[name]; !ok {
			t.Errorf("%s declares HAComponent() but is missing from haComponentImplementers — "+
				"add it, so its component is checked against the empty-string branch in "+
				"Channel.HasSinglePrimaryCustomDP", name)
		}
	}
	inSource := make(map[string]bool, len(fromSource))
	for _, name := range fromSource {
		inSource[name] = true
	}
	for name := range table {
		if !inSource[name] {
			t.Errorf("haComponentImplementers lists %s, but no HAComponent() method for it "+
				"exists in the source — the table is stale", name)
		}
	}
}
