// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// addLinkCentral registers a central with one device and a fake backend
// whose empty-address GetLinks returns the supplied links (or an error)
// into the shared registry + value writer. The device's InterfaceID is
// the exact ValueWriter.Backend key, mirroring the WireInterfaceID form.
func addLinkCentral(
	t *testing.T,
	reg *central.Registry,
	w *client.ValueWriter,
	name, ifaceID, devAddr string,
	links []hmproto.LinkDescription,
	linksErr error,
) {
	t.Helper()
	c, err := central.New(central.Config{Name: name})
	if err != nil {
		t.Fatalf("central.New(%s): %v", name, err)
	}
	if err := reg.Register(c); err != nil {
		t.Fatalf("reg.Register(%s): %v", name, err)
	}
	dev := device.New(device.Config{
		InterfaceID: ifaceID,
		Interface:   hmenum.InterfaceHmIPRF,
		Address:     devAddr,
		Model:       "HmIP-KEY4",
		Name:        devAddr,
	})
	dev.AddChannel(devAddr+":1", 1, "KEY", hmenum.ParamsetKeyValues)
	c.ModelRegistry.Put(dev)

	fake := &fakeOpsWithLinks{
		fakeOperations: fakeOperations{kind: backends.KindCCU},
		links:          links,
		linksErr:       linksErr,
	}
	w.Register(name, hmtypes.ParseWireInterfaceID(ifaceID), fake)
}

func TestLinksDomain_ListAllLinks_AggregatesAcrossCentrals(t *testing.T) {
	t.Parallel()
	reg := central.NewRegistry()
	w := client.NewValueWriter()
	addLinkCentral(t, reg, w, "ccu-a", "ccu-a-HmIP-RF", "DEVA",
		[]hmproto.LinkDescription{{Sender: "DEVA:1", Receiver: "PEERA:1", Name: "a"}}, nil)
	addLinkCentral(t, reg, w, "ccu-b", "ccu-b-HmIP-RF", "DEVB",
		[]hmproto.LinkDescription{{Sender: "DEVB:1", Receiver: "PEERB:1", Name: "b"}}, nil)
	d := NewLinksDomain(reg, w, nil)

	got, err := d.ListAllLinks(context.Background(), "", "en")
	if err != nil {
		t.Fatalf("ListAllLinks: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 links across both centrals, got %d: %+v", len(got), got)
	}
	byCentral := map[string]hmapiLinkView{}
	for _, l := range got {
		byCentral[l.CentralName] = hmapiLinkView{interfaceID: l.InterfaceID, sender: l.Sender, direction: l.Direction}
	}
	a, okA := byCentral["ccu-a"]
	b, okB := byCentral["ccu-b"]
	if !okA || !okB {
		t.Fatalf("both centrals must be represented, got %v", byCentral)
	}
	if a.interfaceID != "ccu-a-HmIP-RF" || b.interfaceID != "ccu-b-HmIP-RF" {
		t.Errorf("interface ids not carried: a=%q b=%q", a.interfaceID, b.interfaceID)
	}
	// The global path renders sender→receiver canonically — no queried
	// device, so no relative direction.
	if a.direction != "" {
		t.Errorf("global link direction must be empty, got %q", a.direction)
	}
}

type hmapiLinkView struct {
	interfaceID string
	sender      string
	direction   string
}

func TestLinksDomain_ListAllLinks_ScopedToCentral(t *testing.T) {
	t.Parallel()
	reg := central.NewRegistry()
	w := client.NewValueWriter()
	addLinkCentral(t, reg, w, "ccu-a", "ccu-a-HmIP-RF", "DEVA",
		[]hmproto.LinkDescription{{Sender: "DEVA:1", Receiver: "PEERA:1"}}, nil)
	addLinkCentral(t, reg, w, "ccu-b", "ccu-b-HmIP-RF", "DEVB",
		[]hmproto.LinkDescription{{Sender: "DEVB:1", Receiver: "PEERB:1"}}, nil)
	d := NewLinksDomain(reg, w, nil)

	got, err := d.ListAllLinks(context.Background(), "ccu-a", "en")
	if err != nil {
		t.Fatalf("ListAllLinks(ccu-a): %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 link scoped to ccu-a, got %d", len(got))
	}
	if got[0].CentralName != "ccu-a" {
		t.Errorf("scoped result leaked another central: %q", got[0].CentralName)
	}
}

func TestLinksDomain_ListAllLinks_UnknownCentral(t *testing.T) {
	t.Parallel()
	reg := central.NewRegistry()
	w := client.NewValueWriter()
	addLinkCentral(t, reg, w, "ccu-a", "ccu-a-HmIP-RF", "DEVA", nil, nil)
	d := NewLinksDomain(reg, w, nil)

	_, err := d.ListAllLinks(context.Background(), "ccu-x", "en")
	if !errors.Is(err, hmerr.ErrUnknownCentral) {
		t.Errorf("expected ErrUnknownCentral, got %v", err)
	}
}

func TestLinksDomain_ListAllLinks_BackendErrorSkipped(t *testing.T) {
	t.Parallel()
	reg := central.NewRegistry()
	w := client.NewValueWriter()
	addLinkCentral(t, reg, w, "ccu-a", "ccu-a-HmIP-RF", "DEVA",
		[]hmproto.LinkDescription{{Sender: "DEVA:1", Receiver: "PEERA:1"}}, nil)
	// ccu-b's interface cannot list links (e.g. CUxD / offline) — it must
	// contribute nothing rather than failing the whole aggregate.
	addLinkCentral(t, reg, w, "ccu-b", "ccu-b-HmIP-RF", "DEVB", nil, backends.ErrUnsupported)
	d := NewLinksDomain(reg, w, nil)

	got, err := d.ListAllLinks(context.Background(), "", "en")
	if err != nil {
		t.Fatalf("ListAllLinks: %v", err)
	}
	if len(got) != 1 || got[0].CentralName != "ccu-a" {
		t.Fatalf("expected only ccu-a's link, got %+v", got)
	}
}

func TestLinksDomain_ListAllLinks_DedupPerCentral(t *testing.T) {
	t.Parallel()
	reg := central.NewRegistry()
	w := client.NewValueWriter()
	addLinkCentral(t, reg, w, "ccu-a", "ccu-a-HmIP-RF", "DEVA",
		[]hmproto.LinkDescription{
			{Sender: "DEVA:1", Receiver: "PEERA:1", Name: "dup"},
			{Sender: "DEVA:1", Receiver: "PEERA:1", Name: "dup"},
		}, nil)
	d := NewLinksDomain(reg, w, nil)

	got, err := d.ListAllLinks(context.Background(), "", "en")
	if err != nil {
		t.Fatalf("ListAllLinks: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("expected duplicate sender->receiver deduped to 1, got %d", len(got))
	}
}
