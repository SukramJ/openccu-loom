// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package contract

// spa_visual_regression_guard_test.go — keeps the SPA screenshot suite able
// to see drift.
//
// The visual suite has one failure mode that a green run cannot report,
// because a green run is the symptom: when the comparison budget is wider
// than the change, every baseline silently keeps describing a version of the
// SPA that no longer exists.
//
// It happened here. With a budget of 2 % of a 1280x800 viewport — 20 480
// pixels — every one of the committed baselines had drifted from what the
// code rendered, by 2 600 to 12 100 pixels each: enough to move the whole
// navigation sidebar, never enough to fail. The refresh command could not
// repair it either, because `--update-snapshots` without an explicit mode
// resolves to `changed`, and `changed` rewrites a baseline only when the
// comparison FAILED. A sub-budget change therefore left the old PNG on disk
// and reported success, which reads exactly like "nothing changed".
//
// Both halves are pinned here rather than in the suite itself: a test inside
// the suite would be scored by the same budget it is trying to constrain.
//
// What is deliberately NOT pinned here: that every screenshot assertion has
// a committed baseline. Playwright already fails a run whose baseline is
// missing (a soft error out of `handleMissing`), so a guard for it would be
// decoration.

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// maxScreenshotPixelBudget caps `expect.toHaveScreenshot.maxDiffPixels` in
// the Playwright config.
//
// The number is empirical, not zeal. Two consecutive container runs of all
// 37 screenshot tests against freshly written baselines each reported 0
// differing pixels, so the floor is exact reproduction — while renaming a
// single table header from "Source" to "Source (probe)" cost 90 pixels.
// Between those two measurements there is no room for a comfortable margin:
// any budget worth calling a budget already hides a changed label.
//
// Raising it is how the guard dies. If a change genuinely renders different
// pixels, refresh the baseline — that is what the refresh command is for.
const maxScreenshotPixelBudget = 0

// playwrightConfigPath and spaPackageJSONPath locate the two files that
// together decide whether the visual suite can see drift at all.
const (
	playwrightConfigPath = "assets/ui/playwright.config.ts"
	spaPackageJSONPath   = "assets/ui/package.json"
)

var (
	maxDiffPixelsRe = regexp.MustCompile(`maxDiffPixels\s*:\s*(\d+)`)
	pixelRatioRe    = regexp.MustCompile(`maxDiffPixelRatio\s*:\s*([0-9.]+)`)
)

// stripLineComments removes `//` comments so the settings below are read from
// the configuration itself. The config explains the budget in prose that
// names the rejected setting, and a plain substring search cannot tell an
// explanation from a declaration.
func stripLineComments(src string) string {
	lines := strings.Split(src, "\n")
	for i, line := range lines {
		if idx := strings.Index(line, "//"); idx >= 0 {
			lines[i] = line[:idx]
		}
	}
	return strings.Join(lines, "\n")
}

// TestScreenshotComparisonBudgetIsTightEnoughToSeeDrift asserts the visual
// suite compares against an exact-match pixel budget.
//
// A ratio-based budget is rejected outright: it scales with the viewport,
// which is precisely backwards. The thing being detected — a shifted
// element, a changed label, a dropped card — has a size in pixels that does
// not grow when the viewport does, so a percentage silently buys more
// blindness on every larger surface.
func TestScreenshotComparisonBudgetIsTightEnoughToSeeDrift(t *testing.T) {
	t.Parallel()

	cfg := stripLineComments(readRepoFile(t, playwrightConfigPath))

	if m := pixelRatioRe.FindStringSubmatch(cfg); m != nil {
		t.Errorf("%s sets maxDiffPixelRatio: %s — a relative budget grows with the "+
			"viewport while the drift it must catch does not. Use "+
			"maxDiffPixels: %d instead.",
			playwrightConfigPath, m[1], maxScreenshotPixelBudget)
	}

	m := maxDiffPixelsRe.FindStringSubmatch(cfg)
	if m == nil {
		t.Fatalf("%s sets no maxDiffPixels under expect.toHaveScreenshot; without an "+
			"explicit budget the suite falls back to Playwright's default and the "+
			"budget stops being a reviewed decision", playwrightConfigPath)
	}
	budget, err := strconv.Atoi(m[1])
	if err != nil {
		t.Fatalf("maxDiffPixels in %s is not a number: %q", playwrightConfigPath, m[1])
	}
	if budget > maxScreenshotPixelBudget {
		t.Errorf("%s allows %d differing pixels per screenshot; renaming one table "+
			"header costs 90, so a budget in that range hides exactly the change a "+
			"reviewer would want to see. Refresh the baseline instead of widening "+
			"past %d", playwrightConfigPath, budget, maxScreenshotPixelBudget)
	}
}

// TestBaselineRefreshScriptRewritesEveryBaseline asserts the documented
// refresh command passes an explicit `--update-snapshots=all`.
//
// Without the explicit mode Playwright uses `changed`, which rewrites only
// the baselines whose comparison failed. Every baseline that drifted but
// stayed inside the budget survives the refresh untouched — so the operator
// runs the documented command, sees it succeed, and keeps the stale PNG.
func TestBaselineRefreshScriptRewritesEveryBaseline(t *testing.T) {
	t.Parallel()

	raw := readRepoFile(t, spaPackageJSONPath)
	var pkg struct {
		Scripts map[string]string `json:"scripts"`
	}
	if err := json.Unmarshal([]byte(raw), &pkg); err != nil {
		t.Fatalf("parse %s: %v", spaPackageJSONPath, err)
	}

	const script = "e2e:update"
	cmd, ok := pkg.Scripts[script]
	if !ok {
		t.Fatalf("%s declares no %q script — the refresh path must stay one documented "+
			"command, not folklore", spaPackageJSONPath, script)
	}
	if !strings.Contains(cmd, "--update-snapshots=all") {
		t.Errorf("%s script %q is %q; it must pass --update-snapshots=all. The bare flag "+
			"resolves to the `changed` mode, which keeps every baseline whose drift fits "+
			"inside the comparison budget", spaPackageJSONPath, script, cmd)
	}
}
