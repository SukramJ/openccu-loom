// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

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

	matter := readMatterComposition(t)
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

	mount := readRestMount(t)
	if !strings.Contains(mount, "MatterSessionLister: d.matter.sessionLister") {
		t.Error("the REST router never receives the session lister, so the endpoint stays disabled " +
			"however well the bridge itself is wired")
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
