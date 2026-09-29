// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package contract

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
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
		Lite          string   `json:"lite"`
		Architectures []string `json:"architectures"`
	} `json:"requires"`
	UI struct {
		Icon          string `json:"icon"`
		IconDark      string `json:"icon_dark"`
		SettingsURL   string `json:"settings_url"`
		SessionHeader bool   `json:"session_header"`
	} `json:"ui"`
	Runtime struct {
		Daemon    bool      `json:"daemon"`
		Needs     *[]string `json:"needs"`
		APIScopes []string  `json:"api_scopes"`
		Ports     []int     `json:"ports"`
		PortInfo  map[string]struct {
			Proto string            `json:"proto"`
			Label map[string]string `json:"label"`
		} `json:"port_info"`
		Note map[string]string `json:"note"`
	} `json:"runtime"`
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

// TestCCUAddonLiteManifestDeclaresRuntimePolicy pins the manifest's runtime
// declarations. openccu-lite applies a manifest as declared and a release
// that declares less than its predecessor loses what it dropped at the next
// update — a silently removed port closes its firewall switch at once. So
// the declared set is pinned here: the UI port (cross-checked against the
// rc.d listen address), the two callback ports a remote CCU would push to,
// and the Matter bridge port; every port carries a bilingual label, needs
// is declared empty (the daemon talks to occulited, never to an interface
// process, so its unit starts right after the network), and the note and
// the icons the Addons page shows exist. mDNS 5353 is deliberately absent:
// the box's always-on discovery firewall rules already accept multicast to
// the mDNS groups, and a declared port would add a closed-by-default
// switch standard mDNS never needs.
func TestCCUAddonLiteManifestDeclaresRuntimePolicy(t *testing.T) {
	t.Parallel()

	var m liteManifest
	if err := json.Unmarshal([]byte(readCCUAddonFile(t, "openccu-lite.json")), &m); err != nil {
		t.Fatalf("openccu-lite.json: %v", err)
	}

	if !m.Runtime.Daemon {
		t.Error("runtime.daemon is not set: a dead daemon then shows as Completed instead of Exited")
	}
	if m.Runtime.Needs == nil {
		t.Error("runtime.needs is undeclared: the unit then waits for rfd and hmipserver, which the daemon never talks to")
	} else if len(*m.Runtime.Needs) != 0 {
		t.Errorf("runtime.needs = %v, want [] — the daemon talks to occulited's API only", *m.Runtime.Needs)
	}
	if m.Requires.Lite == "" {
		t.Error("requires.lite is empty: older systems get no compatibility hint")
	}

	rc := readCCUAddonFile(t, "rc.d/openccu-loom")
	listen := regexp.MustCompile(`(?m)^export OPENCCU_LOOM_REST_LISTEN=:(\d+)$`).FindStringSubmatch(rc)
	if listen == nil {
		t.Fatal("rc.d/openccu-loom exports no OPENCCU_LOOM_REST_LISTEN")
	}
	uiPort, err := strconv.Atoi(listen[1])
	if err != nil {
		t.Fatalf("rc.d listen port %q: %v", listen[1], err)
	}

	wantPorts := []int{uiPort, 8120, 8129, 5540}
	gotPorts := append([]int(nil), m.Runtime.Ports...)
	sort.Ints(wantPorts)
	sort.Ints(gotPorts)
	if fmt.Sprint(gotPorts) != fmt.Sprint(wantPorts) {
		t.Errorf("runtime.ports = %v, want %v (UI port from rc.d, XML-RPC and BIN-RPC callbacks, Matter)", gotPorts, wantPorts)
	}
	for _, p := range m.Runtime.Ports {
		info, ok := m.Runtime.PortInfo[strconv.Itoa(p)]
		if !ok {
			t.Errorf("port %d has no port_info entry", p)
			continue
		}
		if info.Proto != "tcp" && info.Proto != "udp" {
			t.Errorf("port %d: proto = %q", p, info.Proto)
		}
		for _, lang := range []string{"de", "en"} {
			if strings.TrimSpace(info.Label[lang]) == "" {
				t.Errorf("port %d: label misses %s", p, lang)
			}
		}
	}
	for key := range m.Runtime.PortInfo {
		p, err := strconv.Atoi(key)
		if err != nil || !slices.Contains(m.Runtime.Ports, p) {
			t.Errorf("port_info %q names no declared port — openccu-lite refuses such a manifest", key)
		}
	}
	for _, lang := range []string{"de", "en"} {
		if strings.TrimSpace(m.Runtime.Note[lang]) == "" {
			t.Errorf("runtime.note misses %s", lang)
		}
	}

	// The add-on auto-onboarding (ADR 0077) reads the token occulited
	// mints from these scopes; a dropped declaration removes the token
	// file at the next update and the local central loses its
	// credential. backup and power stay out — occulited never grants
	// them to an add-on token, and declaring one is logged as refused.
	wantScopes := []string{"logs:read", "meta:write", "rpc:admin", "system:write"}
	gotScopes := append([]string(nil), m.Runtime.APIScopes...)
	sort.Strings(gotScopes)
	if strings.Join(gotScopes, ",") != strings.Join(wantScopes, ",") {
		t.Errorf("runtime.api_scopes = %v, want %v", gotScopes, wantScopes)
	}
	for _, s := range m.Runtime.APIScopes {
		if s == "backup" || s == "power" || s == "auth:admin" || s == "*" {
			t.Errorf("runtime.api_scopes declares %q, which occulited never grants an add-on", s)
		}
	}

	for field, rel := range map[string]string{"ui.icon": m.UI.Icon, "ui.icon_dark": m.UI.IconDark} {
		if rel == "" {
			t.Errorf("%s is empty: the Addons page and the menu then show no mark", field)
			continue
		}
		if _, err := os.Stat(filepath.Join("..", "..", "packaging", "ccu-addon", "ccu", rel)); err != nil {
			t.Errorf("%s = %q: not in the package (%v)", field, rel, err)
		}
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
