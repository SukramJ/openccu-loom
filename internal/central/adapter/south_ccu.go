// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"net/http"

	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// ccuProfile is the south profile of a CCU with ReGaHss and the WebUI
// JSON-RPC: readiness and hub liveness both come from the OCCU boot marker
// CGI.
type ccuProfile struct {
	readiness *ccuReadinessProbe
	liveness  LivenessProbe
}

// newCCUProfile builds the CCU profile for cc.
func newCCUProfile(cc *config.CentralConfig) *ccuProfile {
	p := &ccuProfile{readiness: newCCUReadinessProbe(*cc, nil)}
	// Without a host there is nothing to poll; the hub plane then folds to
	// reachable on the interface state alone, as it always has.
	if cc.Host != "" {
		p.liveness = &ccuLivenessProbe{
			client: regaLivenessClient(*cc),
			url:    ccuBaseURLFor(*cc) + checkRegaPath,
		}
	}
	return p
}

// SystemType implements [SouthProfile].
func (*ccuProfile) SystemType() hmenum.SystemType { return hmenum.SystemTypeCCU }

// Readiness implements [SouthProfile].
func (p *ccuProfile) Readiness() ReadinessProbe { return p.readiness }

// Liveness implements [SouthProfile].
func (p *ccuProfile) Liveness() LivenessProbe { return p.liveness }

// ccuLivenessProbe polls the boot marker CGI and classifies the answer with
// [probeRegaLiveness]: a hung ReGa is a non-OK answer, not a missing one.
type ccuLivenessProbe struct {
	client *http.Client
	url    string
}

// Probe implements [LivenessProbe].
func (p *ccuLivenessProbe) Probe(ctx context.Context) systemProbeResult {
	return probeRegaLiveness(ctx, p.client, p.url)
}
