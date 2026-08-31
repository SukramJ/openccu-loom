// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
)

// linkDirectionParityOps serves a two-row link roster in which the
// queried device owns the sender channel of one row and the receiver
// channel of the other. Both rows carry both endpoints, which is what
// every production backend guarantees — the CCU and Homegear backends
// drop half-empty rows, and CUxD returns none.
type linkDirectionParityOps struct {
	paramsetFakeOps
}

func (f *linkDirectionParityOps) GetLinks(_ context.Context, _ string) ([]hmproto.LinkDescription, error) {
	return []hmproto.LinkDescription{
		{Sender: "LNKDIRDEV01:1", Receiver: "LNKDIRPEER:1", Name: "device-is-sender"},
		{Sender: "LNKDIRPEER:2", Receiver: "LNKDIRDEV01:2", Name: "device-is-receiver"},
	}, nil
}

// TestLinkDirectionIsSingleSourcedFromLinksDomain pins the two
// surfaces that expose a link's direction relative to the queried
// device — the domain rows served north-bound, and the
// LinkCoordinator rows served through the client adapter — against
// each other rather than against a literal. The rule lives in
// LinksDomain.enrichLink; a second derivation in the adapter is what
// this guard exists to catch.
func TestLinkDirectionIsSingleSourcedFromLinksDomain(t *testing.T) {
	t.Parallel()

	c, err := central.New(central.Config{Name: "ccu-link-direction-parity"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	reg := central.NewRegistry()
	if err := reg.Register(c); err != nil {
		t.Fatalf("reg.Register: %v", err)
	}

	dev := device.New(device.Config{
		InterfaceID: "HmIP-RF",
		Interface:   hmenum.InterfaceHmIPRF,
		Address:     "LNKDIRDEV01",
		Model:       "HmIP-BSM",
		Name:        "LNKDIRDEV01",
	})
	dev.AddChannel("LNKDIRDEV01:1", 1, "KEY", hmenum.ParamsetKeyValues)
	dev.AddChannel("LNKDIRDEV01:2", 2, "SWITCH", hmenum.ParamsetKeyValues)
	c.ModelRegistry.Put(dev)

	w := client.NewValueWriter()
	w.Register("ccu-link-direction-parity", "HmIP-RF", &linkDirectionParityOps{})

	domain := NewLinksDomain(reg, w, nil)
	if err := WireLinkCoordinator(c, domain); err != nil {
		t.Fatalf("WireLinkCoordinator: %v", err)
	}

	ctx := context.Background()
	domainRows, err := domain.ListLinks(ctx, "LNKDIRDEV01", "")
	if err != nil {
		t.Fatalf("ListLinks: %v", err)
	}
	coordRows, err := c.Link.GetLinks(ctx, "LNKDIRDEV01")
	if err != nil {
		t.Fatalf("Link.GetLinks: %v", err)
	}

	// LinkCoordinator.GetLinks sorts by (sender, receiver) while
	// ListLinks returns channel-iteration order, so compare by key.
	domainDirection := make(map[string]string, len(domainRows))
	for i := range domainRows {
		domainDirection[domainRows[i].Sender+"->"+domainRows[i].Receiver] = domainRows[i].Direction
	}
	coordDirection := make(map[string]string, len(coordRows))
	for i := range coordRows {
		coordDirection[coordRows[i].SenderAddress+"->"+coordRows[i].ReceiverAddress] = coordRows[i].Direction
	}
	if len(domainDirection) != len(coordDirection) {
		t.Fatalf("row sets differ: domain %v, coordinator %v", domainDirection, coordDirection)
	}
	for key, want := range domainDirection {
		got, ok := coordDirection[key]
		if !ok {
			t.Fatalf("coordinator is missing link %q (has %v)", key, coordDirection)
		}
		if got != want {
			t.Errorf("link %s: coordinator says %q, domain says %q", key, got, want)
		}
	}

	// Both directions must actually occur, otherwise two sites that
	// each returned one constant would satisfy the comparison above.
	seen := make(map[string]struct{}, 2)
	for _, dir := range domainDirection {
		seen[dir] = struct{}{}
	}
	for _, want := range []string{"outgoing", "incoming"} {
		if _, ok := seen[want]; !ok {
			t.Fatalf("fixture no longer produces a %q link: %v", want, domainDirection)
		}
	}
}
