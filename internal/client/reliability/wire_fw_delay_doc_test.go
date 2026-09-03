// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package reliability

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmerr"
	"github.com/SukramJ/openccu-loom/pkg/hmreliability"
)

// wireFwReadComments returns the comment text of a Go file, lower-cased, with
// every run of whitespace collapsed so a claim can be matched across the line
// wrapping of a doc comment.
func wireFwReadComments(t *testing.T, path string) string {
	t.Helper()

	raw, err := os.ReadFile(path) //nolint:gosec // fixed in-repo path, test-only
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var b strings.Builder
	for _, line := range strings.Split(string(raw), "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "//") {
			continue
		}
		b.WriteString(strings.TrimPrefix(trimmed, "//"))
		b.WriteString(" ")
	}
	return strings.Join(strings.Fields(strings.ToLower(b.String())), " ")
}

// wireFwDefaultsPath is the constants file the two special delays live in. It
// sits outside this package, so the guard reaches it by repo-relative path.
func wireFwDefaultsPath() string {
	return filepath.Join("..", "..", "..", "pkg", "hmreliability", "defaults.go")
}

// TestWireFwSpecialDelayDocsMatchTheFirmware pins what the two fixed
// recovery windows are allowed to claim about the CCU.
//
// Neither trigger is what our comments said. Fault -8 ("not enough DutyCycle
// free") is raised at exactly one place in the whole CCU source tree —
// OpenCCU-Base src/rfd/RFDevice.cpp:1492, inside RFDevice::UpdateFirmware —
// and never on a setValue/putParamset path; the gate is an instantaneous
// airtime-budget predicate (src/rfd/BidcosInterfaceConcentrator.cpp:1045)
// with no time dimension at all. Fault -10 exists only on the HmIP side and
// is emitted behind a reachability test, not behind a timer, so it is not a
// window that "the CCU enforces".
//
// The 40 s and 5 s themselves are ported witness values. No readable source
// carries a recovery law for either condition, so the honest record is a
// comment that says so — hence the required "unverified" token.
func TestWireFwSpecialDelayDocsMatchTheFirmware(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		path     string
		banned   []string
		required []string
	}{
		{
			name: "defaults.go",
			path: wireFwDefaultsPath(),
			banned: []string{
				"when a ccu rejects a command with a duty-cycle",
				"reports a transmission-pending stall",
				"duty-cycle rejection is absorbed by",
			},
			required: []string{
				"rfdevice.cpp:1492",
				"updatefirmware",
				"unverified",
			},
		},
		{
			name: "retry.go",
			path: "retry.go",
			banned: []string{
				"the ccu itself enforces these windows",
				"so the rf window has time to drain",
			},
			required: []string{
				"rfdevice.cpp:1492",
				"unverified",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			doc := wireFwReadComments(t, tc.path)
			for _, b := range tc.banned {
				if strings.Contains(doc, b) {
					t.Errorf("%s: comments still claim %q.\n"+
						"  Fault -8 is produced only by RFDevice::UpdateFirmware"+
						" (OpenCCU-Base src/rfd/RFDevice.cpp:1492) behind an instantaneous"+
						" airtime-budget test, never by a value write; fault -10 is HmIP-only"+
						" and is emitted behind a reachability test, not a timer.", tc.path, b)
				}
			}
			for _, r := range tc.required {
				if !strings.Contains(doc, r) {
					t.Errorf("%s: comments do not mention %q — a fixed delay must name the"+
						" firmware path its trigger really has, and say in those words which"+
						" part is unverified.", tc.path, r)
				}
			}
		})
	}
}

// TestWireFwSpecialDelayBehaviourIsUnchanged pins the delays themselves.
//
// The two constants stay as they are: no readable CCU source carries a
// recovery interval for either condition, so there is no measured number to
// replace them with, and -10 does still reach us from paths where a short
// retry is defensible. Changing them is a decision that needs evidence, and
// this pin makes such a change deliberate rather than incidental.
func TestWireFwSpecialDelayBehaviourIsUnchanged(t *testing.T) {
	t.Parallel()

	if hmreliability.RetryDutyCycleDelay != 40*time.Second {
		t.Errorf("RetryDutyCycleDelay = %v, want 40s", hmreliability.RetryDutyCycleDelay)
	}
	if hmreliability.RetryTransmissionPendingDelay != 5*time.Second {
		t.Errorf("RetryTransmissionPendingDelay = %v, want 5s", hmreliability.RetryTransmissionPendingDelay)
	}

	r := NewRetrier(RetryConfig{MaxAttempts: 3})
	cases := []struct {
		code int
		want time.Duration
	}{
		{int(hmerr.XMLRPCFaultDutyCycle), hmreliability.RetryDutyCycleDelay},
		{int(hmerr.XMLRPCFaultTransmissionPending), hmreliability.RetryTransmissionPendingDelay},
		{int(hmerr.XMLRPCFaultGeneral), 0},
		{int(hmerr.XMLRPCFaultDeviceOutOfRange), 0},
	}
	for _, tc := range cases {
		err := error(&hmerr.XMLRPCFault{Code: tc.code, Message: "fault"})
		if got := r.specialDelayFor(err); got != tc.want {
			t.Errorf("specialDelayFor(fault %d) = %v, want %v", tc.code, got, tc.want)
		}
	}
	if got := r.specialDelayFor(errors.New("plain")); got != 0 {
		t.Errorf("specialDelayFor(non-fault) = %v, want 0", got)
	}
}
