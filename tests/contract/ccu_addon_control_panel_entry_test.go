// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package contract

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// controlPanelHelper is the CCU platform helper that maintains
// /etc/config/hm_addons.cfg — the file the WebUI's control panel
// (config/control_panel.cgi) renders one button per entry from. It is the
// ONLY helper name that exists on CCU3 / OpenCCU firmware;
// invoking any other name leaves the add-on without its control-panel tile,
// and because the call is guarded by an `-x` probe the miss is silent.
const controlPanelHelper = "/bin/updateAddonConfig.tcl"

// TestCCUAddonRegistersControlPanelEntry pins how the add-on claims its tile
// in the CCU's "Systemsteuerung": the platform helper by its real name, plus
// the underlying HomeMatic Tcl API as the fallback for firmware that predates
// the helper. Install and uninstall are checked together so a package can
// never register a tile it cannot remove again.
func TestCCUAddonRegistersControlPanelEntry(t *testing.T) {
	t.Parallel()

	updateScript := readAddonFile(t, "packaging/ccu-addon/ccu/update_script")
	rcScript := readAddonFile(t, "packaging/ccu-addon/ccu/rc.d/openccu-loom")

	cases := []struct {
		file    string
		body    string
		needles []string
	}{
		{
			file: "update_script",
			body: updateScript,
			needles: []string{
				controlPanelHelper,
				// The registration itself: add by id, pointing at the
				// add-on's own settings CGI under the /addons alias.
				"-a ${ADDON_ID}",
				"-url /addons/${ADDON_ID}/config.cgi",
				// Fallback for firmware without the helper.
				"::HomeMatic::Addon::AddConfigPage",
			},
		},
		{
			file: "rc.d/openccu-loom",
			body: rcScript,
			needles: []string{
				controlPanelHelper,
				"-d ${ADDON_ID}",
				// RemoveConfigPage is an OpenCCU addition, so
				// the fallback must probe for it before calling it.
				"info procs ::HomeMatic::Addon::RemoveConfigPage",
				"::HomeMatic::Addon::RemoveConfigPage",
			},
		},
	}

	for _, tc := range cases {
		if strings.Contains(tc.body, "update_hm_addons") {
			t.Errorf("%s references update_hm_addons.tcl, which exists on no CCU firmware — use %s",
				tc.file, controlPanelHelper)
		}
		for _, needle := range tc.needles {
			if !strings.Contains(tc.body, needle) {
				t.Errorf("%s: missing %q", tc.file, needle)
			}
		}
	}
}

// TestCCUAddonUpdateScriptWarnsWithoutControlPanelHelper verifies the miss is
// LOUD: on firmware that offers neither the helper nor a Tcl interpreter the
// install still succeeds (the daemon does not need the tile), but it says so
// rather than leaving the operator with a silently tile-less install — the
// exact failure mode that hid the wrong helper name.
func TestCCUAddonUpdateScriptWarnsWithoutControlPanelHelper(t *testing.T) {
	t.Parallel()

	// The script probes both paths absolutely, so PATH stubs cannot fake
	// them. Only assert the warning when the host genuinely has neither.
	if fileExists(controlPanelHelper) || fileExists("/bin/tclsh") {
		t.Skip("host provides a control-panel registration path; warning branch not reachable")
	}

	scriptPath, err := filepath.Abs("../../packaging/ccu-addon/ccu/update_script")
	if err != nil {
		t.Fatalf("resolve update_script path: %v", err)
	}
	cmd := exec.Command("sh", scriptPath, "HM-RASPBERRYMATIC")
	cmd.Dir = writeUpdateScriptFixtures(t)
	cmd.Env = append(os.Environ(), "PATH="+writeUpdateScriptStubs(t)+":"+os.Getenv("PATH"))

	out, runErr := cmd.CombinedOutput()
	if code := exitCodeOf(t, runErr); code != 0 {
		t.Fatalf("exit code = %d, want 0 (a missing tile must not fail the install)\noutput:\n%s", code, out)
	}
	if !strings.Contains(string(out), "control panel entry") {
		t.Errorf("expected a control-panel warning in the install output, got:\n%s", out)
	}
}

// readAddonFile reads a repo-relative packaging file, failing the test when
// it is missing — a rename must surface here rather than as a vacuous pass.
func readAddonFile(t *testing.T, rel string) string {
	t.Helper()
	path := filepath.Join(repoRoot(t), rel)
	b, err := os.ReadFile(path) //nolint:gosec // fixed repo-relative packaging path
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(b)
}

// fileExists reports whether path is present, treating any stat error as
// absent — the caller only decides whether a branch is reachable.
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
