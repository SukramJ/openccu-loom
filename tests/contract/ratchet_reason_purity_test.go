// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package contract

import (
	"regexp"
	"testing"
)

// deferralPattern matches justifications that defer instead of decide:
// "no surface consumes it yet", "kept for now", "wired later", "TBD".
// A ratchet entry carrying one of these is not a classification — it is
// the unclassified state wearing a reason's clothes.
var deferralPattern = regexp.MustCompile(
	`(?i)\byet\b|\bfor now\b|\bcoming (soon|later)\b|\bwired later\b|\bimplemented later\b|\btbd\b|\btodo\b`,
)

// TestRatchetReasonsAreNotDeferrals enforces textually what the ratchet
// headers already demand in prose: an entry in a declared-silence list
// says someone looked and decided the silence is CORRECT — not that the
// work is pending. Both wiring ratchets and the event ratchet carried
// exactly such entries ("recovery telemetry; no surface consumes it
// yet") for months, quoting the forbidden phrase verbatim under a
// header that forbade it. Prose rules without a guard become
// decoration within a release; this is the guard.
//
// A pending decision belongs in wiringSeamsUnderInvestigation (which
// exists to be emptied), in the deep-audit backlog, or in the change
// that resolves it — never in a verified-silence list.
func TestRatchetReasonsAreNotDeferrals(t *testing.T) {
	t.Parallel()

	ratchets := map[string]map[string]string{
		"eventsWithoutSubscriber":    eventsWithoutSubscriber,
		"wiringSettersWithoutCaller": wiringSettersWithoutCaller,
	}
	for listName, entries := range ratchets {
		for entry, reason := range entries {
			if m := deferralPattern.FindString(reason); m != "" {
				t.Errorf("%s[%q] justifies the silence with a deferral (%q in %q) — "+
					"decide the seam (wire it, delete it, or state why the silence is correct) "+
					"instead of postponing it inside a verified list",
					listName, entry, m, reason)
			}
		}
	}
}
