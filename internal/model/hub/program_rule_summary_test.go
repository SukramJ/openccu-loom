// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package hub

import (
	"sync"
	"testing"
)

// TestProgramRuleSummaryDefaultsEmpty verifies a freshly constructed Program
// reports empty rule summaries until SetRuleSummary is called — the state
// before the hub coordinator's first scan, or when a program has no rule.
func TestProgramRuleSummaryDefaultsEmpty(t *testing.T) {
	t.Parallel()
	p := NewProgram("main", "42", "TestProg", "", false, nil)
	cond, act := p.RuleSummary()
	if cond != "" || act != "" {
		t.Errorf("RuleSummary() = (%q, %q), want (\"\", \"\")", cond, act)
	}
}

// TestProgramSetRuleSummaryUpdatesBothFields verifies SetRuleSummary records
// both the condition and activity summaries, and that a later call
// overwrites the previous values rather than merging with them.
func TestProgramSetRuleSummaryUpdatesBothFields(t *testing.T) {
	t.Parallel()
	p := NewProgram("main", "42", "TestProg", "", false, nil)

	p.SetRuleSummary("Wohnzimmer >= 20.00", "Bücherregal := 1.00")
	if cond, act := p.RuleSummary(); cond != "Wohnzimmer >= 20.00" || act != "Bücherregal := 1.00" {
		t.Errorf("RuleSummary() = (%q, %q), want (%q, %q)", cond, act, "Wohnzimmer >= 20.00", "Bücherregal := 1.00")
	}

	// A later refresh (e.g. the program's rule was edited on the CCU)
	// overwrites the prior values — the latest scan wins.
	p.SetRuleSummary("Flur == 1.00", "")
	cond, act := p.RuleSummary()
	if cond != "Flur == 1.00" {
		t.Errorf("condition after second SetRuleSummary = %q, want %q", cond, "Flur == 1.00")
	}
	if act != "" {
		t.Errorf("activity after second SetRuleSummary = %q, want empty (not merged with the old value)", act)
	}
}

// TestProgramSetRuleSummaryConcurrentAccess exercises SetRuleSummary and
// RuleSummary from multiple goroutines to pin the documented "safe to call
// on every refresh" contract. Run with -race to catch a regression that
// drops the mutex.
func TestProgramSetRuleSummaryConcurrentAccess(t *testing.T) {
	t.Parallel()
	p := NewProgram("main", "42", "TestProg", "", false, nil)

	var wg sync.WaitGroup
	for range 20 {
		wg.Add(2)
		go func() {
			defer wg.Done()
			p.SetRuleSummary("cond", "act")
		}()
		go func() {
			defer wg.Done()
			_, _ = p.RuleSummary()
		}()
	}
	wg.Wait()

	cond, act := p.RuleSummary()
	if cond != "cond" || act != "act" {
		t.Errorf("RuleSummary() = (%q, %q), want (%q, %q)", cond, act, "cond", "act")
	}
}
