// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package contract

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestGoTestTimeoutIsOneValue pins the per-package `go test -timeout` ceiling
// to a single value across the Makefile and every workflow that states it.
//
// The ceiling exists because Go's silent 10-minute default sat ~17 % above the
// slowest package in this module (cmd/openccu-loom, ~500 s under -race on a
// developer box), and crossing it does not report "slow suite": go test panics
// and names whichever test happened to be running, which is usually a
// bystander — see #829, where a wedged broker shutdown in a cleanup presented
// for weeks as an unrelated test hanging.
//
// A ceiling only does that job if local and CI agree on it. If the Makefile
// says 20m and CI still inherits 10m, a developer's green run proves nothing
// about the runner, and the drift is invisible: both configurations are
// individually valid YAML and make. So the value is pinned here rather than
// left to two files staying in sync by habit.
//
// This test does not judge the number — it judges that there is exactly one of
// them. The argument for 20m, and the rule that it is not to be raised just
// because it tripped, live at the point of use in ci.yml's test-shard job.
func TestGoTestTimeoutIsOneValue(t *testing.T) {
	t.Parallel()

	makefile := readRepoFile(t, "Makefile")
	makeRe := regexp.MustCompile(`(?m)^GO_TEST_TIMEOUT\s*\?=\s*(\S+)\s*$`)
	m := makeRe.FindStringSubmatch(makefile)
	if m == nil {
		t.Fatal("Makefile: no `GO_TEST_TIMEOUT ?= ...` assignment found — " +
			"the per-package go test ceiling must stay declared in one place")
	}
	want := m[1]

	// Every `go test` line in the Makefile that runs plain unit/contract
	// packages must carry the variable rather than a literal. Targets with
	// their own argued ceiling (scenario, e2e, chiptool, fuzz) are excluded by
	// the build tag they pass, and the bench target runs -bench, not tests.
	for _, line := range strings.Split(makefile, "\n") {
		if !strings.Contains(line, "$(GO) test") {
			continue
		}
		if strings.Contains(line, "-tags=") || strings.Contains(line, "-tags ") ||
			strings.Contains(line, "-bench") || strings.Contains(line, "-fuzz") ||
			strings.Contains(line, "-list") || strings.Contains(line, "-timeout=300s") {
			continue
		}
		if !strings.Contains(line, "-timeout=$(GO_TEST_TIMEOUT)") {
			t.Errorf("Makefile: `go test` without the shared ceiling:\n  %s\n"+
				"add -timeout=$(GO_TEST_TIMEOUT) so this run cannot silently "+
				"inherit Go's 10-minute default", strings.TrimSpace(line))
		}
	}

	// The workflows that run the module's tests declare the same value.
	envRe := regexp.MustCompile(`(?m)^\s*GO_TEST_TIMEOUT:\s*"?([^"\s]+)"?\s*$`)
	for _, wf := range []string{"ci.yml", "nightly.yml"} {
		body := readRepoFile(t, filepath.Join(".github", "workflows", wf))
		w := envRe.FindStringSubmatch(body)
		if w == nil {
			t.Errorf("%s: no GO_TEST_TIMEOUT in env — this workflow runs the "+
				"module's tests and would inherit Go's 10-minute default", wf)
			continue
		}
		if w[1] != want {
			t.Errorf("%s: GO_TEST_TIMEOUT=%s, Makefile says %s — a local run and "+
				"CI must agree on when a package counts as wedged", wf, w[1], want)
		}
		// Declaring the variable is not enough: an invocation that never
		// references it is the same silent default with extra steps.
		for _, line := range strings.Split(body, "\n") {
			trimmed := strings.TrimSpace(line)
			if !strings.HasPrefix(trimmed, "go test") && !strings.Contains(trimmed, "run: go test") {
				continue
			}
			if strings.Contains(trimmed, "-tags=") || strings.Contains(trimmed, "-bench") ||
				strings.Contains(trimmed, "-list") || strings.Contains(trimmed, "-fuzz") {
				continue
			}
			if !strings.Contains(trimmed, "GO_TEST_TIMEOUT") {
				t.Errorf("%s: `go test` without the shared ceiling:\n  %s", wf, trimmed)
			}
		}
	}
}
