// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package wiring_pins

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestMatterSessionListerIsWiredFromBothManagers pins the composition
// root's half of the session-diagnostics surface.
//
// The endpoint is only as good as what feeds it, and it needs two
// sources that live in different packages: the operational manager for
// which sessions exist and when they last carried traffic, the
// subscription manager for what rides on them. Wire one and the endpoint
// still answers — with every session reporting zero subscriptions, which
// reads as "the controller is connected but receiving nothing". That is
// a real and alarming state, so reporting it falsely is worse than not
// reporting it.
//
// Leave the port unset entirely and the endpoint answers 503, "Matter
// bridge not enabled", on a daemon whose bridge is running.
func TestMatterSessionListerIsWiredFromBothManagers(t *testing.T) {
	t.Parallel()

	matter := collapseSpaces(readMatterComposition(t))
	if !strings.Contains(matter, "wiring.sessionLister = matterSessionLister{") {
		t.Error("wireMatterRuntime never builds a matterSessionLister — GET /matter/sessions answers 503 " +
			"on a daemon whose Matter bridge is running")
	}
	for _, source := range []string{"op: bundle.opMgr", "sub: bundle.subMgr"} {
		if !strings.Contains(matter, source) {
			t.Errorf("the session lister is not fed from %q — half the session picture is missing and the "+
				"endpoint reports it as fact", source)
		}
	}

	mount := collapseSpaces(readRestMount(t))
	if !strings.Contains(mount, "MatterSessionLister: d.matter.sessionLister") {
		t.Error("the REST router never receives the session lister, so the endpoint stays disabled " +
			"however well the bridge itself is wired")
	}
}

// TestMatterDiagnosticsSurfacesAreWired pins the other three diagnostic
// surfaces the same way, for the same reason: each answers 503 "Matter
// bridge not enabled" on a running bridge when its port is unset, which
// reads as a configuration problem rather than a wiring one.
func TestMatterDiagnosticsSurfacesAreWired(t *testing.T) {
	t.Parallel()

	matter := collapseSpaces(readMatterComposition(t))
	for _, built := range []struct{ what, expr string }{
		{"mDNS diagnostics", "wiring.mdnsReporter = matterMdnsReporter{"},
		{"endpoint inspector", "wiring.endpointInspector = matterEndpointInspector{"},
		{"ecosystem compatibility", "wiring.compatReporter = matterCompatibilityReporter{"},
	} {
		if !strings.Contains(matter, built.expr) {
			t.Errorf("wireMatterRuntime never builds the %s adapter", built.what)
		}
	}

	// The mDNS reporter must read the live advertiser rather than the
	// config: the two diverge exactly when discovery fails, which is the
	// only situation the endpoint exists for.
	if !strings.Contains(matter, "adv: bundle.advertiser") {
		t.Error("the mDNS reporter is not fed from the live advertiser — reporting the configured " +
			"advertisement would agree with itself in precisely the case that is broken")
	}
	// Compatibility needs both halves: fabrics say which ecosystems are
	// commissioned, the topology says what they are being shown.
	for _, half := range []string{"fabrics: mfs", "inspector: wiring.endpointInspector"} {
		if !strings.Contains(matter, half) {
			t.Errorf("the compatibility reporter is missing its %q source; with one half absent it "+
				"reports 'no problems' for a combination it cannot see", half)
		}
	}

	mount := collapseSpaces(readRestMount(t))
	for _, port := range []string{
		"MatterMdnsReporter:",
		"MatterEndpointInspector:",
		"MatterCompatibilityReporter:",
	} {
		if !strings.Contains(mount, port) {
			t.Errorf("the REST router never receives %s", port)
		}
	}
}

func readMatterComposition(t *testing.T) string {
	t.Helper()
	return readRepoFile(t, filepath.Join("cmd", "openccu-loom", "daemon_matter.go"))
}

func readRestMount(t *testing.T) string {
	t.Helper()
	return readRepoFile(t, filepath.Join("cmd", "openccu-loom", "daemon_rest_mount.go"))
}

func readRepoFile(t *testing.T, rel string) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root := filepath.Join(filepath.Dir(thisFile), "..", "..", "..")
	src, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(src)
}

// collapseSpaces folds runs of spaces and tabs to a single space so a
// pin matches the code rather than gofmt's column alignment, which
// shifts whenever a longer field name joins the same struct literal.
func collapseSpaces(src string) string {
	var b strings.Builder
	b.Grow(len(src))
	prevSpace := false
	for _, r := range src {
		if r == ' ' || r == '\t' {
			if !prevSpace {
				b.WriteByte(' ')
			}
			prevSpace = true
			continue
		}
		prevSpace = false
		b.WriteRune(r)
	}
	return b.String()
}
