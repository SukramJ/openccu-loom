// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/registry"
	clientpkg "github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
)

// ─── nil-guard ───────────────────────────────────────────────────────────────

func TestNewFirmwareDomainNilGuards(t *testing.T) {
	t.Parallel()
	d := NewFirmwareDomain(nil, nil)
	if err := d.RefreshFirmwareData(context.Background()); err == nil {
		t.Fatal("expected error when registry and writer are nil")
	}
}

// ─── writerDescFetcher ───────────────────────────────────────────────────────

// listRecordingOps embeds fakeOperations and records ListDevices calls with
// configurable return values.
type listRecordingOps struct {
	fakeOperations
	calls      int
	returnDesc []hmproto.DeviceDescription
	returnErr  error
}

func (f *listRecordingOps) ListDevices(_ context.Context) ([]hmproto.DeviceDescription, error) {
	f.calls++
	return f.returnDesc, f.returnErr
}

func TestWriterDescFetcherListDevices_HappyPath(t *testing.T) {
	t.Parallel()

	want := []hmproto.DeviceDescription{
		{Address: "0002ABCD", Type: "HmIP-PSM"},
	}
	fake := &listRecordingOps{
		fakeOperations: fakeOperations{kind: backends.KindCCU},
		returnDesc:     want,
	}
	w := clientpkg.NewValueWriter()
	w.Register("ccu-01", "HmIP-RF", fake)

	f := &writerDescFetcher{writer: w, central: "ccu-01"}
	got, err := f.ListDevices(context.Background(), hmenum.InterfaceHmIPRF)
	if err != nil {
		t.Fatalf("ListDevices: %v", err)
	}
	if fake.calls != 1 {
		t.Errorf("expected 1 backend call, got %d", fake.calls)
	}
	if len(got) != len(want) || got[0].Address != want[0].Address {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestWriterDescFetcherListDevices_MissingBackend(t *testing.T) {
	t.Parallel()

	// Writer has no backend registered for ccu-01/HmIP-RF.
	w := clientpkg.NewValueWriter()
	f := &writerDescFetcher{writer: w, central: "ccu-01"}

	_, err := f.ListDevices(context.Background(), hmenum.InterfaceHmIPRF)
	if err == nil {
		t.Fatal("expected error when backend is not registered")
	}
	if !strings.Contains(err.Error(), "ccu-01") || !strings.Contains(err.Error(), "HmIP-RF") {
		t.Errorf("error %q must mention central and interface", err.Error())
	}
}

// ─── RefreshFirmwareData happy path ──────────────────────────────────────────

// buildFirmwareDomainFixture creates a central with one device and a fake
// backend registered under "HmIP-RF", returning the FirmwareDomain and the
// fake for call inspection.
func buildFirmwareDomainFixture(t *testing.T) (*FirmwareDomain, *listRecordingOps) {
	t.Helper()

	c, err := central.New(central.Config{Name: "ccu-01"})
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
		Address:     "0002ABCD",
		Model:       "HmIP-PSM",
		Name:        "Socket",
	})
	c.ModelRegistry.Put(dev)

	// Seed the description registry so the coordinator's GetInterfaceIDs()
	// returns HmIP-RF and the firmware sweep reaches the backend.
	c.DescRegistry.Put(hmenum.InterfaceHmIPRF, hmproto.DeviceDescription{
		Address: "0002ABCD", Type: "HmIP-PSM",
	})
	c.DeviceRegistry.Put(registry.DeviceEntry{
		Interface: hmenum.InterfaceHmIPRF,
		Address:   "0002ABCD",
		Model:     "HmIP-PSM",
	})

	fake := &listRecordingOps{
		fakeOperations: fakeOperations{kind: backends.KindCCU},
		returnDesc: []hmproto.DeviceDescription{
			{Address: "0002ABCD", Type: "HmIP-PSM", Children: []string{"0002ABCD:0"}},
		},
	}
	w := clientpkg.NewValueWriter()
	w.Register("ccu-01", "HmIP-RF", fake)

	return NewFirmwareDomain(reg, w), fake
}

func TestFirmwareDomainRefreshFirmwareData_HappyPath(t *testing.T) {
	t.Parallel()

	d, fake := buildFirmwareDomainFixture(t)
	if err := d.RefreshFirmwareData(context.Background()); err != nil {
		t.Fatalf("RefreshFirmwareData: %v", err)
	}
	if fake.calls < 1 {
		t.Errorf("expected backend ListDevices to be invoked, got %d calls", fake.calls)
	}
}
