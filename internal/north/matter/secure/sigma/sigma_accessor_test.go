// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package sigma

import (
	"testing"
)

// TestResponder_PeerSessionID_BeforeSigma1 verifies that PeerSessionID
// returns 0 before any Sigma1 has been processed.
func TestResponder_PeerSessionID_BeforeSigma1(t *testing.T) {
	t.Parallel()
	r := &Responder{}
	if got := r.PeerSessionID(); got != 0 {
		t.Errorf("PeerSessionID before Sigma1 = %d, want 0", got)
	}
}

// TestResponder_PeerNodeID_BeforeSigma1 verifies that PeerNodeID
// returns 0 before any Sigma1 has been processed.
func TestResponder_PeerNodeID_BeforeSigma1(t *testing.T) {
	t.Parallel()
	r := &Responder{}
	if got := r.PeerNodeID(); got != 0 {
		t.Errorf("PeerNodeID before Sigma1 = %d, want 0", got)
	}
}

// TestResponder_SetSessionParameters_Nil verifies that SetSessionParameters
// accepts nil without panic.
func TestResponder_SetSessionParameters_Nil(t *testing.T) {
	t.Parallel()
	r := &Responder{}
	r.SetSessionParameters(nil) // must not panic
}

// TestResponder_SetSessionParameters_NonNil verifies that a non-nil
// SessionParameters is stored (getter not exposed; confirm no panic
// and nil/non-nil wired in sigma2 construction — only smoke test here).
func TestResponder_SetSessionParameters_NonNil(t *testing.T) {
	t.Parallel()
	r := &Responder{}
	params := &SessionParameters{
		SessionIdleInterval:    1000,
		SessionActiveInterval:  200,
		SessionActiveThreshold: 4000,
	}
	r.SetSessionParameters(params) // must not panic
}

// TestInitiator_SessionKeys_BeforeComplete verifies that SessionKeys
// returns ok=false before Sigma2/Sigma3 have been processed.
func TestInitiator_SessionKeys_BeforeComplete(t *testing.T) {
	t.Parallel()
	ini := &Initiator{}
	_, ok := ini.SessionKeys()
	if ok {
		t.Error("expected ok=false from Initiator.SessionKeys before complete")
	}
}

// TestResponder_SessionIdentity_BeforeSigma1 verifies that SessionIdentity
// returns ok=false before any Sigma1 has been processed.
func TestResponder_SessionIdentity_BeforeSigma1(t *testing.T) {
	t.Parallel()
	r := &Responder{}
	_, _, ok := r.SessionIdentity()
	if ok {
		t.Error("SessionIdentity returned ok=true before Sigma1")
	}
}
