// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package contract

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// liteManifest is the part of openccu-lite's add-on manifest this package
// declares. occulited reads openccu-lite.json from the root of an add-on
// tarball; without one it falls back to its catalogue's own entry.
type liteManifest struct {
	Format  int    `json:"format"`
	ID      string `json:"id"`
	Release struct {
		GitHub string `json:"github"`
		Asset  string `json:"asset"`
	} `json:"release"`
	Requires struct {
		Architectures []string `json:"architectures"`
	} `json:"requires"`
	UI struct {
		SettingsURL   string `json:"settings_url"`
		SessionHeader bool   `json:"session_header"`
	} `json:"ui"`
}

func readCCUAddonFile(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "packaging", "ccu-addon", "ccu", rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(b)
}

// TestCCUAddonLiteManifestMatchesThePackage pins the manifest to the files it
// describes, so a renamed add-on id, config page or tarball cannot leave the
// manifest pointing at something that no longer exists.
func TestCCUAddonLiteManifestMatchesThePackage(t *testing.T) {
	t.Parallel()

	var m liteManifest
	if err := json.Unmarshal([]byte(readCCUAddonFile(t, "openccu-lite.json")), &m); err != nil {
		t.Fatalf("openccu-lite.json: %v", err)
	}
	if m.Format != 1 {
		t.Errorf("format = %d, want 1", m.Format)
	}

	rc := readCCUAddonFile(t, "rc.d/openccu-loom")
	id := regexp.MustCompile(`(?m)^ADDON_ID=(\S+)$`).FindStringSubmatch(rc)
	if id == nil {
		t.Fatal("rc.d/openccu-loom declares no ADDON_ID")
	}
	if m.ID != id[1] {
		t.Errorf("id = %q, the rc.d script's ADDON_ID is %q", m.ID, id[1])
	}

	cfg := regexp.MustCompile(`(?m)echo "Config-Url: ([^"]+)"`).FindStringSubmatch(rc)
	if cfg == nil {
		t.Fatal("rc.d/openccu-loom's info names no Config-Url")
	}
	if want := strings.ReplaceAll(cfg[1], "${ADDON_ID}", id[1]); m.UI.SettingsURL != want {
		t.Errorf("ui.settings_url = %q, the add-on's config page is %q", m.UI.SettingsURL, want)
	}

	build, err := os.ReadFile("../../script/build_ccu_addon.sh")
	if err != nil {
		t.Fatalf("read build script: %v", err)
	}
	tarball := regexp.MustCompile(`TARBALL="\$OUT/([^"]+)"`).FindStringSubmatch(string(build))
	if tarball == nil {
		t.Fatal("script/build_ccu_addon.sh names no TARBALL")
	}
	if want := strings.ReplaceAll(tarball[1], "${VERSION}", "{version}"); m.Release.Asset != want {
		t.Errorf("release.asset = %q, the build writes %q", m.Release.Asset, want)
	}
	if m.Release.GitHub != "SukramJ/openccu-loom" {
		t.Errorf("release.github = %q", m.Release.GitHub)
	}

	// The architectures the update script installs a binary for, spelled as
	// `uname -m` reports them (the first name of each case pattern).
	update := readCCUAddonFile(t, "update_script")
	cases := regexp.MustCompile(`(?m)^\s+([a-z0-9_]+)(?:\|[a-z0-9_|]+)?\)\s+ADDON_BIN=`).FindAllStringSubmatch(update, -1)
	arches := make([]string, 0, len(cases))
	for _, c := range cases {
		arches = append(arches, c[1])
	}
	got := append([]string(nil), m.Requires.Architectures...)
	sort.Strings(arches)
	sort.Strings(got)
	if strings.Join(got, ",") != strings.Join(arches, ",") {
		t.Errorf("requires.architectures = %v, the update script installs for %v", got, arches)
	}
}

// TestCCUAddonLiteManifestNeedsNoSessionInTheURL holds the reason the
// manifest sets session_header. Without it openccu-lite hands the add-on's
// page the operator's session in the URL (`?sid=`, the old CCU convention)
// and warns that the page receives a session alias. No page of this add-on
// reads a session at all: the config page is a static landing card. The
// second half is the precondition — a page that starts reading `sid` from
// the URL would need the old handover back.
func TestCCUAddonLiteManifestNeedsNoSessionInTheURL(t *testing.T) {
	t.Parallel()

	var m liteManifest
	if err := json.Unmarshal([]byte(readCCUAddonFile(t, "openccu-lite.json")), &m); err != nil {
		t.Fatalf("openccu-lite.json: %v", err)
	}
	if !m.UI.SessionHeader {
		t.Error("ui.session_header is not set: openccu-lite then passes the session in the page URL and warns about it")
	}

	pages, err := filepath.Glob("../../packaging/ccu-addon/ccu/www/*")
	if err != nil || len(pages) == 0 {
		t.Fatalf("no add-on pages found (err %v)", err)
	}
	sid := regexp.MustCompile(`\bsid\b`)
	for _, p := range pages {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("read %s: %v", p, err)
		}
		if sid.Match(b) {
			t.Errorf("%s mentions sid: a page that reads the session from the URL contradicts ui.session_header", filepath.Base(p))
		}
	}
}
