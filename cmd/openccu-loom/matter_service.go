// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package main

import "context"

// matterService adapts the Matter bring-up onto the north-bound
// bridge.Service contract so the registry owns its ordered teardown.
//
// Unlike the REST and MQTT surfaces, Matter's listener, endpoint topology
// and background goroutines are started during CONSTRUCTION
// (wireMatterRuntime), not in Start. Its bring-up interleaves construct,
// handler-attach and start steps in an order the Matter protocol and
// commissioner interop depend on: the node facade's Start runs the
// stack bring-up in its documented order (the DNS-SD re-announce loop
// included), and the daemon's own wiring (readiness, the REST surface)
// attaches around it.
// Separating start from construct would risk silent Apple/Google
// Home pair-aborts that take days to attribute, and Matter cannot be
// interop-verified from a unit test. So this Service deliberately owns only
// the ordered teardown (run in the registry's reverse-order StopAll, after
// REST and before the webhook) — Start is a no-op. This partial migration
// (teardown-managed, self-started) is a documented divergence from the
// "Service.Start does the starting" ideal; see ADR 0047.
//
// The BasicInformation ShutDown event (Matter spec §11.1.6.2) is emitted by
// the node facade's Stop, which this Service's Stop runs.
type matterService struct {
	stop func()
}

// newMatterService wraps the wireMatterRuntime teardown closure. stop is
// never nil for an enabled bridge (it is only registered when Matter is
// enabled).
func newMatterService(stop func()) *matterService {
	return &matterService{stop: stop}
}

// Name implements bridge.Service.
func (s *matterService) Name() string { return "matter" }

// Start is a no-op: the bridge is already brought up during construction
// (see the type doc).
func (s *matterService) Start(context.Context) error { return nil }

// Stop runs the Matter teardown (unwire the readiness hook, the node
// facade's Stop — which ends the re-announce loop and closes the
// advertiser — and the explicit advertiser release).
// Idempotent via the registry, which calls it at most once.
func (s *matterService) Stop(context.Context) error {
	if s.stop != nil {
		s.stop()
	}
	return nil
}
