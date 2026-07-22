// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"errors"
	"fmt"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/pkg/hmapi"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// CentralLinksDomain implements interfaces.CentralLinksService. The CCU uses
// the per-channel counter to decide whether to forward the PRESS_SHORT /
// PRESS_LONG events to the central — refCounter > 0 switches the flow on,
// refCounter = 0 turns it off.
//
// Only BidCos-RF, BidCos-Wired and HmIP-RF interfaces are eligible (mirrors
// `Device.relevant_for_central_link_management`); CUxD and virtual-device
// interfaces have no concept of central event routing.
type CentralLinksDomain struct {
	registry *central.Registry
	writer   *client.ValueWriter
}

// NewCentralLinksDomain wires the adapter.
func NewCentralLinksDomain(r *central.Registry, w *client.ValueWriter) *CentralLinksDomain {
	return &CentralLinksDomain{registry: r, writer: w}
}

// ErrNoCentralLinkBackend bubbles when the CCU backend cannot be
// resolved.
var ErrNoCentralLinkBackend = errors.New("central-links: no backend for device")

// reportValueUsageValueID is the primary press value-id whose per-channel
// counter gates central click-event forwarding; matches
// `REPORT_VALUE_USAGE_VALUE_ID` in const.py.
const reportValueUsageValueID = "PRESS_SHORT"

// reportValueUsageLongValueID is the second value-id the teardown path
// zeroes. Deactivating a central link must also drop the PRESS_LONG
// ref-counter so the device-internal direct link is fully removed. The
// CCU WebUI removeCentralLink zeroes PRESS_SHORT and PRESS_LONG, while
// its createCentralLink only ever raises PRESS_SHORT — this deactivate
// asymmetry is a deliberate divergence from the reference (which only
// touches PRESS_SHORT on both paths); see docs/parity/by_design.md.
const reportValueUsageLongValueID = "PRESS_LONG"

// CreateCentralLinks enables click-event routing. When channelAddress
// is empty every eligible channel of the device is switched on; when it
// names a channel address only that single channel is touched (mirrors
// the CCU channel-config dialog, which scopes the switch to the opened
// channel). Returns the count of channels touched and the count of
// channels that were skipped (no press events / wrong interface).
func (c *CentralLinksDomain) CreateCentralLinks(ctx context.Context, deviceAddress, channelAddress string) (hmapi.CentralLinksReport, error) {
	return c.runReport(ctx, deviceAddress, channelAddress, 1)
}

// RemoveCentralLinks tears the central click-event routing down. When
// channelAddress is empty every eligible channel of the device is
// switched off; when it names a channel address only that channel is
// touched.
func (c *CentralLinksDomain) RemoveCentralLinks(ctx context.Context, deviceAddress, channelAddress string) (hmapi.CentralLinksReport, error) {
	return c.runReport(ctx, deviceAddress, channelAddress, 0)
}

// CentralLinksStatus reports per-device whether central links are
// applicable / created. The SPA uses it to decide whether to render
// the buttons and what label to show.
func (c *CentralLinksDomain) CentralLinksStatus(deviceAddress string) (hmapi.CentralLinksStatus, error) {
	if c.registry == nil {
		return hmapi.CentralLinksStatus{}, ErrNoCentralLinkBackend
	}
	for _, u := range c.registry.List() {
		dev, ok := u.ModelRegistry.Get(deviceAddress)
		if !ok {
			continue
		}
		if !isCentralLinkInterface(dev.Interface) {
			return hmapi.CentralLinksStatus{
				Supported: false,
				Reason:    "interface_unsupported",
			}, nil
		}
		eligible := 0
		var channels []hmapi.CentralLinksChannelStatus
		for _, ch := range dev.Channels() {
			if !channelHasPressEvents(ch) {
				continue
			}
			eligible++
			channels = append(channels, hmapi.CentralLinksChannelStatus{
				Address:  ch.Address,
				Number:   ch.Number,
				Eligible: true,
			})
		}
		return hmapi.CentralLinksStatus{
			Supported:        true,
			EligibleChannels: eligible,
			Channels:         channels,
		}, nil
	}
	return hmapi.CentralLinksStatus{}, fmt.Errorf("%w: device %s", ErrNoCentralLinkBackend, deviceAddress)
}

func (c *CentralLinksDomain) runReport(ctx context.Context, deviceAddress, channelAddress string, refCounter int) (hmapi.CentralLinksReport, error) {
	if c.registry == nil || c.writer == nil {
		return hmapi.CentralLinksReport{}, ErrNoCentralLinkBackend
	}
	for _, u := range c.registry.List() {
		dev, ok := u.ModelRegistry.Get(deviceAddress)
		if !ok {
			continue
		}
		if !isCentralLinkInterface(dev.Interface) {
			return hmapi.CentralLinksReport{}, hmapi.ErrCentralLinksUnsupported
		}
		channels := dev.Channels()
		if channelAddress != "" {
			ch := findChannelByAddress(channels, channelAddress)
			if ch == nil {
				return hmapi.CentralLinksReport{}, fmt.Errorf("%w: %s", hmapi.ErrCentralLinksChannelNotFound, channelAddress)
			}
			channels = []*device.Channel{ch}
		}
		backend, ok := c.writer.Backend(u.Name(), dev.InterfaceID)
		if !ok {
			return hmapi.CentralLinksReport{}, fmt.Errorf("%w: %s/%s", ErrNoCentralLinkBackend, u.Name(), dev.InterfaceID)
		}
		caller, ok := backend.(centralLinkBackend)
		if !ok {
			return hmapi.CentralLinksReport{}, hmapi.ErrCentralLinksUnsupported
		}
		// Activation raises only PRESS_SHORT (matching both the CCU WebUI
		// createCentralLink and the reference). Teardown zeroes PRESS_SHORT
		// and PRESS_LONG per channel so the device-internal direct link is
		// fully removed; see docs/parity/by_design.md.
		valueIDs := []string{reportValueUsageValueID}
		if refCounter == 0 {
			valueIDs = append(valueIDs, reportValueUsageLongValueID)
		}
		report := hmapi.CentralLinksReport{}
		var firstErr error
		for _, ch := range channels {
			if !channelHasPressEvents(ch) {
				report.Skipped++
				continue
			}
			// A channel is counted once regardless of how many value-ids it
			// takes; the first wire error marks the channel failed.
			var chErr error
			for _, valueID := range valueIDs {
				if err := caller.ReportValueUsage(ctx, ch.Address, valueID, refCounter); err != nil && chErr == nil {
					chErr = err
				}
			}
			if chErr != nil {
				if firstErr == nil {
					firstErr = chErr
				}
				report.Failed++
				continue
			}
			report.Touched++
		}
		if firstErr != nil {
			return report, fmt.Errorf("central-links: %w", firstErr)
		}
		return report, nil
	}
	return hmapi.CentralLinksReport{}, fmt.Errorf("%w: device %s", ErrNoCentralLinkBackend, deviceAddress)
}

// findChannelByAddress returns the channel whose address matches, or nil.
func findChannelByAddress(channels []*device.Channel, address string) *device.Channel {
	for _, ch := range channels {
		if ch != nil && ch.Address == address {
			return ch
		}
	}
	return nil
}

// centralLinkBackend is the slim slice of backends.Operations the
// adapter actually needs — keeps tests free from the full surface.
type centralLinkBackend interface {
	ReportValueUsage(ctx context.Context, channelAddress, valueID string, refCounter int) error
}

// IsCentralLinkInterface mirrors
// `relevant_for_central_link_management`. CUxD and VirtualDevices
// do not participate in central event routing.
func isCentralLinkInterface(iface hmenum.Interface) bool {
	switch iface {
	case hmenum.InterfaceBidCosRF, hmenum.InterfaceBidCosWired, hmenum.InterfaceHmIPRF:
		return true
	case hmenum.InterfaceVirtualDevices, hmenum.InterfaceCUxD:
		return false
	}
	return false
}

// channelHasPressEvents reports whether the channel exposes PRESS_SHORT /
// PRESS_LONG as a generic event parameter — the minimal indicator that the
// channel can drive central click events.
func channelHasPressEvents(ch *device.Channel) bool {
	if ch == nil {
		return false
	}
	if dp := ch.ParamsetParameter(hmenum.ParamsetKeyValues, hmenum.ParameterPressShort); dp != nil {
		return true
	}
	if dp := ch.ParamsetParameter(hmenum.ParamsetKeyValues, hmenum.ParameterPressLong); dp != nil {
		return true
	}
	return false
}
