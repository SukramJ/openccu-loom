// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/client/transport/xmlrpc"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// multiChannelDescs is the announcement shape the CCU sends for a
// multi-channel device: the root carries CHILDREN, every channel carries
// PARENT and INDEX.
func multiChannelDescs(address string, channels []int) xmlrpc.ArrayValue {
	children := make(xmlrpc.ArrayValue, 0, len(channels))
	out := make(xmlrpc.ArrayValue, 0, len(channels)+1)
	for _, no := range channels {
		chAddr := fmt.Sprintf("%s:%d", address, no)
		children = append(children, xmlrpc.StringValue(chAddr))
		out = append(out, xmlrpc.StructValue{Members: []xmlrpc.Member{
			{Name: "ADDRESS", Value: xmlrpc.StringValue(chAddr)},
			{Name: "TYPE", Value: xmlrpc.StringValue("SWITCH_VIRTUAL_RECEIVER")},
			{Name: "PARENT", Value: xmlrpc.StringValue(address)},
			{Name: "INDEX", Value: xmlrpc.IntValue(no)},
		}})
	}
	root := xmlrpc.StructValue{Members: []xmlrpc.Member{
		{Name: "ADDRESS", Value: xmlrpc.StringValue(address)},
		{Name: "TYPE", Value: xmlrpc.StringValue("HmIP-BSM")},
		{Name: "CHILDREN", Value: children},
	}}
	return append(xmlrpc.ArrayValue{root}, out...)
}

// materialisingIngestFn is a stand-in for the production hot-plug
// ingestor (newHotplugIngestor): it builds the announced device with all
// its channels in the model registry, synchronously, before it returns —
// the property AcceptPendingDevice's caller relies on.
func materialisingIngestFn(cu *central.Unit) func(context.Context, string, []hmproto.DeviceDescription) error {
	return func(_ context.Context, interfaceID string, descs []hmproto.DeviceDescription) error {
		for i := range descs {
			root := &descs[i]
			if root.Parent != "" {
				continue
			}
			dev := device.New(device.Config{
				InterfaceID: interfaceID,
				Interface:   hmenum.InterfaceHmIPRF,
				Address:     root.Address,
				Model:       root.Type,
			})
			for j := range descs {
				ch := &descs[j]
				if ch.Parent != root.Address || ch.Index == nil {
					continue
				}
				dev.AddChannel(ch.Address, *ch.Index, ch.Type, hmenum.ParamsetKeyValues)
			}
			cu.ModelRegistry.Put(dev)
		}
		return nil
	}
}

// renameRecorder captures every address the persistent-rename hook is
// asked to rename, in the "<address>=<name>" form the assertions read.
type renameRecorder struct {
	mu    sync.Mutex
	calls []string
}

func (r *renameRecorder) fn(_ context.Context, address, name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, address+"="+name)
	return nil
}

func (r *renameRecorder) sorted() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := append([]string(nil), r.calls...)
	sort.Strings(out)
	return out
}

// acceptingInboxAccepter is a CCU-side inbox that promotes every device,
// the answer a real CCU gives for a device sitting in its inbox.
type acceptingInboxAccepter struct{}

func (acceptingInboxAccepter) AcceptDeviceInInbox(context.Context, string) error { return nil }

// acceptRenameCentral registers one central whose ingest materialises a
// four-channel device, with a recording rename hook installed. When
// deferred is true the device is parked in the deferred-creation queue
// and no CCU-side inbox exists, so the accept runs the daemon-only path;
// otherwise the device is already in the model registry and a CCU-side
// inbox promotes it, the way an immediately created device is accepted.
func acceptRenameCentral(t *testing.T, name, address string, deferred bool) (*central.Registry, *renameRecorder) {
	t.Helper()
	cu, err := central.New(central.Config{Name: name})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	reg := central.NewRegistry()
	if err := reg.Register(cu); err != nil {
		t.Fatalf("reg.Register: %v", err)
	}
	cu.SetDeviceIngestFn(materialisingIngestFn(cu))
	rec := &renameRecorder{}
	cu.SetRenameDeviceFn(rec.fn)

	h := NewCallbackHandlers(cu, nil)
	t.Cleanup(h.Stop)
	h.SetDelayNewDeviceCreation(deferred)
	if err := h.NewDevices(context.Background(), "HmIP-RF", multiChannelDescs(address, []int{1, 2, 3})); err != nil {
		t.Fatalf("NewDevices: %v", err)
	}
	if !deferred {
		// The immediate path ingests in the background; Stop drains it so
		// the device is in the registry before the accept runs.
		h.Stop()
		cu.HubModel.InboxAccepter = acceptingInboxAccepter{}
		if _, ok := cu.ModelRegistry.Get(address); !ok {
			t.Fatalf("device %s was not materialised by the immediate path", address)
		}
	}
	return reg, rec
}

// TestAcceptInboxDeviceRenamesEveryChannel pins the promise the accept
// dialog's "rename channels too" checkbox makes: after the accept the
// device AND every one of its channels carry the operator's name.
//
// The deferred case is the one that regressed. The configuration ran
// before the parked device was materialised, so
// RenameDeviceWithChannels found no model device and returned before its
// channel loop (internal/central/central.go, renameDeviceQuietly returns
// a nil device when the registry does not hold the address) — the CCU
// got the device rename and nothing else. The immediate case is the
// control: it always worked and must keep working.
func TestAcceptInboxDeviceRenamesEveryChannel(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		central  string
		address  string
		deferred bool
	}{
		{"deferred device", "ccu-accept-rename-deferred", "RENDEF01", true},
		{"already materialised", "ccu-accept-rename-immediate", "RENIMM01", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			reg, rec := acceptRenameCentral(t, tc.central, tc.address, tc.deferred)

			admin := NewDeviceAdminDomain(reg, nil)
			if err := admin.AcceptInboxDevice(context.Background(), tc.address, interfaces.AcceptInboxOptions{
				Name:            "Haustür",
				IncludeChannels: true,
			}); err != nil {
				t.Fatalf("AcceptInboxDevice: %v", err)
			}

			want := []string{
				tc.address + "=Haustür",
				tc.address + ":1=Haustür:1",
				tc.address + ":2=Haustür:2",
				tc.address + ":3=Haustür:3",
			}
			sort.Strings(want)
			got := rec.sorted()
			if len(got) != len(want) {
				t.Fatalf("rename hook saw %v, want %v — the channels keep their old names on the CCU", got, want)
			}
			for i := range want {
				if got[i] != want[i] {
					t.Errorf("rename %d: got %q, want %q", i, got[i], want[i])
				}
			}
		})
	}
}

// recordingAssignments is a CCU-side room / function mutator that accepts
// every write, so a test can separate "the CCU was told" from "the model
// knows".
type recordingAssignments struct {
	mu        sync.Mutex
	rooms     []string
	functions []string
}

func (r *recordingAssignments) SetDeviceRooms(_ context.Context, _ string, rooms []string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rooms = append([]string(nil), rooms...)
	return nil
}

func (r *recordingAssignments) SetDeviceFunctions(_ context.Context, _ string, functions []string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.functions = append([]string(nil), functions...)
	return nil
}

// TestAcceptInboxDeviceStampsRoomsAndFunctionsOnTheModel pins that the
// accept's first-time configuration reaches the live model, not only the
// CCU.
//
// The rooms / functions writes go to the hub remotes, which mutate the
// CCU alone. While the configuration ran BEFORE the materialisation the
// model picked the assignments up on the way in, from the DeviceDetails
// refresh the ingest performs. Now that the materialisation goes first,
// nothing would carry them into the model until the periodic
// device-details restamp runs (up to five minutes later), so a freshly
// accepted device rendered without its room and its function.
func TestAcceptInboxDeviceStampsRoomsAndFunctionsOnTheModel(t *testing.T) {
	t.Parallel()
	const address = "RENSTM01"
	reg, _ := acceptRenameCentral(t, "ccu-accept-stamp", address, true)
	cu := reg.List()[0]
	assign := &recordingAssignments{}
	cu.HubModel.RoomMutator = assign
	cu.HubModel.FunctionMutator = assign

	admin := NewDeviceAdminDomain(reg, nil)
	if err := admin.AcceptInboxDevice(context.Background(), address, interfaces.AcceptInboxOptions{
		Rooms:     []string{"Flur"},
		Functions: []string{"Licht"},
	}); err != nil {
		t.Fatalf("AcceptInboxDevice: %v", err)
	}

	if got := assign.rooms; len(got) != 1 || got[0] != "Flur" {
		t.Fatalf("CCU-side rooms = %v, want [Flur] — the remote write itself broke", got)
	}
	dev, ok := cu.ModelRegistry.Get(address)
	if !ok {
		t.Fatalf("device %s was not materialised", address)
	}
	if got := dev.Rooms(); len(got) != 1 || got[0] != "Flur" {
		t.Errorf("model rooms = %v, want [Flur] — the SPA and MQTT read the model, not the CCU", got)
	}
	if got := dev.Functions(); len(got) != 1 || got[0] != "Licht" {
		t.Errorf("model functions = %v, want [Licht]", got)
	}
}
