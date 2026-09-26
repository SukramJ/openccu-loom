// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"log/slog"

	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// SouthProfile is a central's south-bound strategy: where the facts the
// bring-up depends on come from. It is selected once per central by
// [southProfileFor] from the central's system type, and it is the only place
// the system type is looked at — everything downstream of a source (the
// XML-RPC client, the description and paramset pipeline, the callback
// handlers, the model, every north-bound adapter) is shared.
//
// The boundary sits at "where does this fact come from", not at "which
// protocol": a CCU and an openccu-lite system speak the same XML-RPC to the
// same interface processes, but they signal readiness, deliver events and
// hold names in entirely different places.
type SouthProfile interface {
	// SystemType reports which system this profile talks to.
	SystemType() hmenum.SystemType
	// Readiness decides when the system serves names and devices. It gates
	// the boot bring-up, the pre-announce re-check, the recovery reconnect
	// and the InterfaceClient reconnect; each site keeps its own timeout.
	Readiness() ReadinessProbe
	// Liveness is the probe the MQTT hub publisher polls to decide whether
	// the central's hub plane is live. Nil means "no probe": the plane folds
	// to reachable on the interface state alone.
	Liveness() LivenessProbe
}

// ReadinessProbe performs ONE readiness check. reason names why the system is
// not ready ("checkrega.cgi answered 503", "unreachable: …") and is logged
// when the gate starts waiting.
type ReadinessProbe interface {
	Probe(ctx context.Context) (ready bool, reason string)
	// Target names what is probed, for the log line that reports a wait.
	Target() string
}

// LivenessProbe classifies one hub-plane liveness poll. It separates "the
// system answered that it is not serving" from "no answer" from "this
// system cannot be asked", because the hub publisher acts on each
// differently (see [regaLivenessTracker.observe]).
type LivenessProbe interface {
	Probe(ctx context.Context) systemProbeResult
}

// southProfileFor selects cc's south profile. Every central is a CCU until
// the system type becomes configurable; the CCU profile reproduces the
// behaviour the daemon has always had.
func southProfileFor(cc *config.CentralConfig, _ *slog.Logger) SouthProfile {
	return newCCUProfile(cc)
}

// southLivenessFor is the hub-plane liveness probe of cc's profile, or nil
// when the profile has none.
func southLivenessFor(cc *config.CentralConfig, logger *slog.Logger) LivenessProbe {
	return southProfileFor(cc, logger).Liveness()
}
