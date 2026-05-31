// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package coordinators

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"

	"github.com/SukramJ/openccu-loom/internal/observability"
)

// DeviceLink is one direct link between two channels.
type DeviceLink struct {
	SenderAddress       string
	ReceiverAddress     string
	Name                string
	Description         string
	Flags               int
	SenderDeviceModel   string
	ReceiverDeviceModel string
	Direction           string // "outgoing" or "incoming"

	// Extended fields — populated when the underlying client returns
	// richer link metadata.
	LinkType      string // e.g. "DIRECT", "VIRTUAL"
	PeerName      string // operator-assigned name of the peer device
	Address       string // channel address (canonical form)
	Channel       string // channel number or label
	Group         string // optional link group identifier
	PeerInterface string // interface of the peer channel
	PeerSerial    string // serial number of the peer device
	PeerType      string // device type of the peer (e.g. "HmIP-PSM")
	PeerSubtype   string // subtype discriminator for the peer
}

// LinkableChannel is a candidate for a new link.
type LinkableChannel struct {
	Address       string
	ChannelType   string
	DeviceAddress string
	DeviceModel   string
}

// LinkClient is the narrow outbound surface the coordinator needs.
// Implementations live alongside [InterfaceClient]. `deviceAddress`
// is passed verbatim so the client can look up the right interface.
type LinkClient interface {
	AddLink(ctx context.Context, sender, receiver, name, description string) error
	RemoveLink(ctx context.Context, sender, receiver string) error
	GetLinks(ctx context.Context, deviceAddress string) ([]DeviceLink, error)
	GetLinkableChannels(ctx context.Context, deviceAddress string) ([]LinkableChannel, error)
	SetLinkInfo(ctx context.Context, sender, receiver, name, description string) error
	GetLinkInfo(ctx context.Context, sender, receiver string) (DeviceLink, error)
}

// ErrLinkClientMissing is returned when no [LinkClient] has been
// wired for the device's interface.
var ErrLinkClientMissing = errors.New("link: no client wired for device")

// ClientResolver maps a device address to the [LinkClient] that
// owns its interface. Returns (nil, false) when no client is known.
type ClientResolver func(deviceAddress string) (LinkClient, bool)

// LinkCoordinator is the MVP Go port.
// LinkCoordinator. It has no internal state beyond the resolver; the
// device registry + wire clients live elsewhere.
type LinkCoordinator struct {
	mu       sync.RWMutex
	resolver ClientResolver
	recorder observability.Recorder
}

// NewLinkCoordinator constructs a coordinator bound to a resolver.
func NewLinkCoordinator(resolver ClientResolver) *LinkCoordinator {
	return &LinkCoordinator{resolver: resolver, recorder: observability.NoopRecorder{}}
}

// SetRecorder rewires the observability recorder. Returns the receiver
// so callers can chain.
func (c *LinkCoordinator) SetRecorder(rec observability.Recorder) *LinkCoordinator {
	if rec == nil {
		rec = observability.NoopRecorder{}
	}
	c.mu.Lock()
	c.recorder = rec
	c.mu.Unlock()
	return c
}

func (c *LinkCoordinator) snapshotRecorder() observability.Recorder {
	c.mu.RLock()
	rec := c.recorder
	c.mu.RUnlock()
	if rec == nil {
		return observability.NoopRecorder{}
	}
	return rec
}

// SetResolver swaps the resolver. Safe to call while the coordinator
// is serving requests; concurrent readers see the previous or new
// resolver atomically.
func (c *LinkCoordinator) SetResolver(r ClientResolver) {
	c.mu.Lock()
	c.resolver = r
	c.mu.Unlock()
}

// AddLink creates a direct link between two channels.
func (c *LinkCoordinator) AddLink(ctx context.Context, sender, receiver, name, description string) error {
	return observability.Instrument(ctx, c.snapshotRecorder(), "link_coordinator.add_link", observability.ScopeCoordinator,
		func(ctx context.Context) error {
			client, err := c.resolve(sender)
			if err != nil {
				return err
			}
			if name == "" {
				name = fmt.Sprintf("%s -> %s", sender, receiver)
			}
			return client.AddLink(ctx, sender, receiver, name, description)
		})
}

// RemoveLink tears a direct link down.
func (c *LinkCoordinator) RemoveLink(ctx context.Context, sender, receiver string) error {
	return observability.Instrument(ctx, c.snapshotRecorder(), "link_coordinator.remove_link", observability.ScopeCoordinator,
		func(ctx context.Context) error {
			client, err := c.resolve(sender)
			if err != nil {
				return err
			}
			return client.RemoveLink(ctx, sender, receiver)
		})
}

// GetLinks lists every direct link a device participates in, sorted
// by (sender, receiver) for stable iteration.
func (c *LinkCoordinator) GetLinks(ctx context.Context, deviceAddress string) ([]DeviceLink, error) {
	return observability.InstrumentValue(ctx, c.snapshotRecorder(), "link_coordinator.get_links", observability.ScopeCoordinator,
		func(ctx context.Context) ([]DeviceLink, error) {
			client, err := c.resolve(deviceAddress)
			if err != nil {
				return nil, err
			}
			links, err := client.GetLinks(ctx, deviceAddress)
			if err != nil {
				return nil, err
			}
			sort.Slice(links, func(i, j int) bool {
				if links[i].SenderAddress != links[j].SenderAddress {
					return links[i].SenderAddress < links[j].SenderAddress
				}
				return links[i].ReceiverAddress < links[j].ReceiverAddress
			})
			return links, nil
		})
}

// GetLinkableChannels enumerates channels that could become the peer
// of a new direct link.
func (c *LinkCoordinator) GetLinkableChannels(ctx context.Context, deviceAddress string) ([]LinkableChannel, error) {
	return observability.InstrumentValue(ctx, c.snapshotRecorder(), "link_coordinator.linkable_channels", observability.ScopeCoordinator,
		func(ctx context.Context) ([]LinkableChannel, error) {
			client, err := c.resolve(deviceAddress)
			if err != nil {
				return nil, err
			}
			return client.GetLinkableChannels(ctx, deviceAddress)
		})
}

// GetLinksForLocale lists every direct link a device participates in,
// filtered by role and sorted by (sender, receiver). locale is currently
// reserved for future i18n label translation; role filters the result:
// "" = all, "sender" = only outgoing links, "receiver" = only incoming.
func (c *LinkCoordinator) GetLinksForLocale(ctx context.Context, deviceAddress, locale, role string) ([]DeviceLink, error) {
	links, err := c.GetLinks(ctx, deviceAddress)
	if err != nil {
		return nil, err
	}
	if role == "" {
		return links, nil
	}
	out := links[:0]
	for i := range links {
		if links[i].Direction == role {
			out = append(out, links[i])
		}
	}
	return out, nil
}

// GetLinkableChannelsForLocale enumerates linkable channels, filtered by
// role. locale is reserved for future i18n label translation; role is
// a free-form filter string applied against ChannelType (exact match, "" =
// all). This is the extended-signature variant of [GetLinkableChannels].
func (c *LinkCoordinator) GetLinkableChannelsForLocale(ctx context.Context, deviceAddress, locale, role string) ([]LinkableChannel, error) {
	channels, err := c.GetLinkableChannels(ctx, deviceAddress)
	if err != nil {
		return nil, err
	}
	if role == "" {
		return channels, nil
	}
	out := channels[:0]
	for i := range channels {
		if channels[i].ChannelType == role {
			out = append(out, channels[i])
		}
	}
	return out, nil
}

// SetLinkInfo updates the human-readable name / description of an
// existing link.
func (c *LinkCoordinator) SetLinkInfo(ctx context.Context, sender, receiver, name, description string) error {
	return observability.Instrument(ctx, c.snapshotRecorder(), "link_coordinator.set_link_info", observability.ScopeCoordinator,
		func(ctx context.Context) error {
			client, err := c.resolve(sender)
			if err != nil {
				return err
			}
			return client.SetLinkInfo(ctx, sender, receiver, name, description)
		})
}

// GetLinkInfo returns the metadata of a specific link.
func (c *LinkCoordinator) GetLinkInfo(ctx context.Context, sender, receiver string) (DeviceLink, error) {
	return observability.InstrumentValue(ctx, c.snapshotRecorder(), "link_coordinator.get_link_info", observability.ScopeCoordinator,
		func(ctx context.Context) (DeviceLink, error) {
			client, err := c.resolve(sender)
			if err != nil {
				return DeviceLink{}, err
			}
			return client.GetLinkInfo(ctx, sender, receiver)
		})
}

// resolve extracts the device address (drop the `:channel` suffix)
// and looks up the client.
func (c *LinkCoordinator) resolve(channelOrDevice string) (LinkClient, error) {
	c.mu.RLock()
	resolver := c.resolver
	c.mu.RUnlock()
	if resolver == nil {
		return nil, ErrLinkClientMissing
	}
	addr := deviceAddressOf(channelOrDevice)
	client, ok := resolver(addr)
	if !ok || client == nil {
		return nil, fmt.Errorf("link: %w (%s)", ErrLinkClientMissing, addr)
	}
	return client, nil
}

// deviceAddressOf strips a trailing `:channel` suffix.
func deviceAddressOf(s string) string {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == ':' {
			return s[:i]
		}
	}
	return s
}
