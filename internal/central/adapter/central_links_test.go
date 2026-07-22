// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmapi"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// ============================================================
// isCentralLinkInterface tests (pure logic, no deps)
// ============================================================

func TestIsCentralLinkInterfaceEligible(t *testing.T) {
	t.Parallel()
	eligible := []hmenum.Interface{
		hmenum.InterfaceBidCosRF,
		hmenum.InterfaceBidCosWired,
		hmenum.InterfaceHmIPRF,
	}
	for _, iface := range eligible {
		if !isCentralLinkInterface(iface) {
			t.Errorf("isCentralLinkInterface(%v) = false, want true", iface)
		}
	}
}

func TestIsCentralLinkInterfaceIneligible(t *testing.T) {
	t.Parallel()
	ineligible := []hmenum.Interface{
		hmenum.InterfaceVirtualDevices,
		hmenum.InterfaceCUxD,
	}
	for _, iface := range ineligible {
		if isCentralLinkInterface(iface) {
			t.Errorf("isCentralLinkInterface(%v) = true, want false", iface)
		}
	}
}

// ============================================================
// channelHasPressEvents tests
// ============================================================

func TestChannelHasPressEventsNilChannel(t *testing.T) {
	t.Parallel()
	if channelHasPressEvents(nil) {
		t.Error("nil channel must return false")
	}
}

func TestChannelHasPressEventsWithPressShort(t *testing.T) {
	t.Parallel()
	dev := device.New(device.Config{Address: "DEV001", InterfaceID: "BidCos-RF"})
	ch := dev.AddChannel("DEV001:1", 1, "KEY", hmenum.ParamsetKeyValues)
	pressDP := generic.NewDataPoint[bool](generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: "DEV001:1",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterPressShort),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeBool,
			Operations: hmenum.OperationsEvent,
		},
	})
	ch.Put(pressDP)
	if !channelHasPressEvents(ch) {
		t.Error("channel with PRESS_SHORT must return true")
	}
}

func TestChannelHasPressEventsWithPressLong(t *testing.T) {
	t.Parallel()
	dev := device.New(device.Config{Address: "DEV002", InterfaceID: "BidCos-RF"})
	ch := dev.AddChannel("DEV002:1", 1, "KEY", hmenum.ParamsetKeyValues)
	pressDP := generic.NewDataPoint[bool](generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: "DEV002:1",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterPressLong),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeBool,
			Operations: hmenum.OperationsEvent,
		},
	})
	ch.Put(pressDP)
	if !channelHasPressEvents(ch) {
		t.Error("channel with PRESS_LONG must return true")
	}
}

func TestChannelHasPressEventsWithoutPressParams(t *testing.T) {
	t.Parallel()
	dev := device.New(device.Config{Address: "DEV003", InterfaceID: "BidCos-RF"})
	ch := dev.AddChannel("DEV003:1", 1, "TEMP", hmenum.ParamsetKeyValues)
	// Add a non-press parameter.
	stateDP := generic.NewDataPoint[float64](generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: "DEV003:1",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      "TEMPERATURE",
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeFloat,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
		},
	})
	ch.Put(stateDP)
	if channelHasPressEvents(ch) {
		t.Error("channel without press parameters must return false")
	}
}

// ============================================================
// CentralLinksDomain.CentralLinksStatus tests
// ============================================================

// TestCentralLinksStatusNilRegistryError verifies that a nil registry
// returns ErrNoCentralLinkBackend.
func TestCentralLinksStatusNilRegistryError(t *testing.T) {
	t.Parallel()
	d := NewCentralLinksDomain(nil, nil)
	_, err := d.CentralLinksStatus("DEV001")
	if !errors.Is(err, ErrNoCentralLinkBackend) {
		t.Fatalf("err = %v, want ErrNoCentralLinkBackend", err)
	}
}

// TestCentralLinksStatusDeviceNotFound verifies that a missing device
// returns ErrNoCentralLinkBackend.
func TestCentralLinksStatusDeviceNotFound(t *testing.T) {
	t.Parallel()
	c, err := central.New(central.Config{Name: "ccu-01"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	reg := central.NewRegistry()
	_ = reg.Register(c)

	d := NewCentralLinksDomain(reg, nil)
	_, sErr := d.CentralLinksStatus("MISSING_DEV")
	if !errors.Is(sErr, ErrNoCentralLinkBackend) {
		t.Fatalf("err = %v, want ErrNoCentralLinkBackend", sErr)
	}
}

// TestCentralLinksStatusUnsupportedInterface verifies that a device on
// an unsupported interface reports Supported=false.
func TestCentralLinksStatusUnsupportedInterface(t *testing.T) {
	t.Parallel()
	c, err := central.New(central.Config{Name: "ccu-01"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	reg := central.NewRegistry()
	_ = reg.Register(c)

	dev := device.New(device.Config{
		InterfaceID: "CUxD",
		Interface:   hmenum.InterfaceCUxD,
		Address:     "CUX0001",
		Model:       "CUX-Model",
	})
	c.ModelRegistry.Put(dev)

	d := NewCentralLinksDomain(reg, nil)
	status, sErr := d.CentralLinksStatus("CUX0001")
	if sErr != nil {
		t.Fatalf("CentralLinksStatus: %v", sErr)
	}
	if status.Supported {
		t.Error("CUxD device must not be supported for central links")
	}
	if status.Reason != "interface_unsupported" {
		t.Errorf("Reason = %q, want interface_unsupported", status.Reason)
	}
}

// TestCentralLinksStatusSupportedDevice verifies that a device on a
// supported interface with press-capable channels reports correctly.
func TestCentralLinksStatusSupportedDevice(t *testing.T) {
	t.Parallel()
	c, err := central.New(central.Config{Name: "ccu-01"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	reg := central.NewRegistry()
	_ = reg.Register(c)

	dev := device.New(device.Config{
		InterfaceID: "BidCos-RF",
		Interface:   hmenum.InterfaceBidCosRF,
		Address:     "BRF0001",
		Model:       "HM-RC-4",
	})
	// Add a channel with PRESS_SHORT.
	ch := dev.AddChannel("BRF0001:1", 1, "KEY", hmenum.ParamsetKeyValues)
	pressDP := generic.NewDataPoint[bool](generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: "BRF0001:1",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterPressShort),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeBool,
			Operations: hmenum.OperationsEvent,
		},
	})
	ch.Put(pressDP)
	c.ModelRegistry.Put(dev)

	d := NewCentralLinksDomain(reg, nil)
	status, sErr := d.CentralLinksStatus("BRF0001")
	if sErr != nil {
		t.Fatalf("CentralLinksStatus: %v", sErr)
	}
	if !status.Supported {
		t.Error("BidCos-RF device must be supported for central links")
	}
	if status.EligibleChannels != 1 {
		t.Errorf("EligibleChannels = %d, want 1", status.EligibleChannels)
	}
}

// ============================================================
// CentralLinksDomain.CreateCentralLinks / RemoveCentralLinks tests
// ============================================================

// TestCentralLinksCreateNilRegistryError verifies nil registry returns error.
func TestCentralLinksCreateNilRegistryError(t *testing.T) {
	t.Parallel()
	d := NewCentralLinksDomain(nil, nil)
	_, err := d.CreateCentralLinks(context.Background(), "DEV001", "")
	if !errors.Is(err, ErrNoCentralLinkBackend) {
		t.Fatalf("err = %v, want ErrNoCentralLinkBackend", err)
	}
}

// TestCentralLinksRemoveNilRegistryError verifies nil registry returns error.
func TestCentralLinksRemoveNilRegistryError(t *testing.T) {
	t.Parallel()
	d := NewCentralLinksDomain(nil, nil)
	_, err := d.RemoveCentralLinks(context.Background(), "DEV001", "")
	if !errors.Is(err, ErrNoCentralLinkBackend) {
		t.Fatalf("err = %v, want ErrNoCentralLinkBackend", err)
	}
}

// TestCentralLinksCreateDeviceNotFound verifies missing device error.
func TestCentralLinksCreateDeviceNotFound(t *testing.T) {
	t.Parallel()
	c, err := central.New(central.Config{Name: "ccu-01"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	reg := central.NewRegistry()
	_ = reg.Register(c)
	d := NewCentralLinksDomain(reg, nil)
	_, sErr := d.CreateCentralLinks(context.Background(), "MISSING", "")
	if !errors.Is(sErr, ErrNoCentralLinkBackend) {
		t.Fatalf("err = %v, want ErrNoCentralLinkBackend", sErr)
	}
}

// ============================================================
// CentralLinksDomain per-channel filter tests
// ============================================================

// reportValueUsageCall captures one ReportValueUsage invocation so the
// remove-path tests can assert both the channel scope and that the
// teardown zeroes PRESS_SHORT and PRESS_LONG.
type reportValueUsageCall struct {
	channel    string
	valueID    string
	refCounter int
}

// recordingReportBackend embeds the full fake operations surface and
// records every ReportValueUsage invocation, so the channel-filter and
// value-id tests can assert exactly which channels and value-ids were
// touched.
type recordingReportBackend struct {
	paramsetFakeOps
	calls []reportValueUsageCall
}

func (b *recordingReportBackend) ReportValueUsage(_ context.Context, channelAddress, valueID string, refCounter int) error {
	b.calls = append(b.calls, reportValueUsageCall{channel: channelAddress, valueID: valueID, refCounter: refCounter})
	return nil
}

// channels returns the channel address of every recorded call, in order.
func (b *recordingReportBackend) channels() []string {
	out := make([]string, 0, len(b.calls))
	for _, c := range b.calls {
		out = append(out, c.channel)
	}
	return out
}

// valueIDsFor returns the value-ids recorded for a given channel, in order.
func (b *recordingReportBackend) valueIDsFor(channel string) []string {
	var out []string
	for _, c := range b.calls {
		if c.channel == channel {
			out = append(out, c.valueID)
		}
	}
	return out
}

// buildTwoPressChannelDomainWithBackend wires a BidCos-RF device with two
// press-capable channels (":1" and ":2") behind the supplied backend.
func buildTwoPressChannelDomainWithBackend(t *testing.T, backend backends.Operations) *CentralLinksDomain {
	t.Helper()
	c, err := central.New(central.Config{Name: "ccu-chan-filter"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	reg := central.NewRegistry()
	if err := reg.Register(c); err != nil {
		t.Fatalf("reg.Register: %v", err)
	}

	dev := device.New(device.Config{
		InterfaceID: "BidCos-RF",
		Interface:   hmenum.InterfaceBidCosRF,
		Address:     "CHFILT01",
		Model:       "HM-RC-4",
		Name:        "CHFILT01",
	})
	for _, no := range []int{1, 2} {
		addr := "CHFILT01:" + string(rune('0'+no))
		ch := dev.AddChannel(addr, no, "KEY", hmenum.ParamsetKeyValues)
		ch.Put(generic.NewDataPoint[bool](generic.Spec{
			Key: hmtypes.DataPointKey{
				ChannelAddress: addr,
				ParamsetKey:    hmenum.ParamsetKeyValues,
				Parameter:      string(hmenum.ParameterPressShort),
			},
			Descriptor: hmproto.ParameterData{
				Type:       hmenum.ParameterTypeBool,
				Operations: hmenum.OperationsEvent,
			},
		}))
	}
	c.ModelRegistry.Put(dev)

	w := client.NewValueWriter()
	w.Register("ccu-chan-filter", "BidCos-RF", backend)
	return NewCentralLinksDomain(reg, w)
}

// buildTwoPressChannelDomain wires a BidCos-RF device with two
// press-capable channels (":1" and ":2") behind a recording backend.
func buildTwoPressChannelDomain(t *testing.T) (*CentralLinksDomain, *recordingReportBackend) {
	t.Helper()
	b := &recordingReportBackend{}
	return buildTwoPressChannelDomainWithBackend(t, b), b
}

// TestCentralLinksCreateAllChannels verifies that an empty channel
// argument touches every eligible channel of the device.
func TestCentralLinksCreateAllChannels(t *testing.T) {
	t.Parallel()
	d, b := buildTwoPressChannelDomain(t)
	report, err := d.CreateCentralLinks(context.Background(), "CHFILT01", "")
	if err != nil {
		t.Fatalf("CreateCentralLinks: %v", err)
	}
	if report.Touched != 2 {
		t.Errorf("Touched = %d, want 2", report.Touched)
	}
	if len(b.channels()) != 2 {
		t.Fatalf("backend calls = %v, want 2", b.channels())
	}
}

// TestCentralLinksCreateSingleChannel verifies that a non-empty channel
// argument touches only that one channel.
func TestCentralLinksCreateSingleChannel(t *testing.T) {
	t.Parallel()
	d, b := buildTwoPressChannelDomain(t)
	report, err := d.CreateCentralLinks(context.Background(), "CHFILT01", "CHFILT01:2")
	if err != nil {
		t.Fatalf("CreateCentralLinks: %v", err)
	}
	if report.Touched != 1 {
		t.Errorf("Touched = %d, want 1", report.Touched)
	}
	if got := b.channels(); len(got) != 1 || got[0] != "CHFILT01:2" {
		t.Fatalf("backend calls = %v, want [CHFILT01:2]", got)
	}
}

// TestCentralLinksRemoveSingleChannel verifies the same scoping on the
// remove path — and that teardown zeroes PRESS_SHORT and PRESS_LONG on
// the single named channel (two wire calls, one counted channel).
func TestCentralLinksRemoveSingleChannel(t *testing.T) {
	t.Parallel()
	d, b := buildTwoPressChannelDomain(t)
	report, err := d.RemoveCentralLinks(context.Background(), "CHFILT01", "CHFILT01:1")
	if err != nil {
		t.Fatalf("RemoveCentralLinks: %v", err)
	}
	if report.Touched != 1 {
		t.Errorf("Touched = %d, want 1", report.Touched)
	}
	got := b.channels()
	if len(got) != 2 || got[0] != "CHFILT01:1" || got[1] != "CHFILT01:1" {
		t.Fatalf("backend channels = %v, want two calls on CHFILT01:1", got)
	}
	if vids := b.valueIDsFor("CHFILT01:1"); len(vids) != 2 || vids[0] != "PRESS_SHORT" || vids[1] != "PRESS_LONG" {
		t.Fatalf("value-ids = %v, want [PRESS_SHORT PRESS_LONG]", vids)
	}
}

// TestCentralLinksRemoveZeroesPressShortAndLong verifies that tearing a
// central link down zeroes PRESS_SHORT and PRESS_LONG on every eligible
// channel — a device-wide remove issues two ReportValueUsage calls per
// channel with refCounter 0, so the device-internal direct link is fully
// removed (mirrors the CCU WebUI removeCentralLink).
func TestCentralLinksRemoveZeroesPressShortAndLong(t *testing.T) {
	t.Parallel()
	d, b := buildTwoPressChannelDomain(t)
	report, err := d.RemoveCentralLinks(context.Background(), "CHFILT01", "")
	if err != nil {
		t.Fatalf("RemoveCentralLinks: %v", err)
	}
	if report.Touched != 2 {
		t.Errorf("Touched = %d, want 2 (two channels)", report.Touched)
	}
	if len(b.calls) != 4 {
		t.Fatalf("backend calls = %v, want 4 (two per channel)", b.calls)
	}
	for _, ch := range []string{"CHFILT01:1", "CHFILT01:2"} {
		if vids := b.valueIDsFor(ch); len(vids) != 2 || vids[0] != "PRESS_SHORT" || vids[1] != "PRESS_LONG" {
			t.Errorf("channel %s value-ids = %v, want [PRESS_SHORT PRESS_LONG]", ch, vids)
		}
	}
	for _, c := range b.calls {
		if c.refCounter != 0 {
			t.Errorf("call %+v: refCounter = %d, want 0", c, c.refCounter)
		}
	}
}

// TestCentralLinksCreateOnlyRaisesPressShort verifies that activating a
// central link raises PRESS_SHORT only (refCounter 1) and never touches
// PRESS_LONG — matching the CCU WebUI createCentralLink and the reference.
func TestCentralLinksCreateOnlyRaisesPressShort(t *testing.T) {
	t.Parallel()
	d, b := buildTwoPressChannelDomain(t)
	report, err := d.CreateCentralLinks(context.Background(), "CHFILT01", "")
	if err != nil {
		t.Fatalf("CreateCentralLinks: %v", err)
	}
	if report.Touched != 2 {
		t.Errorf("Touched = %d, want 2", report.Touched)
	}
	if len(b.calls) != 2 {
		t.Fatalf("backend calls = %v, want 2 (one per channel)", b.calls)
	}
	for _, c := range b.calls {
		if c.valueID != "PRESS_SHORT" {
			t.Errorf("create call %+v: valueID = %q, want PRESS_SHORT", c, c.valueID)
		}
		if c.refCounter != 1 {
			t.Errorf("create call %+v: refCounter = %d, want 1", c, c.refCounter)
		}
	}
}

// failingReportBackend errors on every ReportValueUsage call and counts
// the invocations, so the failure-accounting test can assert per-channel
// (not per-value-id) Failed counting on the remove path.
type failingReportBackend struct {
	paramsetFakeOps
	calls int
}

func (b *failingReportBackend) ReportValueUsage(context.Context, string, string, int) error {
	b.calls++
	return errors.New("report value usage failed")
}

// TestCentralLinksRemoveFailureCountsChannelsNotCalls verifies that a
// backend that rejects every ReportValueUsage marks each channel Failed
// exactly once even though the remove path issues two value-id calls per
// channel, and propagates the first CCU error.
func TestCentralLinksRemoveFailureCountsChannelsNotCalls(t *testing.T) {
	t.Parallel()
	b := &failingReportBackend{}
	d := buildTwoPressChannelDomainWithBackend(t, b)
	report, err := d.RemoveCentralLinks(context.Background(), "CHFILT01", "")
	if err == nil {
		t.Fatal("RemoveCentralLinks: want propagated error, got nil")
	}
	if report.Failed != 2 {
		t.Errorf("Failed = %d, want 2 (per channel)", report.Failed)
	}
	if report.Touched != 0 {
		t.Errorf("Touched = %d, want 0", report.Touched)
	}
}

// TestCentralLinksUnknownChannel verifies that naming a channel the
// device does not carry returns ErrCentralLinksChannelNotFound and
// touches nothing.
func TestCentralLinksUnknownChannel(t *testing.T) {
	t.Parallel()
	d, b := buildTwoPressChannelDomain(t)
	_, err := d.CreateCentralLinks(context.Background(), "CHFILT01", "CHFILT01:9")
	if !errors.Is(err, hmapi.ErrCentralLinksChannelNotFound) {
		t.Fatalf("err = %v, want ErrCentralLinksChannelNotFound", err)
	}
	if len(b.channels()) != 0 {
		t.Fatalf("backend calls = %v, want none", b.channels())
	}
}

// buildMixedChannelDomain wires a BidCos-RF device with one
// press-capable channel (":1") and one channel that carries no press
// parameter at all (":2", a plain TEMPERATURE channel) behind a
// recording backend — used to verify that naming a non-press channel
// via the `channel` scope is reported as skipped rather than touched
// or errored.
func buildMixedChannelDomain(t *testing.T) (*CentralLinksDomain, *recordingReportBackend) {
	t.Helper()
	c, err := central.New(central.Config{Name: "ccu-mixed-chan"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	reg := central.NewRegistry()
	if err := reg.Register(c); err != nil {
		t.Fatalf("reg.Register: %v", err)
	}

	dev := device.New(device.Config{
		InterfaceID: "BidCos-RF",
		Interface:   hmenum.InterfaceBidCosRF,
		Address:     "MIXEDCH01",
		Model:       "HM-RC-4",
		Name:        "MIXEDCH01",
	})
	pressCh := dev.AddChannel("MIXEDCH01:1", 1, "KEY", hmenum.ParamsetKeyValues)
	pressCh.Put(generic.NewDataPoint[bool](generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: "MIXEDCH01:1",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterPressShort),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeBool,
			Operations: hmenum.OperationsEvent,
		},
	}))
	tempCh := dev.AddChannel("MIXEDCH01:2", 2, "TEMP", hmenum.ParamsetKeyValues)
	tempCh.Put(generic.NewDataPoint[float64](generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: "MIXEDCH01:2",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      "TEMPERATURE",
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeFloat,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
		},
	}))
	c.ModelRegistry.Put(dev)

	b := &recordingReportBackend{}
	w := client.NewValueWriter()
	w.Register("ccu-mixed-chan", "BidCos-RF", b)
	return NewCentralLinksDomain(reg, w), b
}

// TestCentralLinksNamedChannelWithoutPressEvents verifies that scoping
// a create/remove call to a channel address that exists on the device
// but exposes no PRESS_SHORT / PRESS_LONG parameter reports the channel
// as skipped (not an error, not touched) — the channel is a legitimate
// device channel, just not one that can drive central click events.
func TestCentralLinksNamedChannelWithoutPressEvents(t *testing.T) {
	t.Parallel()
	d, b := buildMixedChannelDomain(t)
	report, err := d.CreateCentralLinks(context.Background(), "MIXEDCH01", "MIXEDCH01:2")
	if err != nil {
		t.Fatalf("CreateCentralLinks: %v", err)
	}
	if report.Skipped != 1 {
		t.Errorf("Skipped = %d, want 1", report.Skipped)
	}
	if report.Touched != 0 {
		t.Errorf("Touched = %d, want 0", report.Touched)
	}
	if len(b.channels()) != 0 {
		t.Fatalf("backend calls = %v, want none", b.channels())
	}
}

// TestCentralLinksStatusListsChannels verifies the status carries a
// per-channel eligibility list alongside the device-wide count.
func TestCentralLinksStatusListsChannels(t *testing.T) {
	t.Parallel()
	d, _ := buildTwoPressChannelDomain(t)
	status, err := d.CentralLinksStatus("CHFILT01")
	if err != nil {
		t.Fatalf("CentralLinksStatus: %v", err)
	}
	if status.EligibleChannels != 2 {
		t.Errorf("EligibleChannels = %d, want 2", status.EligibleChannels)
	}
	if len(status.Channels) != 2 {
		t.Fatalf("Channels = %v, want 2 entries", status.Channels)
	}
	for _, ch := range status.Channels {
		if !ch.Eligible {
			t.Errorf("channel %s: Eligible = false, want true", ch.Address)
		}
	}
}

// ============================================================
// CentralLinksDomain runReport error / edge-case tests
// ============================================================

// buildUnsupportedInterfaceDomain wires a CUxD device (ineligible
// interface) into a fresh registry, with no writer required since the
// interface check short-circuits before any backend lookup.
func buildUnsupportedInterfaceDomain(t *testing.T) *CentralLinksDomain {
	t.Helper()
	c, err := central.New(central.Config{Name: "ccu-unsupported"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	reg := central.NewRegistry()
	if err := reg.Register(c); err != nil {
		t.Fatalf("reg.Register: %v", err)
	}
	dev := device.New(device.Config{
		InterfaceID: "CUxD",
		Interface:   hmenum.InterfaceCUxD,
		Address:     "CUXRPT01",
		Model:       "CUX-Model",
	})
	c.ModelRegistry.Put(dev)
	w := client.NewValueWriter()
	return NewCentralLinksDomain(reg, w)
}

// TestCentralLinksRemoveUnsupportedInterface verifies that
// RemoveCentralLinks (mirroring the existing CreateCentralLinks
// coverage) rejects a device on an ineligible interface with
// ErrCentralLinksUnsupported — the interface gate applies before
// PRESS_LONG is ever considered.
func TestCentralLinksRemoveUnsupportedInterface(t *testing.T) {
	t.Parallel()
	d := buildUnsupportedInterfaceDomain(t)
	_, err := d.RemoveCentralLinks(context.Background(), "CUXRPT01", "")
	if !errors.Is(err, hmapi.ErrCentralLinksUnsupported) {
		t.Fatalf("err = %v, want ErrCentralLinksUnsupported", err)
	}
}

// TestCentralLinksRemoveNoBackendRegistered verifies that an eligible
// device whose (central, interface) pair has no backend registered on
// the writer surfaces ErrNoCentralLinkBackend rather than panicking or
// silently no-op-ing.
func TestCentralLinksRemoveNoBackendRegistered(t *testing.T) {
	t.Parallel()
	c, err := central.New(central.Config{Name: "ccu-no-backend"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	reg := central.NewRegistry()
	if err := reg.Register(c); err != nil {
		t.Fatalf("reg.Register: %v", err)
	}
	dev := device.New(device.Config{
		InterfaceID: "BidCos-RF",
		Interface:   hmenum.InterfaceBidCosRF,
		Address:     "NOBACK01",
		Model:       "HM-RC-4",
	})
	ch := dev.AddChannel("NOBACK01:1", 1, "KEY", hmenum.ParamsetKeyValues)
	ch.Put(generic.NewDataPoint[bool](generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: "NOBACK01:1",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterPressShort),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeBool,
			Operations: hmenum.OperationsEvent,
		},
	}))
	c.ModelRegistry.Put(dev)

	// Writer exists but nothing was Register()-ed for this central/interface.
	w := client.NewValueWriter()
	d := NewCentralLinksDomain(reg, w)
	_, sErr := d.RemoveCentralLinks(context.Background(), "NOBACK01", "")
	if !errors.Is(sErr, ErrNoCentralLinkBackend) {
		t.Fatalf("err = %v, want ErrNoCentralLinkBackend", sErr)
	}
}

// selectiveFailBackend records every ReportValueUsage call like
// recordingReportBackend, but fails calls matched by failOn — used to
// verify (a) a single value-id failure does not skip the sibling
// value-id call within the same channel, and (b) a single failing
// channel does not abort processing of the remaining channels.
type selectiveFailBackend struct {
	paramsetFakeOps
	failOn func(channelAddress, valueID string) bool
	calls  []reportValueUsageCall
}

func (b *selectiveFailBackend) ReportValueUsage(_ context.Context, channelAddress, valueID string, refCounter int) error {
	b.calls = append(b.calls, reportValueUsageCall{channel: channelAddress, valueID: valueID, refCounter: refCounter})
	if b.failOn != nil && b.failOn(channelAddress, valueID) {
		return errors.New("selective report value usage failure")
	}
	return nil
}

// valueIDsFor returns the value-ids recorded for a given channel, in order.
func (b *selectiveFailBackend) valueIDsFor(channel string) []string {
	var out []string
	for _, c := range b.calls {
		if c.channel == channel {
			out = append(out, c.valueID)
		}
	}
	return out
}

// TestCentralLinksRemovePartialValueIDFailureStillIssuesBoth verifies
// that when PRESS_SHORT succeeds but the second PRESS_LONG call fails,
// the PRESS_LONG call is still issued (the per-channel value-id loop
// does not break on the first error) and the channel is counted Failed
// exactly once, with the error propagated.
func TestCentralLinksRemovePartialValueIDFailureStillIssuesBoth(t *testing.T) {
	t.Parallel()
	b := &selectiveFailBackend{
		failOn: func(_, valueID string) bool { return valueID == reportValueUsageLongValueID },
	}
	d := buildTwoPressChannelDomainWithBackend(t, b)
	report, err := d.RemoveCentralLinks(context.Background(), "CHFILT01", "CHFILT01:1")
	if err == nil {
		t.Fatal("RemoveCentralLinks: want propagated error, got nil")
	}
	if report.Failed != 1 {
		t.Errorf("Failed = %d, want 1", report.Failed)
	}
	if report.Touched != 0 {
		t.Errorf("Touched = %d, want 0", report.Touched)
	}
	if vids := b.valueIDsFor("CHFILT01:1"); len(vids) != 2 || vids[0] != "PRESS_SHORT" || vids[1] != "PRESS_LONG" {
		t.Fatalf("value-ids = %v, want [PRESS_SHORT PRESS_LONG] (both must be attempted)", vids)
	}
}

// TestCentralLinksRemoveMixedChannelFailureContinuesToNextChannel
// verifies that a failure on one channel does not abort processing of
// sibling channels: with channel :1 failing and channel :2 succeeding,
// the report shows one Touched and one Failed, both channels receive
// wire calls, and the (first) error is still propagated.
func TestCentralLinksRemoveMixedChannelFailureContinuesToNextChannel(t *testing.T) {
	t.Parallel()
	b := &selectiveFailBackend{
		failOn: func(channelAddress, _ string) bool { return channelAddress == "CHFILT01:1" },
	}
	d := buildTwoPressChannelDomainWithBackend(t, b)
	report, err := d.RemoveCentralLinks(context.Background(), "CHFILT01", "")
	if err == nil {
		t.Fatal("RemoveCentralLinks: want propagated error, got nil")
	}
	if report.Failed != 1 {
		t.Errorf("Failed = %d, want 1", report.Failed)
	}
	if report.Touched != 1 {
		t.Errorf("Touched = %d, want 1", report.Touched)
	}
	if vids := b.valueIDsFor("CHFILT01:1"); len(vids) != 2 {
		t.Errorf("channel :1 value-ids = %v, want 2 calls attempted despite failure", vids)
	}
	if vids := b.valueIDsFor("CHFILT01:2"); len(vids) != 2 || vids[0] != "PRESS_SHORT" || vids[1] != "PRESS_LONG" {
		t.Errorf("channel :2 value-ids = %v, want [PRESS_SHORT PRESS_LONG] (sibling channel unaffected)", vids)
	}
}
