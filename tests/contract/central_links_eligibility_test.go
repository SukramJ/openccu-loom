// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package contract

import (
	"context"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/adapter"
	"github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmapi"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// centralLinksEligibilityCall records one wire-level ReportValueUsage the
// central-links domain issued.
type centralLinksEligibilityCall struct {
	channel  string
	valueID  string
	refCount int
}

// errCentralLinksEligibilityNoMetadata stands in for a CCU that carries no
// report-value-usage metadata, which leaves the live active-state unresolved
// without affecting the eligibility verdict under test.
var errCentralLinksEligibilityNoMetadata = errors.New("no metadata")

// centralLinksEligibilityBackend is a CCU backend that records
// ReportValueUsage instead of talking to a CCU. The embedded interface stays
// nil: apart from the two methods below the paths under test reach no backend
// method, and a nil-pointer panic is a louder failure than a silent default.
type centralLinksEligibilityBackend struct {
	backends.Operations
	calls []centralLinksEligibilityCall
}

func (b *centralLinksEligibilityBackend) GetMetadata(_ context.Context, _, _ string) (any, error) {
	return nil, errCentralLinksEligibilityNoMetadata
}

func (b *centralLinksEligibilityBackend) ReportValueUsage(_ context.Context, channelAddress, valueID string, refCounter int) error {
	b.calls = append(b.calls, centralLinksEligibilityCall{channel: channelAddress, valueID: valueID, refCount: refCounter})
	return nil
}

// newCentralLinksEligibilityDomain builds the real dispatch path — a central
// holding one press-capable device, a value writer with the recording backend
// registered under the device's wire interface id, and the production
// constructor of the domain.
func newCentralLinksEligibilityDomain(
	t *testing.T,
	iface hmenum.Interface,
	model string,
) (*adapter.CentralLinksDomain, *centralLinksEligibilityBackend) {
	t.Helper()
	const centralName = "ccu-central-links"
	c, err := central.New(central.Config{Name: centralName})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	reg := central.NewRegistry()
	if err := reg.Register(c); err != nil {
		t.Fatalf("reg.Register: %v", err)
	}
	dev := device.New(device.Config{
		InterfaceID: string(iface),
		Interface:   iface,
		Address:     "CLDEV01",
		Model:       model,
		Name:        "CLDEV01",
	})
	ch := dev.AddChannel("CLDEV01:1", 1, "KEY", hmenum.ParamsetKeyValues)
	ch.Put(generic.NewDataPoint[bool](generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: "CLDEV01:1",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterPressShort),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeBool,
			Operations: hmenum.OperationsEvent,
		},
	}))
	c.ModelRegistry.Put(dev)

	backend := &centralLinksEligibilityBackend{}
	w := client.NewValueWriter()
	w.Register(centralName, hmtypes.ParseWireInterfaceID(string(iface)), backend)
	return adapter.NewCentralLinksDomain(reg, w), backend
}

// TestCentralLinksEligibilityMatchesDeviceRule pins the dispatching adapter to
// the domain rule it claims to follow.
//
// The adapter used to carry its own interface-only copy of the rule, so a
// virtual-remote pseudo-device on an eligible interface was offered central
// links and actually received ReportValueUsage writes — a device with no
// physical button behind the KEY_* source. The test asserts the wire effect,
// not the predicate: a rejected device must produce zero backend calls, and an
// accepted one must produce the exact triple, so an adapter that re-forks the
// rule fails here rather than in front of an operator.
func TestCentralLinksEligibilityMatchesDeviceRule(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name          string
		iface         hmenum.Interface
		model         string
		wantSupported bool
	}{
		{"hmip-rf wall remote", hmenum.InterfaceHmIPRF, "HMIP-WRC6", true},
		{"bidcos-rf push button", hmenum.InterfaceBidCosRF, "HM-PB-2-WM55", true},
		{"bidcos-wired io module", hmenum.InterfaceBidCosWired, "HMW-IO-12-SW14", true},
		{"cuxd device", hmenum.InterfaceCUxD, "CUX-SWITCH", false},
		{"virtual device", hmenum.InterfaceVirtualDevices, "VIRT", false},
		{"hmip virtual remote", hmenum.InterfaceHmIPRF, "HmIP-RCV-50", false},
		{"bidcos-rf virtual remote", hmenum.InterfaceBidCosRF, "HM-RCV-50", false},
		{"bidcos-wired virtual remote", hmenum.InterfaceBidCosWired, "HMW-RCV-50", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			d, backend := newCentralLinksEligibilityDomain(t, tc.iface, tc.model)

			status, err := d.CentralLinksStatus(ctx, "CLDEV01")
			if err != nil {
				t.Fatalf("CentralLinksStatus: %v", err)
			}
			if status.Supported != tc.wantSupported {
				t.Errorf("CentralLinksStatus(%s/%s).Supported = %v, want %v",
					tc.iface, tc.model, status.Supported, tc.wantSupported)
			}

			_, createErr := d.CreateCentralLinks(ctx, "CLDEV01", "")
			_, removeErr := d.RemoveCentralLinks(ctx, "CLDEV01", "")
			if tc.wantSupported {
				if createErr != nil {
					t.Fatalf("CreateCentralLinks(%s/%s): %v", tc.iface, tc.model, createErr)
				}
				if removeErr != nil {
					t.Fatalf("RemoveCentralLinks(%s/%s): %v", tc.iface, tc.model, removeErr)
				}
				// Positive control for the "zero calls" assertion below: an
				// eligible device must actually reach the wire, otherwise a
				// silent gate elsewhere would satisfy every negative row.
				want := centralLinksEligibilityCall{channel: "CLDEV01:1", valueID: "PRESS_SHORT", refCount: 1}
				if len(backend.calls) == 0 || backend.calls[0] != want {
					t.Fatalf("backend calls = %v, want first call %v", backend.calls, want)
				}
			} else {
				if !errors.Is(createErr, hmapi.ErrCentralLinksUnsupported) {
					t.Errorf("CreateCentralLinks(%s/%s) error = %v, want ErrCentralLinksUnsupported",
						tc.iface, tc.model, createErr)
				}
				if !errors.Is(removeErr, hmapi.ErrCentralLinksUnsupported) {
					t.Errorf("RemoveCentralLinks(%s/%s) error = %v, want ErrCentralLinksUnsupported",
						tc.iface, tc.model, removeErr)
				}
				if len(backend.calls) != 0 {
					t.Errorf("ineligible device reached the CCU: %v", backend.calls)
				}
			}

			// The cross-site tie: the model-layer rule and the dispatching
			// adapter must agree, so a private copy in either one shows up as
			// two columns disagreeing.
			ruleDev := device.New(device.Config{Interface: tc.iface, Address: "CLDEV01", Model: tc.model})
			if got := ruleDev.RelevantForCentralLinkManagement(); got != tc.wantSupported {
				t.Errorf("Device.RelevantForCentralLinkManagement(%s/%s) = %v, want %v",
					tc.iface, tc.model, got, tc.wantSupported)
			}
		})
	}
}

// TestCentralLinksVirtualRemoteReasonNamesTheModel pins the operator-visible
// reason token. The SPA prints it verbatim, so a virtual remote on an eligible
// interface must not be reported as an unsupported interface.
func TestCentralLinksVirtualRemoteReasonNamesTheModel(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	for _, tc := range []struct {
		iface      hmenum.Interface
		model      string
		wantReason string
	}{
		{hmenum.InterfaceHmIPRF, "HmIP-RCV-50", device.CentralLinkReasonVirtualRemote},
		{hmenum.InterfaceCUxD, "CUX-SWITCH", device.CentralLinkReasonInterfaceUnsupported},
	} {
		d, _ := newCentralLinksEligibilityDomain(t, tc.iface, tc.model)
		status, err := d.CentralLinksStatus(ctx, "CLDEV01")
		if err != nil {
			t.Fatalf("CentralLinksStatus: %v", err)
		}
		if status.Reason != tc.wantReason {
			t.Errorf("Reason for %s/%s = %q, want %q", tc.iface, tc.model, status.Reason, tc.wantReason)
		}
	}
}
