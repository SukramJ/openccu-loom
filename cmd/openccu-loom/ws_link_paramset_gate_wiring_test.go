// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package main

import (
	"context"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/adapter"
	clientpkg "github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/store/visibility"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

// linkGateFakeBackend records whether PutLinkParamset actually reached the
// backend, so the test can tell "refused by the gate" from "delivered".
type linkGateFakeBackend struct {
	testBackendOps
	putCalled bool
}

func (b *linkGateFakeBackend) PutLinkParamset(_ context.Context, _, _ string, _ map[string]any) error {
	b.putCalled = true
	return nil
}

// TestWSLinkQueryPutLinkParamset_RefusesIgnoredModel_ReachesBackendOtherwise
// pins that a LINK paramset write issued through the WebSocket link path
// (wsLinkQuery, the exact type the composition root wires as ws.LinkQuery
// in wireWSCommands) is refused by the VisibilityGate when the target
// channel's device model is on the ignore list, and reaches the backend
// when the model is not ignored.
//
// The real visibility.Registry never refuses an individual LINK parameter —
// ParameterDecider.computeIgnored's default case (any paramset other than
// VALUES/MASTER) always answers "not ignored", matching the reference
// decider, which only handles VALUES and MASTER before falling through to
// False. IsModelIgnored is therefore the only axis on which the production
// gate can ever refuse a LINK write, which is why this pin drives that path
// instead of a per-parameter hide.
//
// Before the WS LINK paramset path was routed through ParamsetsDomain, it
// went through LinksDomain, which never consulted a VisibilityGate at all:
// a write to a channel of an ignored model reached the CCU over WebSocket
// while the identical REST request was correctly refused.
func TestWSLinkQueryPutLinkParamset_RefusesIgnoredModel_ReachesBackendOtherwise(t *testing.T) {
	t.Parallel()

	c, err := central.New(central.Config{Name: "ccu-link-gate"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	reg := central.NewRegistry()
	if err := reg.Register(c); err != nil {
		t.Fatalf("reg.Register: %v", err)
	}

	hiddenDev := device.New(device.Config{
		InterfaceID: "HmIP-RF",
		Interface:   hmenum.InterfaceHmIPRF,
		Address:     "GATEHIDDEN",
		Model:       "HmIP-Gate-Hidden",
	})
	hiddenDev.AddChannel("GATEHIDDEN:4", 4, "SWITCH", hmenum.ParamsetKeyValues)
	c.ModelRegistry.Put(hiddenDev)

	allowedDev := device.New(device.Config{
		InterfaceID: "HmIP-RF",
		Interface:   hmenum.InterfaceHmIPRF,
		Address:     "GATEALLOWED",
		Model:       "HmIP-Gate-Allowed",
	})
	allowedDev.AddChannel("GATEALLOWED:4", 4, "SWITCH", hmenum.ParamsetKeyValues)
	c.ModelRegistry.Put(allowedDev)

	be := &linkGateFakeBackend{}
	w := clientpkg.NewValueWriter()
	w.Register("ccu-link-gate", "HmIP-RF", be)

	gate := visibility.NewRegistry()
	gate.Model().IgnoreModel("HmIP-Gate-Hidden")

	paramsets := adapter.NewParamsetsDomain(reg, w).SetVisibilityGate(gate)
	q := &wsLinkQuery{registry: reg, paramsets: paramsets}

	// Arm 1: channel whose device model is ignored — refused, backend
	// never reached.
	err = q.PutLinkParamset(context.Background(), "GATEHIDDEN:4", "PEER:1", map[string]any{"SHORT_JT_ON": 1})
	if !errors.Is(err, hmerr.ErrParameterHidden) {
		t.Fatalf("ignored model: want ErrParameterHidden, got %v", err)
	}
	if be.putCalled {
		t.Fatal("ignored model: backend PutLinkParamset must not be called")
	}

	// Arm 2: same construction, a channel of a model that is not
	// ignored — the write reaches the backend.
	if err := q.PutLinkParamset(context.Background(), "GATEALLOWED:4", "PEER:1", map[string]any{"SHORT_JT_ON": 1}); err != nil {
		t.Fatalf("allowed model: PutLinkParamset: %v", err)
	}
	if !be.putCalled {
		t.Fatal("allowed model: backend PutLinkParamset was not called")
	}
}
