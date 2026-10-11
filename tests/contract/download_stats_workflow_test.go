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

// TestDownloadStatsWorkflowRecordsEveryAddon pins the download-stats workflow
// to the inputs it samples. The workflow is the only writer of the `stats`
// branch and runs unattended, so nothing else notices when it silently stops
// covering an add-on: the Home Assistant analytics row for a new add-on is
// simply never written, and the gap looks like zero installs. The slug list
// therefore comes from packaging/ha-addon/*/config.yaml, where an add-on has
// to be declared anyway, and the workflow must pass each one.
//
// The same test holds the shape that keeps the workflow safe to run daily:
// a schedule, the script's own unit test run before the sample, and no write
// permission beyond `contents` (the branch push).
func TestDownloadStatsWorkflowRecordsEveryAddon(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile("../../.github/workflows/download-stats.yml")
	if err != nil {
		t.Fatalf("read workflow: %v", err)
	}
	workflow := string(raw)

	for _, want := range []string{
		"schedule:",
		"cron:",
		"workflow_dispatch:",
		"script/test_release_download_snapshot.py",
		"script/release_download_snapshot.py",
	} {
		if !strings.Contains(workflow, want) {
			t.Errorf("workflow lacks %q", want)
		}
	}

	slugs := addonSlugs(t)
	if len(slugs) == 0 {
		t.Fatal("no add-on slugs found under packaging/ha-addon — the guard would pass vacuously")
	}
	for _, slug := range slugs {
		if !strings.Contains(workflow, "--addon-slug "+slug+" ") && !strings.Contains(workflow, "--addon-slug "+slug+"\n") {
			t.Errorf("workflow does not sample add-on %q: add `--addon-slug %s` to the snapshot step", slug, slug)
		}
	}

	writes := regexp.MustCompile(`(?m)^\s+([a-z-]+):\s*write\s*$`).FindAllStringSubmatch(workflow, -1)
	if len(writes) == 0 {
		t.Error("workflow grants no write permission, yet it has to push the stats branch")
	}
	for _, m := range writes {
		if m[1] != "contents" {
			t.Errorf("workflow grants %s: write; only contents: write is needed for the branch push", m[1])
		}
	}
}

// addonSlugs reads the `slug:` line of every add-on config under
// packaging/ha-addon. Home Assistant derives the analytics key from this slug,
// so it is the value the workflow has to pass — not the directory name, which
// happens to match today but is not what the export keys on.
func addonSlugs(t *testing.T) []string {
	t.Helper()
	configs, err := filepath.Glob("../../packaging/ha-addon/*/config.yaml")
	if err != nil {
		t.Fatalf("glob add-on configs: %v", err)
	}
	slugLine := regexp.MustCompile(`(?m)^slug:\s*"?([A-Za-z0-9_-]+)"?\s*$`)
	slugs := make([]string, 0, len(configs))
	for _, cfg := range configs {
		raw, err := os.ReadFile(cfg)
		if err != nil {
			t.Fatalf("read %s: %v", cfg, err)
		}
		m := slugLine.FindStringSubmatch(string(raw))
		if m == nil {
			t.Fatalf("%s carries no slug: line", cfg)
		}
		slugs = append(slugs, m[1])
	}
	return slugs
}
