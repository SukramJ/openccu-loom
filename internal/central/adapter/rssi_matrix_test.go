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
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

const noInfo = backends.RSSINoInformation

// rssiMatrixOperations wraps fakeOperations with a scripted matrix, gateway
// list and SetBidcosInterface recorder.
type rssiMatrixOperations struct {
	*fakeOperations
	matrix    map[string]map[string][2]int
	matrixErr error
	gateways  []map[string]any
	gwErr     error
	assigned  []string
	assignErr error
}

func (f *rssiMatrixOperations) RSSIInfo(context.Context) (map[string]map[string][2]int, error) {
	return f.matrix, f.matrixErr
}

func (f *rssiMatrixOperations) ListBidcosInterfaces(context.Context, string) ([]map[string]any, error) {
	return f.gateways, f.gwErr
}

func (f *rssiMatrixOperations) SetBidcosInterface(_ context.Context, dev, gw string, roaming bool) error {
	r := "pinned"
	if roaming {
		r = "roaming"
	}
	f.assigned = append(f.assigned, dev+"->"+gw+"/"+r)
	return f.assignErr
}

// buildRSSIMatrixFixture registers one central "ccu-01" with a BidCos-RF
// backend under the production wire id, two gateways GW1 (default) and
// GW2, and the given model devices with their description INTERFACE /
// ROAMING.
func buildRSSIMatrixFixture(
	t *testing.T, matrix map[string]map[string][2]int, devs []hmproto.DeviceDescription,
) (*RSSIMatrixDomain, *rssiMatrixOperations) {
	t.Helper()
	c, err := central.New(central.Config{Name: "ccu-01"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	reg := central.NewRegistry()
	if err := reg.Register(c); err != nil {
		t.Fatalf("reg.Register: %v", err)
	}
	wireID := hmtypes.NewWireInterfaceID("ccu-01", hmenum.InterfaceBidCosRF)
	for i := range devs {
		c.ModelRegistry.Put(device.New(device.Config{
			InterfaceID: wireID.String(),
			Interface:   hmenum.InterfaceBidCosRF,
			Address:     devs[i].Address,
			Model:       "HM-LC-Sw1-FM",
			Name:        "Name " + devs[i].Address,
		}))
		c.DescRegistry.Put(wireID, devs[i])
	}
	fake := &rssiMatrixOperations{
		fakeOperations: &fakeOperations{kind: backends.KindCCU},
		matrix:         matrix,
		gateways: []map[string]any{
			{"address": "GW2", "description": "LAN gateway", "dutyCycle": "12", "isConnected": true, "isDefault": false},
			{"address": "GW1", "description": "CCU antenna", "dutyCycle": 3, "isConnected": true, "isDefault": true},
		},
	}
	w := client.NewValueWriter()
	w.Register("ccu-01", wireID, fake)
	return NewRSSIMatrixDomain(reg, w), fake
}

func desc(addr, iface string, roaming bool) hmproto.DeviceDescription {
	return hmproto.DeviceDescription{Address: addr, Type: "HM-LC-Sw1-FM", Interface: iface, Roaming: &roaming}
}

// TestRSSIMatrixMapsRowsSortedWithNamesAndNil verifies the mapping of the
// raw matrix: 65536 becomes nil in either direction, devices and partners
// come sorted by address, model devices carry their display name, and the
// central's gateways are listed sorted with their duty cycle.
func TestRSSIMatrixMapsRowsSortedWithNamesAndNil(t *testing.T) {
	t.Parallel()
	matrix := map[string]map[string][2]int{
		"DEVB": {"GW2": {-80, noInfo}, "GW1": {-60, -62}},
		"DEVA": {"GW1": {noInfo, -70}},
		"GW1":  {"DEVB": {-62, -60}},
	}
	domain, _ := buildRSSIMatrixFixture(t, matrix, []hmproto.DeviceDescription{desc("DEVB", "GW1", false)})
	got, err := domain.RSSIMatrix(context.Background())
	if err != nil {
		t.Fatalf("RSSIMatrix: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("centrals = %d, want 1", len(got))
	}
	m := got[0]
	if m.Central != "ccu-01" || m.InterfaceID != "ccu-01-BidCos-RF" {
		t.Errorf("central/iface = %q/%q", m.Central, m.InterfaceID)
	}
	if len(m.Interfaces) != 2 || m.Interfaces[0].Address != "GW1" || !m.Interfaces[0].Default ||
		m.Interfaces[0].DutyCycle != 3 || m.Interfaces[1].Address != "GW2" || m.Interfaces[1].DutyCycle != 12 ||
		m.Interfaces[1].Description != "LAN gateway" || !m.Interfaces[1].Connected {
		t.Errorf("interfaces = %+v", m.Interfaces)
	}
	if len(m.Devices) != 3 || m.Devices[0].Address != "DEVA" || m.Devices[1].Address != "DEVB" || m.Devices[2].Address != "GW1" {
		t.Fatalf("device order = %+v", m.Devices)
	}
	if m.Devices[1].Name != "Name DEVB" || m.Devices[0].Name != "" {
		t.Errorf("names = %q / %q", m.Devices[0].Name, m.Devices[1].Name)
	}
	a := m.Devices[0].Partners
	if len(a) != 1 || a[0].RxDBm != nil || a[0].TxDBm == nil || *a[0].TxDBm != -70 {
		t.Errorf("DEVA partners = %+v", a)
	}
	b := m.Devices[1].Partners
	if len(b) != 2 || b[0].Address != "GW1" || b[1].Address != "GW2" {
		t.Fatalf("DEVB partner order = %+v", b)
	}
	if *b[0].RxDBm != -60 || *b[0].TxDBm != -62 || *b[1].RxDBm != -80 || b[1].TxDBm != nil {
		t.Errorf("DEVB readings = rx %v tx %v / rx %v tx %v", b[0].RxDBm, b[0].TxDBm, b[1].RxDBm, b[1].TxDBm)
	}
}

// TestRSSIMatrixSkipsCentralWithoutBidCosRF verifies a central without a
// BidCos-RF backend contributes nothing, and a backend fault fails the
// call.
func TestRSSIMatrixSkipsCentralWithoutBidCosRF(t *testing.T) {
	t.Parallel()
	c, err := central.New(central.Config{Name: "ccu-02"})
	if err != nil {
		t.Fatal(err)
	}
	reg := central.NewRegistry()
	if err := reg.Register(c); err != nil {
		t.Fatal(err)
	}
	got, err := NewRSSIMatrixDomain(reg, client.NewValueWriter()).RSSIMatrix(context.Background())
	if err != nil || len(got) != 0 {
		t.Fatalf("got %+v, %v; want empty", got, err)
	}

	if _, err := NewRSSIMatrixDomain(nil, nil).RSSIMatrix(context.Background()); !errors.Is(err, ErrNoRSSIMatrixSource) {
		t.Fatalf("unwired: expected ErrNoRSSIMatrixSource, got %v", err)
	}

	domain, fake := buildRSSIMatrixFixture(t, nil, nil)
	fake.matrixErr = backends.ErrUnsupported
	if got, err := domain.RSSIMatrix(context.Background()); err != nil || len(got) != 0 {
		t.Fatalf("unsupported backend: got %+v, %v; want skipped", got, err)
	}
}

// TestRSSIMatrixFaultyCentralDoesNotBlankOthers verifies that with two
// centrals, a fault on the first one's backend — in the matrix read or in
// the gateway listing — lists that central with its error and empty rows,
// keeps the second central's data intact, and gives the faulty central's
// devices no proposal at all.
func TestRSSIMatrixFaultyCentralDoesNotBlankOthers(t *testing.T) {
	t.Parallel()
	for _, faultInListing := range []bool{false, true} {
		reg := central.NewRegistry()
		w := client.NewValueWriter()
		fakes := map[string]*rssiMatrixOperations{}
		for _, name := range []string{"ccu-a", "ccu-b"} {
			c, err := central.New(central.Config{Name: name})
			if err != nil {
				t.Fatal(err)
			}
			if err := reg.Register(c); err != nil {
				t.Fatal(err)
			}
			wireID := hmtypes.NewWireInterfaceID(name, hmenum.InterfaceBidCosRF)
			addr := "DEV-" + name
			c.ModelRegistry.Put(device.New(device.Config{
				InterfaceID: wireID.String(), Interface: hmenum.InterfaceBidCosRF, Address: addr, Model: "HM-LC-Sw1-FM",
			}))
			c.DescRegistry.Put(wireID, desc(addr, "GW1", false))
			f := &rssiMatrixOperations{
				fakeOperations: &fakeOperations{kind: backends.KindCCU},
				matrix:         map[string]map[string][2]int{addr: {"GW1": {-50, -55}}},
				gateways:       []map[string]any{{"address": "GW1", "isDefault": true}},
			}
			fakes[name] = f
			w.Register(name, wireID, f)
		}
		if faultInListing {
			fakes["ccu-a"].gwErr = errors.New("gateway listing down")
		} else {
			fakes["ccu-a"].matrixErr = errors.New("ccu unreachable")
		}
		domain := NewRSSIMatrixDomain(reg, w)

		got, err := domain.RSSIMatrix(context.Background())
		if err != nil {
			t.Fatalf("listing fault=%v: RSSIMatrix: %v", faultInListing, err)
		}
		if len(got) != 2 {
			t.Fatalf("listing fault=%v: centrals = %+v, want both", faultInListing, got)
		}
		byName := map[string]interfaces.RSSIMatrixCentral{got[0].Central: got[0], got[1].Central: got[1]}
		a, b := byName["ccu-a"], byName["ccu-b"]
		if a.Error == "" || a.InterfaceID != "ccu-a-BidCos-RF" || a.Interfaces == nil || a.Devices == nil ||
			len(a.Interfaces) != 0 || len(a.Devices) != 0 {
			t.Errorf("listing fault=%v: faulty central = %+v", faultInListing, a)
		}
		if b.Error != "" || len(b.Devices) != 1 || b.Devices[0].Address != "DEV-ccu-b" || len(b.Interfaces) != 1 {
			t.Errorf("listing fault=%v: healthy central = %+v", faultInListing, b)
		}

		props, err := domain.ReceiverProposal(context.Background(), -1)
		if err != nil {
			t.Fatalf("listing fault=%v: ReceiverProposal: %v", faultInListing, err)
		}
		if len(props) != 1 || props[0].Address != "DEV-ccu-b" || props[0].Verdict != interfaces.ReceiverKeep {
			t.Errorf("listing fault=%v: proposals = %+v, want only DEV-ccu-b keep", faultInListing, props)
		}
	}
}

// TestReceiverProposalVerdictTable covers every verdict and the ordering
// of the checks. Gateway readings are the "gateway hears device" direction
// (index 1 of the device's row entry).
func TestReceiverProposalVerdictTable(t *testing.T) {
	t.Parallel()
	matrix := map[string]map[string][2]int{
		// Roaming wins even though GW2 is far better.
		"ROAM": {"GW1": {0, -90}, "GW2": {0, -50}},
		// No gateway reading at all: only a non-gateway partner.
		"DEAF": {"GW1": {-50, noInfo}, "PEER": {-40, -40}},
		// Current is the best.
		"KEEP": {"GW1": {0, -55}, "GW2": {0, -70}},
		// Tie between current and another gateway keeps.
		"TIE": {"GW1": {0, -60}, "GW2": {0, -60}},
		// Advantage exactly the margin switches.
		"EDGE": {"GW1": {0, -70}, "GW2": {0, -64}},
		// Advantage below the margin is marginal.
		"NEAR": {"GW1": {0, -70}, "GW2": {0, -65}},
		// Current unheard, another gateway heard weakly: minus infinity
		// on the current side proposes a switch regardless of the margin.
		"LOST": {"GW1": {-50, noInfo}, "GW2": {0, -95}},
	}
	devs := []hmproto.DeviceDescription{
		desc("ROAM", "GW1", true),
		desc("NONE", "GW1", false), // no matrix row
		desc("DEAF", "GW1", false),
		desc("KEEP", "GW1", false),
		desc("TIE", "GW1", false),
		desc("EDGE", "GW1", false),
		desc("NEAR", "GW1", false),
		desc("LOST", "GW1", false),
	}
	domain, _ := buildRSSIMatrixFixture(t, matrix, devs)
	got, err := domain.ReceiverProposal(context.Background(), -1) // default margin 6
	if err != nil {
		t.Fatalf("ReceiverProposal: %v", err)
	}
	byAddr := make(map[string]interfaces.ReceiverProposal, len(got))
	for i, p := range got {
		byAddr[p.Address] = p
		if i > 0 && got[i-1].Address >= p.Address {
			t.Errorf("proposals not sorted: %s before %s", got[i-1].Address, p.Address)
		}
	}
	cases := []struct {
		addr    string
		verdict string
		best    string
		curRx   *int
		bestRx  *int
	}{
		{"ROAM", interfaces.ReceiverRoaming, "GW2", new(-90), new(-50)},
		{"NONE", interfaces.ReceiverUnmeasured, "", nil, nil},
		{"DEAF", interfaces.ReceiverUnheard, "", nil, nil},
		{"KEEP", interfaces.ReceiverKeep, "GW1", new(-55), new(-55)},
		{"TIE", interfaces.ReceiverKeep, "GW1", new(-60), new(-60)},
		{"EDGE", interfaces.ReceiverSwitch, "GW2", new(-70), new(-64)},
		{"NEAR", interfaces.ReceiverMarginal, "GW2", new(-70), new(-65)},
		{"LOST", interfaces.ReceiverSwitch, "GW2", nil, new(-95)},
	}
	if len(got) != len(cases) {
		t.Fatalf("proposals = %d, want %d: %+v", len(got), len(cases), got)
	}
	eq := func(a, b *int) bool { return (a == nil && b == nil) || (a != nil && b != nil && *a == *b) }
	for _, tc := range cases {
		p, ok := byAddr[tc.addr]
		if !ok {
			t.Errorf("%s: missing", tc.addr)
			continue
		}
		if p.Verdict != tc.verdict || p.BestInterface != tc.best || !eq(p.CurrentRxDBm, tc.curRx) || !eq(p.BestRxDBm, tc.bestRx) {
			t.Errorf("%s: got verdict=%s best=%s cur=%v best=%v; want %s/%s", tc.addr, p.Verdict, p.BestInterface,
				p.CurrentRxDBm, p.BestRxDBm, tc.verdict, tc.best)
		}
		if p.CurrentInterface != "GW1" || p.Central != "ccu-01" || p.Name != "Name "+tc.addr {
			t.Errorf("%s: identity = %+v", tc.addr, p)
		}
		if p.Roaming != (tc.addr == "ROAM") {
			t.Errorf("%s: roaming = %v", tc.addr, p.Roaming)
		}
	}
}

// TestReceiverProposalMarginIsHonoured verifies an explicit margin moves the
// boundary: NEAR (5 dB advantage) switches at margin 5 and EDGE (6 dB) is
// marginal at margin 7; margin 0 proposes a switch for any advantage.
func TestReceiverProposalMarginIsHonoured(t *testing.T) {
	t.Parallel()
	matrix := map[string]map[string][2]int{
		"EDGE": {"GW1": {0, -70}, "GW2": {0, -64}},
		"NEAR": {"GW1": {0, -70}, "GW2": {0, -65}},
	}
	domain, _ := buildRSSIMatrixFixture(t, matrix, []hmproto.DeviceDescription{
		desc("EDGE", "GW1", false), desc("NEAR", "GW1", false),
	})
	for _, tc := range []struct {
		margin     int
		edge, near string
	}{
		{5, interfaces.ReceiverSwitch, interfaces.ReceiverSwitch},
		{7, interfaces.ReceiverMarginal, interfaces.ReceiverMarginal},
		{0, interfaces.ReceiverSwitch, interfaces.ReceiverSwitch},
	} {
		got, err := domain.ReceiverProposal(context.Background(), tc.margin)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 2 || got[0].Verdict != tc.edge || got[1].Verdict != tc.near {
			t.Errorf("margin %d: got %s/%s, want %s/%s", tc.margin, got[0].Verdict, got[1].Verdict, tc.edge, tc.near)
		}
	}
}

// TestAssignRFInterfaceBidCosRFCallsBackend verifies the device-admin path
// reaches SetBidcosInterface with address, gateway serial and roaming flag
// in that order.
func TestAssignRFInterfaceBidCosRFCallsBackend(t *testing.T) {
	t.Parallel()
	c, err := central.New(central.Config{Name: "ccu-01"})
	if err != nil {
		t.Fatal(err)
	}
	reg := central.NewRegistry()
	if err := reg.Register(c); err != nil {
		t.Fatal(err)
	}
	c.ModelRegistry.Put(device.New(device.Config{
		InterfaceID: "ccu-01-BidCos-RF", Interface: hmenum.InterfaceBidCosRF, Address: "DEV", Model: "HM-LC-Sw1-FM",
	}))
	fake := &rssiMatrixOperations{fakeOperations: &fakeOperations{kind: backends.KindCCU}}
	w := client.NewValueWriter()
	w.Register("ccu-01", hmtypes.ParseWireInterfaceID("ccu-01-BidCos-RF"), fake)
	d := NewDeviceAdminDomain(reg, w)
	if err := d.AssignRFInterface(context.Background(), "DEV", "GW2", false); err != nil {
		t.Fatalf("AssignRFInterface: %v", err)
	}
	if err := d.AssignRFInterface(context.Background(), "DEV", "GW1", true); err != nil {
		t.Fatalf("AssignRFInterface roaming: %v", err)
	}
	if len(fake.assigned) != 2 || fake.assigned[0] != "DEV->GW2/pinned" || fake.assigned[1] != "DEV->GW1/roaming" {
		t.Fatalf("assigned = %v", fake.assigned)
	}
	fake.assignErr = errors.New("fault")
	if err := d.AssignRFInterface(context.Background(), "DEV", "GW2", false); !errors.Is(err, fake.assignErr) {
		t.Fatalf("expected backend fault, got %v", err)
	}
	if err := d.AssignRFInterface(context.Background(), "UNKNOWN", "GW2", false); !errors.Is(err, ErrNoDeviceBackend) {
		t.Fatalf("unknown device: expected ErrNoDeviceBackend, got %v", err)
	}
}

// TestAssignRFInterfaceNonRFRejectedBeforeWireCall verifies every other
// interface is refused with ErrUnsupported and never reaches the wire.
func TestAssignRFInterfaceNonRFRejectedBeforeWireCall(t *testing.T) {
	t.Parallel()
	for _, iface := range []hmenum.Interface{
		hmenum.InterfaceHmIPRF, hmenum.InterfaceBidCosWired, hmenum.InterfaceCUxD, hmenum.InterfaceVirtualDevices,
	} {
		c, err := central.New(central.Config{Name: "ccu-01"})
		if err != nil {
			t.Fatal(err)
		}
		reg := central.NewRegistry()
		if err := reg.Register(c); err != nil {
			t.Fatal(err)
		}
		c.ModelRegistry.Put(device.New(device.Config{
			InterfaceID: string(iface), Interface: iface, Address: "DEV", Model: "X",
		}))
		fake := &rssiMatrixOperations{fakeOperations: &fakeOperations{kind: backends.KindCCU}}
		w := client.NewValueWriter()
		w.Register("ccu-01", hmtypes.ParseWireInterfaceID(string(iface)), fake)
		err = NewDeviceAdminDomain(reg, w).AssignRFInterface(context.Background(), "DEV", "GW", false)
		if !errors.Is(err, backends.ErrUnsupported) {
			t.Errorf("%s: expected ErrUnsupported, got %v", iface, err)
		}
		if len(fake.assigned) != 0 {
			t.Errorf("%s: assigned = %v, want none", iface, fake.assigned)
		}
	}
}
