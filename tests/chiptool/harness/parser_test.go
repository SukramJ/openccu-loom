// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package harness

import "testing"

// chip-tool's own stage lines, in the order a commissioning prints them.
// The surrounding log decoration (timestamp, pid, module tag) is irrelevant
// to the predicates, which match on the phrase alone -- so the fixtures
// carry a representative prefix rather than a pinned one.
const (
	lineSecurePairing = "[1788628785.724] [5822:5824] [CTL] Secure Pairing Success\n" +
		"[1788628785.724] [5822:5824] [CTL] CASE establishment successful\n"
	linePairing = "[1788628770.101] [5822:5824] [CTL] Pairing Success\n" +
		"[1788628770.101] [5822:5824] [CTL] PASE establishment successful\n"
	lineCommissioned = "[1788628790.900] [5822:5824] [CTL] Device commissioning completed with success\n"

	// The controller prints this for a finished attempt whatever its
	// outcome -- CHIPDeviceController.cpp:2196 renders "success" or the
	// error string into the same line -- so it is a completion marker, not
	// a success marker.
	lineControllerComplete = "[1788628790.901] [5822:5824] [CTL] Commissioning complete for node ID 0x0000000000001234: " +
		"src/lib/address_resolve/AddressResolve_DefaultImpl.cpp:124: CHIP Error 0x00000032: Timeout\n"
)

// TestPairingSuccessRequiresCommissioningComplete is the regression this
// file exists for. chip-tool prints "Pairing Success" when PASE alone has
// finished, and the controller prints "Commissioning complete for node ID
// …" for a failed attempt as readily as a successful one. A predicate that
// accepted either reported a green suite for a bridge that never reached an
// operational session -- the exact defect the chip-tool guard is meant to
// catch, made invisible by the guard's own parser.
func TestPairingSuccessRequiresCommissioningComplete(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		out  string
		want bool
	}{
		{"empty", "", false},
		{"pase only", linePairing, false},
		{"through CASE but not commissioned", linePairing + lineSecurePairing, false},
		{
			// The failure mode go-fabric shipped: PASE and CASE both fine,
			// the fabric installed, then operational discovery times out.
			name: "controller completion line reporting a failure",
			out:  linePairing + lineSecurePairing + lineControllerComplete,
			want: false,
		},
		{"fully commissioned", linePairing + lineSecurePairing + lineCommissioned, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := PairingSuccess(tc.out); got != tc.want {
				t.Errorf("PairingSuccess() = %v, want %v\nstage: %s\ninput:\n%s",
					got, tc.want, HandshakeStage(tc.out), tc.out)
			}
		})
	}
}

// TestPASEEstablishedIsNotConfusedByTheCASELine pins the substring trap.
// "Pairing Success" is a substring of "Secure Pairing Success", so matching
// on it cannot tell the PASE stage from the CASE stage. Both predicates key
// on the companion lines instead, which share no prefix.
func TestPASEEstablishedIsNotConfusedByTheCASELine(t *testing.T) {
	t.Parallel()

	if PASEEstablished(lineSecurePairing) {
		t.Error("PASEEstablished() matched the CASE stage lines alone — " +
			"the predicate is keying on a substring shared with \"Secure Pairing Success\"")
	}
	if !PASEEstablished(linePairing) {
		t.Error("PASEEstablished() did not match a completed PASE handshake")
	}
	if !PASEEstablished(linePairing + lineSecurePairing + lineCommissioned) {
		t.Error("PASEEstablished() did not match a full commissioning, which passes through PASE")
	}
}

// TestHandshakeStageNamesTheFurthestStage keeps the diagnostic honest: a
// failure message that names the wrong stage sends the next reader to the
// wrong subsystem.
func TestHandshakeStageNamesTheFurthestStage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		out  string
		want string
	}{
		{"nothing", "", "PASE not established"},
		{"pase only", linePairing, "PASE established, CASE not reached"},
		{"through CASE", linePairing + lineSecurePairing, "CASE established, commissioning did not complete"},
		{"full", linePairing + lineSecurePairing + lineCommissioned, "commissioning complete"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := HandshakeStage(tc.out); got != tc.want {
				t.Errorf("HandshakeStage() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestPairingFailedMarkers covers the negative-path predicate the
// wrong-passcode test leans on.
func TestPairingFailedMarkers(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		out  string
		want bool
	}{
		{"clean run", linePairing + lineCommissioned, false},
		{"pairing failure", "[CTL] Pairing Failure: src/…: CHIP Error 0x00000003\n", true},
		{"bare error code", "[CTL] Error: 0x00000032\n", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := PairingFailed(tc.out); got != tc.want {
				t.Errorf("PairingFailed() = %v, want %v\ninput:\n%s", got, tc.want, tc.out)
			}
		})
	}
}
