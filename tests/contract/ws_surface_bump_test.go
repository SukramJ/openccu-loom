// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package contract

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
)

var updateWSSurface = flag.Bool("update-ws-surface", false,
	"rewrite tests/contract/testdata/ws_surface.json from the current wsapi.json")

// wsSurface is the committed inventory of the WebSocket command surface, the
// counterpart to [apiSurface] for the half of the north-bound contract that
// lives in assets/wsapi.json.
//
// It exists because the REST inventory covered one of two surfaces while the
// version policy covered both. Two of the three most recent major bumps —
// links.put_paramset gaining a required edit token, links.test_profile being
// replaced by links.apply_profile — happened entirely on the WebSocket side
// and reached no guard at all: they were caught by review and recorded by hand
// in [valueSemanticsChanges]. A hand-kept list is the right home for a change
// no diff can see; it is the wrong home for a removed command, which a diff
// sees perfectly well once somebody writes the diff down.
type wsSurface struct {
	WSAPIVersion string            `json:"wsapi_version"`
	Commands     map[string]string `json:"commands"` // command name -> "arg:type, arg:type" sorted
}

// TestWSSurfaceChangesCarryTheRightBump holds the WebSocket surface to the
// same policy the REST surface follows: a removal, rename or retype is a major
// bump, an addition is a minor one.
//
// The argument vocabulary carries optionality as a "?" suffix, so the two
// directions are not symmetric and the guard does not treat them as such.
// Making a required argument optional cannot break a caller — every call it
// used to accept it still accepts — so "string" -> "string?" is additive.
// The reverse tightens the contract on callers that omit it, and is breaking.
func TestWSSurfaceChangesCarryTheRightBump(t *testing.T) {
	current := buildWSSurface(t)
	path := wsSurfacePath(t)

	if *updateWSSurface {
		writeWSSurface(t, path, current)
		t.Logf("rewrote %s at wsapi_version %s", path, current.WSAPIVersion)
		return
	}

	raw, err := os.ReadFile(path) //nolint:gosec // fixed testdata path
	if err != nil {
		t.Fatalf("read %s: %v (regenerate with -update-ws-surface)", path, err)
	}
	var baseline wsSurface
	if err := json.Unmarshal(raw, &baseline); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}

	var breaking, additive []string

	for name, oldArgs := range baseline.Commands {
		newArgs, ok := current.Commands[name]
		if !ok {
			breaking = append(breaking, "command removed: "+name)
			continue
		}
		oldSet := parseWSArgs(oldArgs)
		newSet := parseWSArgs(newArgs)
		for arg, oldType := range oldSet {
			newType, ok := newSet[arg]
			switch {
			case !ok:
				breaking = append(breaking, fmt.Sprintf("argument removed: %s.%s", name, arg))
			case newType == oldType:
			case newType == oldType+"?":
				additive = append(additive, fmt.Sprintf("argument became optional: %s.%s", name, arg))
			default:
				breaking = append(breaking, fmt.Sprintf("argument retyped: %s.%s %s -> %s", name, arg, oldType, newType))
			}
		}
		for arg, newType := range newSet {
			if _, ok := oldSet[arg]; ok {
				continue
			}
			// A new REQUIRED argument breaks every existing caller, which omits
			// it. A new optional one does not, and is the additive way to grow
			// a command.
			if strings.HasSuffix(newType, "?") {
				additive = append(additive, fmt.Sprintf("optional argument added: %s.%s", name, arg))
			} else {
				breaking = append(breaking, fmt.Sprintf("required argument added: %s.%s", name, arg))
			}
		}
	}
	for name := range current.Commands {
		if _, ok := baseline.Commands[name]; !ok {
			additive = append(additive, "command added: "+name)
		}
	}

	if len(breaking) == 0 && len(additive) == 0 {
		if baseline.WSAPIVersion != current.WSAPIVersion {
			t.Errorf("wsapi version moved %s -> %s with no surface change recorded.\n"+
				"If the change is a value-semantics one, add it to valueSemanticsChanges\n"+
				"and refresh the baseline; a command diff cannot see it.",
				baseline.WSAPIVersion, current.WSAPIVersion)
		}
		return
	}

	oldMajor, oldMinor := majorMinor(t, baseline.WSAPIVersion+".0")
	newMajor, newMinor := majorMinor(t, current.WSAPIVersion+".0")

	sort.Strings(breaking)
	sort.Strings(additive)

	const refresh = "  GOMAXPROCS=2 go test -p 2 -run TestWSSurfaceChangesCarryTheRightBump ./tests/contract/ -update-ws-surface"

	switch {
	case len(breaking) > 0 && newMajor <= oldMajor:
		t.Errorf("the WebSocket surface lost or reshaped %d thing(s) while wsapi went %s -> %s.\n"+
			"A removal, rename or retype is a MAJOR bump — a client that sends the old\n"+
			"frame is answered with a protocol error it cannot anticipate.\n\n  %s\n\n"+
			"Either grow the command additively (a new optional argument, or a new command\n"+
			"beside the old one), or bump the major and refresh the baseline with:\n%s",
			len(breaking), baseline.WSAPIVersion, current.WSAPIVersion, strings.Join(breaking, "\n  "), refresh)
	case len(additive) > 0 && newMajor == oldMajor && newMinor <= oldMinor:
		t.Errorf("the WebSocket surface gained %d thing(s) while wsapi stayed at %s.\n"+
			"An addition is a MINOR bump — without one a client has no way to detect the\n"+
			"command it could now send.\n\n  %s\n\nBump the minor, then refresh with:\n%s",
			len(additive), baseline.WSAPIVersion, strings.Join(additive, "\n  "), refresh)
	default:
		t.Errorf("the WebSocket surface changed and wsapi moved %s -> %s correctly, but the\n"+
			"committed baseline is stale. Refresh it in this same commit:\n%s\n\n"+
			"breaking (%d):\n  %s\nadditive (%d):\n  %s",
			baseline.WSAPIVersion, current.WSAPIVersion, refresh,
			len(breaking), strings.Join(breaking, "\n  "),
			len(additive), strings.Join(additive, "\n  "))
	}
}

// wsAPIDoc is the slice of assets/wsapi.json this inventory reads. The file
// carries far more (envelope shape, heartbeat tuning, prose); none of it is a
// command a caller binds to, so none of it belongs in a surface diff.
type wsAPIDoc struct {
	Version  string `json:"version"`
	Commands []struct {
		Name string            `json:"name"`
		Args map[string]string `json:"args"`
	} `json:"commands"`
}

func buildWSSurface(t *testing.T) wsSurface {
	t.Helper()
	_, thisFile, _, _ := runtime.Caller(0)
	repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..")
	raw, err := os.ReadFile(filepath.Join(repoRoot, "assets", "wsapi.json")) //nolint:gosec // fixed asset path
	if err != nil {
		t.Fatalf("read wsapi.json: %v", err)
	}
	var doc wsAPIDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse wsapi.json: %v", err)
	}
	if len(doc.Commands) < 100 {
		t.Fatalf("wsapi.json lists %d commands, expected ≥100 — the inventory is reading the wrong shape", len(doc.Commands))
	}
	out := wsSurface{WSAPIVersion: doc.Version, Commands: make(map[string]string, len(doc.Commands))}
	for _, cmd := range doc.Commands {
		args := make([]string, 0, len(cmd.Args))
		for name, typ := range cmd.Args {
			args = append(args, name+":"+typ)
		}
		sort.Strings(args)
		out.Commands[cmd.Name] = strings.Join(args, ", ")
	}
	return out
}

// parseWSArgs turns the committed "arg:type, arg:type" spelling back into a
// map. The inventory stores a joined string rather than a list so the JSON
// stays readable as a diff.
func parseWSArgs(joined string) map[string]string {
	out := map[string]string{}
	if joined == "" {
		return out
	}
	for _, part := range strings.Split(joined, ", ") {
		name, typ, ok := strings.Cut(part, ":")
		if !ok {
			continue
		}
		out[name] = typ
	}
	return out
}

func wsSurfacePath(t *testing.T) string {
	t.Helper()
	_, thisFile, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(thisFile), "testdata", "ws_surface.json")
}

func writeWSSurface(t *testing.T, path string, s wsSurface) {
	t.Helper()
	raw, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		t.Fatalf("marshal ws surface: %v", err)
	}
	if err := os.WriteFile(path, append(raw, '\n'), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
