// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package alarm

import (
	"fmt"
	"slices"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestRemoteKeyCandidatesOfferEveryRoutedPressParameter pins the picker
// to the intent router's dispatch set. RemotePressParameters is the
// declared authority for which press parameters a remote binding can
// fire on (the write validator already reads it); the operator's picker
// has to offer exactly those, or a parameter the router routes is one an
// operator cannot bind — and the failure looks like a broken remote.
func TestRemoteKeyCandidatesOfferEveryRoutedPressParameter(t *testing.T) {
	t.Parallel()

	routed := RemotePressParameters()
	if len(routed) == 0 {
		t.Fatal("RemotePressParameters() is empty — the router routes no press parameter")
	}
	for i, p := range routed {
		address := fmt.Sprintf("WRC900%d", i)
		_, ch := newTestChannel(t, address, address+":1", 1, "KEY_TRANSCEIVER")
		attachPressEvent(ch, p)

		reg := newCandidatesRegistry(t, "my-ccu", ch.Device())
		s := &Service{reg: reg}

		got := s.RemoteKeyCandidates()
		if len(got) != 1 {
			t.Fatalf("routed parameter %s: candidates = %d, want 1 — the picker cannot offer a parameter the router dispatches", p, len(got))
		}
		if !slices.Contains(got[0].Parameters, string(p)) {
			t.Errorf("routed parameter %s missing from candidate parameters %v", p, got[0].Parameters)
		}
	}
}

// TestRemoteKeyCandidatesRejectUnroutedPressParameter is the other
// direction: a press-shaped parameter the router does not dispatch must
// not reach the picker, or an operator binds a key that never fires.
func TestRemoteKeyCandidatesRejectUnroutedPressParameter(t *testing.T) {
	t.Parallel()

	unrouted := hmenum.ParameterPressCont
	if IsRemotePressParameter(unrouted) {
		t.Skipf("%s is now routed — pick another unrouted press parameter", unrouted)
	}
	_, ch := newTestChannel(t, "WRC9100", "WRC9100:1", 1, "KEY_TRANSCEIVER")
	attachPressEvent(ch, unrouted)

	reg := newCandidatesRegistry(t, "my-ccu", ch.Device())
	s := &Service{reg: reg}

	if got := s.RemoteKeyCandidates(); len(got) != 0 {
		t.Fatalf("candidates = %+v, want none for the unrouted parameter %s", got, unrouted)
	}
}
