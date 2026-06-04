// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"errors"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/coordinators"
)

// linkClientAdapter bridges the existing per-CCU [*LinksDomain] (which
// already routes AddLink / RemoveLink / ListLinks / LinkableChannels
// through the device registry, value writer, and translations) onto
// the narrow [coordinators.LinkClient] surface the
// [coordinators.LinkCoordinator] consumes.
//
// Type conversions:
//
// - LinksDomain.ListLinks returns `[]handlers.Link` (locale-aware,
// translated). LinkCoordinator.GetLinks returns
// `[]coordinators.DeviceLink` (locale-agnostic). The adapter
// converts the relevant subset of fields per row.
// - LinksDomain.LinkableChannels returns
// `[]handlers.LinkableChannel`; LinkCoordinator's signature uses
// `[]coordinators.LinkableChannel`. Same shape, different
// namespace.
//
// SetLinkInfo / GetLinkInfo do not have a [LinksDomain] counterpart
// in 0.1.0; the adapter returns [errLinkInfoUnsupported] for them. The
// wiring closure is the LinkCoordinator entry
// point being functional for the AddLink / RemoveLink / GetLinks
// LinkableChannels paths — those are what HA / configui actually use
// today.
type linkClientAdapter struct {
	domain *LinksDomain
}

// errLinkInfoUnsupported is the placeholder for the two methods that
// do not yet have a Domain counterpart. Callers detect via
// [errors.Is] / [errors.As].
var errLinkInfoUnsupported = errors.New("link: SetLinkInfo / GetLinkInfo not yet wired through LinksDomain")

// AddLink delegates to [LinksDomain.AddLink].
func (a *linkClientAdapter) AddLink(ctx context.Context, sender, receiver, name, description string) error {
	if a == nil || a.domain == nil {
		return coordinators.ErrLinkClientMissing
	}
	return a.domain.AddLink(ctx, sender, receiver, name, description)
}

// RemoveLink delegates to [LinksDomain.RemoveLink].
func (a *linkClientAdapter) RemoveLink(ctx context.Context, sender, receiver string) error {
	if a == nil || a.domain == nil {
		return coordinators.ErrLinkClientMissing
	}
	return a.domain.RemoveLink(ctx, sender, receiver)
}

// GetLinks delegates to [LinksDomain.ListLinks] using the empty
// locale (the LinkCoordinator surface is locale-agnostic) and
// converts the resulting `handlers.Link` rows into
// `coordinators.DeviceLink`.
func (a *linkClientAdapter) GetLinks(ctx context.Context, deviceAddress string) ([]coordinators.DeviceLink, error) {
	if a == nil || a.domain == nil {
		return nil, coordinators.ErrLinkClientMissing
	}
	rows, err := a.domain.ListLinks(ctx, deviceAddress, "")
	if err != nil {
		return nil, err
	}
	out := make([]coordinators.DeviceLink, 0, len(rows))
	for i := range rows {
		direction := "outgoing"
		if rows[i].Receiver != "" && rows[i].Sender == "" {
			direction = "incoming"
		}
		out = append(out, coordinators.DeviceLink{
			SenderAddress:       rows[i].Sender,
			ReceiverAddress:     rows[i].Receiver,
			Name:                rows[i].Name,
			Description:         rows[i].Description,
			Flags:               rows[i].Flags,
			SenderDeviceModel:   rows[i].SenderDeviceModel,
			ReceiverDeviceModel: rows[i].ReceiverDeviceModel,
			Direction:           direction,
		})
	}
	return out, nil
}

// GetLinkableChannels is intentionally not delegated to
// [LinksDomain.LinkableChannels] because the [LinksDomain] signature
// requires `(interfaceID, sourceChannelAddress, role, locale)` which
// the [coordinators.LinkClient] surface does not carry. Callers that
// need linkable-channel discovery use the LinksDomain directly via
// the WS `links.linkable_channels` command. Returns the unsupported
// sentinel so this distinction is explicit.
func (a *linkClientAdapter) GetLinkableChannels(_ context.Context, _ string) ([]coordinators.LinkableChannel, error) {
	return nil, errLinkableChannelsRouteThroughDomain
}

// errLinkableChannelsRouteThroughDomain documents that
// GetLinkableChannels is reachable via the LinksDomain surface, not
// via the LinkCoordinator's narrow contract.
var errLinkableChannelsRouteThroughDomain = errors.New("link: GetLinkableChannels — call LinksDomain.LinkableChannels directly (needs interfaceID + role)")

// SetLinkInfo is not yet wired; returns a sentinel error so callers
// can distinguish "function not implemented" from a transport error.
func (a *linkClientAdapter) SetLinkInfo(ctx context.Context, sender, receiver, name, description string) error {
	_ = ctx
	_ = sender
	_ = receiver
	_ = name
	_ = description
	return errLinkInfoUnsupported
}

// GetLinkInfo is not yet wired; see [linkClientAdapter.SetLinkInfo].
func (a *linkClientAdapter) GetLinkInfo(ctx context.Context, sender, receiver string) (coordinators.DeviceLink, error) {
	_ = ctx
	_ = sender
	_ = receiver
	return coordinators.DeviceLink{}, errLinkInfoUnsupported
}

// WireLinkCoordinator installs the [linkClientAdapter] as the
// [coordinators.LinkCoordinator]'s ClientResolver. The resolver
// returns the same adapter for any non-empty device address — the
// LinksDomain handles per-device dispatch internally via the
// CentralRegistry.
//
// Callers wire this once at daemon boot, after the LinksDomain has
// been constructed. Returns an error when the central or domain is
// nil.
func WireLinkCoordinator(u *central.Unit, domain *LinksDomain) error {
	if u == nil {
		return errors.New("link: WireLinkCoordinator: central is nil")
	}
	if domain == nil {
		return errors.New("link: WireLinkCoordinator: domain is nil")
	}
	if u.Link == nil {
		return errors.New("link: WireLinkCoordinator: central.Link is nil")
	}
	adapter := &linkClientAdapter{domain: domain}
	u.SetLinkResolver(func(deviceAddress string) (coordinators.LinkClient, bool) {
		if deviceAddress == "" {
			return nil, false
		}
		return adapter, true
	})
	return nil
}
