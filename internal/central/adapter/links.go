// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/ccudata"
	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/north/rest/handlers"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
)

// LinksDomain is the high-level facade REST + SPA code uses to list, add, and
// remove direct links between channels.
type LinksDomain struct {
	registry     *central.Registry
	writer       *client.ValueWriter
	translations *ccudata.Translations
	audit        audit.Recorder
}

// NewLinksDomain wires the adapter. Any of the dependencies may be
// nil — the adapter then degrades gracefully (empty link list, 501
// on writes).
func NewLinksDomain(
	r *central.Registry,
	w *client.ValueWriter,
	t *ccudata.Translations,
) *LinksDomain {
	return &LinksDomain{registry: r, writer: w, translations: t, audit: audit.NoopRecorder()}
}

// SetAuditRecorder rewires the audit recorder. Returns the receiver
// so call sites can chain.
func (d *LinksDomain) SetAuditRecorder(rec audit.Recorder) *LinksDomain {
	if rec == nil {
		rec = audit.NoopRecorder()
	}
	d.audit = rec
	return d
}

// ErrNoLinkBackend is returned when the device does not belong to a
// registered central or its interface has no backend registered.
var ErrNoLinkBackend = errors.New("links: no backend for device")

// ListLinks implements [handlers.LinksService].ListLinks.
//
// Enumerates every link (incoming + outgoing) for a device,
// deduplicates by the (sender, receiver) pair, and enriches each
// entry with device + channel names, localised channel-type labels,
// and direction ("outgoing" / "incoming") relative to the queried
// device.
func (d *LinksDomain) ListLinks(ctx context.Context, deviceAddress, locale string) ([]handlers.Link, error) {
	c, dev, err := d.lookupDevice(deviceAddress)
	if err != nil {
		return nil, err
	}
	backend, ok := d.writer.Backend(c.Name(), dev.InterfaceID)
	if !ok {
		return nil, fmt.Errorf("%w: %s/%s", ErrNoLinkBackend, c.Name(), dev.InterfaceID)
	}

	seen := make(map[string]struct{})
	out := make([]handlers.Link, 0)
	for _, ch := range dev.Channels() {
		raw, err := backend.GetLinks(ctx, ch.Address)
		if err != nil {
			// One failing channel does not abort the enumeration
			// some channels simply do not expose a LINK paramset.
			continue
		}
		for _, link := range raw {
			key := link.Sender + "->" + link.Receiver
			if _, dup := seen[key]; dup {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, d.enrichLink(ctx, dev, link, locale))
		}
	}
	return out, nil
}

// enrichLink fills in the presentation-layer fields (names, labels,
// direction) using the local registry. Cross-central peer lookups
// are supported because we walk the whole registry for the peer
// device.
func (d *LinksDomain) enrichLink(_ context.Context, dev *device.Device, link hmproto.LinkDescription, locale string) handlers.Link {
	senderDevAddr := deviceAddressOf(link.Sender)
	receiverDevAddr := deviceAddressOf(link.Receiver)
	isSender := senderDevAddr == dev.Address

	peerAddr := link.Receiver
	peerDevAddr := receiverDevAddr
	if !isSender {
		peerAddr = link.Sender
		peerDevAddr = senderDevAddr
	}
	peerDev := d.findDevice(peerDevAddr)

	senderDev := dev
	if !isSender {
		senderDev = peerDev
	}
	receiverDev := dev
	if isSender {
		receiverDev = peerDev
	}
	senderChannel := channelOf(senderDev, link.Sender)
	receiverChannel := channelOf(receiverDev, link.Receiver)

	direction := "outgoing"
	if !isSender {
		direction = "incoming"
	}

	return handlers.Link{
		Sender:                   link.Sender,
		Receiver:                 link.Receiver,
		Name:                     link.Name,
		Description:              link.Description,
		Flags:                    link.Flags,
		SenderDeviceName:         deviceNameOr(senderDev, senderDevAddr),
		SenderDeviceModel:        modelOf(senderDev),
		SenderChannelType:        channelTypeOf(senderChannel),
		SenderChannelTypeLabel:   d.channelTypeLabel(locale, senderChannel),
		SenderChannelName:        channelNameOf(senderChannel),
		ReceiverDeviceName:       deviceNameOr(receiverDev, receiverDevAddr),
		ReceiverDeviceModel:      modelOf(receiverDev),
		ReceiverChannelType:      channelTypeOf(receiverChannel),
		ReceiverChannelTypeLabel: d.channelTypeLabel(locale, receiverChannel),
		ReceiverChannelName:      channelNameOf(receiverChannel),
		PeerAddress:              peerAddr,
		PeerDeviceName:           deviceNameOr(peerDev, peerDevAddr),
		PeerDeviceModel:          modelOf(peerDev),
		Direction:                direction,
	}
}

// AddLink creates a link. name is auto-generated when empty.
func (d *LinksDomain) AddLink(ctx context.Context, senderAddress, receiverAddress, name, description string) error {
	senderDev := deviceAddressOf(senderAddress)
	c, dev, err := d.lookupDevice(senderDev)
	if err != nil {
		return err
	}
	backend, ok := d.writer.Backend(c.Name(), dev.InterfaceID)
	if !ok {
		return fmt.Errorf("%w: %s/%s", ErrNoLinkBackend, c.Name(), dev.InterfaceID)
	}
	effectiveName := name
	if strings.TrimSpace(effectiveName) == "" {
		effectiveName = senderAddress + " -> " + receiverAddress
	}
	effectiveDesc := description
	if effectiveDesc == "" {
		effectiveDesc = "created by openccu-loom"
	}
	if err := backend.AddLink(ctx, senderAddress, receiverAddress, effectiveName, effectiveDesc); err != nil {
		return err
	}
	d.audit.Record(audit.Entry{
		Action:        audit.ActionLinkAdd,
		DeviceAddress: deviceAddressOf(senderAddress),
		ChannelNo:     channelNumberOf(senderAddress),
		Peer:          receiverAddress,
		Note:          effectiveName,
	})
	return nil
}

// RemoveLink deletes a link.
func (d *LinksDomain) RemoveLink(ctx context.Context, senderAddress, receiverAddress string) error {
	senderDev := deviceAddressOf(senderAddress)
	c, dev, err := d.lookupDevice(senderDev)
	if err != nil {
		return err
	}
	backend, ok := d.writer.Backend(c.Name(), dev.InterfaceID)
	if !ok {
		return fmt.Errorf("%w: %s/%s", ErrNoLinkBackend, c.Name(), dev.InterfaceID)
	}
	if err := backend.RemoveLink(ctx, senderAddress, receiverAddress); err != nil {
		return err
	}
	d.audit.Record(audit.Entry{
		Action:        audit.ActionLinkRemove,
		DeviceAddress: deviceAddressOf(senderAddress),
		ChannelNo:     channelNumberOf(senderAddress),
		Peer:          receiverAddress,
	})
	return nil
}

// GetLinkParamset reads the LINK paramset on channelAddress keyed by the
// peerAddress. by exposing the existing backend method through the
// WS-friendly LinksDomain surface.
func (d *LinksDomain) GetLinkParamset(ctx context.Context, channelAddress, peerAddress string) (map[string]any, error) {
	c, dev, err := d.lookupDevice(deviceAddressOf(channelAddress))
	if err != nil {
		return nil, err
	}
	backend, ok := d.writer.Backend(c.Name(), dev.InterfaceID)
	if !ok {
		return nil, fmt.Errorf("%w: %s/%s", ErrNoLinkBackend, c.Name(), dev.InterfaceID)
	}
	return backend.GetLinkParamset(ctx, channelAddress, peerAddress)
}

// PutLinkParamset writes values to the LINK paramset on channelAddress keyed
// by peerAddress.
func (d *LinksDomain) PutLinkParamset(ctx context.Context, channelAddress, peerAddress string, values map[string]any) error {
	c, dev, err := d.lookupDevice(deviceAddressOf(channelAddress))
	if err != nil {
		return err
	}
	backend, ok := d.writer.Backend(c.Name(), dev.InterfaceID)
	if !ok {
		return fmt.Errorf("%w: %s/%s", ErrNoLinkBackend, c.Name(), dev.InterfaceID)
	}
	if err := backend.PutLinkParamset(ctx, channelAddress, peerAddress, values); err != nil {
		return err
	}
	d.audit.Record(audit.Entry{
		Action:        audit.ActionLinkParamsetWrite,
		DeviceAddress: deviceAddressOf(channelAddress),
		ChannelNo:     channelNumberOf(channelAddress),
		Peer:          peerAddress,
	})
	return nil
}

// LinkableChannels walks every device in the central registry and
// returns the channels that are valid peers for the source channel.
//
// # The heuristic mirrors
//
// - Skip the source channel itself.
// - Only consider channels that belong to an interface capable of
// direct links (for the MVP: every non-CUxD CCU interface).
// - For "sender" role, return channels that can **receive** a
// link (typically any channel carrying a LINK paramset whose
// type is a receiver pattern like `*_VIRTUAL_RECEIVER`).
// - For "receiver" role, return channels that can **emit** a link
// (sender-type channels).
//
// The full role classification in
// `link_peer_source_categories` / `link_peer_target_categories` sets
// that are derived from the receiver-profile catalogue. We shortcut
// with a simpler check for 0.1.0: presence of a LINK paramset peer
// list on the channel. Refine later when we port the category data.
func (d *LinksDomain) LinkableChannels(
	ctx context.Context,
	interfaceID, sourceChannelAddress, role, locale string,
) ([]handlers.LinkableChannel, error) {
	if d.registry == nil {
		return nil, ErrNoLinkBackend
	}
	sourceDev := deviceAddressOf(sourceChannelAddress)
	out := make([]handlers.LinkableChannel, 0)
	for _, u := range d.registry.List() {
		for _, dev := range u.ModelRegistry.List() {
			if dev.InterfaceID != interfaceID {
				continue
			}
			for _, ch := range dev.Channels() {
				if ch.Address == sourceChannelAddress {
					continue
				}
				if !d.channelMatchesRole(ctx, u.Name(), dev.InterfaceID, ch.Address, role) {
					continue
				}
				out = append(out, handlers.LinkableChannel{
					Address:          ch.Address,
					ChannelType:      ch.Type,
					ChannelTypeLabel: d.channelTypeLabel(locale, ch),
					ChannelName:      ch.Name,
					DeviceAddress:    dev.Address,
					DeviceName:       deviceNameOr(dev, dev.Address),
					DeviceModel:      dev.Model,
				})
			}
		}
	}
	_ = sourceDev
	return out, nil
}

// channelMatchesRole is the lightweight peer filter used by
// LinkableChannels. For the MVP we accept any channel that returns
// a (possibly empty) LinkPeers list — that is, every channel the
// backend reports as link-capable. Refinement to sender vs receiver
// roles lands with the per-channel category port.
func (d *LinksDomain) channelMatchesRole(ctx context.Context, centralName, interfaceID, channelAddress, _ string) bool {
	backend, ok := d.writer.Backend(centralName, interfaceID)
	if !ok {
		return false
	}
	if _, err := backend.GetLinkPeers(ctx, channelAddress); err != nil {
		return false
	}
	return true
}

func (d *LinksDomain) lookupDevice(deviceAddress string) (*central.Unit, *device.Device, error) {
	if d.registry == nil {
		return nil, nil, ErrNoLinkBackend
	}
	for _, u := range d.registry.List() {
		if dev, ok := u.ModelRegistry.Get(deviceAddress); ok {
			return u, dev, nil
		}
	}
	// Device exists in no central — distinct from "central wired but
	// no backend": north-bound mappers translate this to 404 via
	// hmerr.ErrDescriptionNotFound.
	return nil, nil, fmt.Errorf("%w: device %s", hmerr.ErrDescriptionNotFound, deviceAddress)
}

func (d *LinksDomain) findDevice(address string) *device.Device {
	if d.registry == nil || address == "" {
		return nil
	}
	for _, u := range d.registry.List() {
		if dev, ok := u.ModelRegistry.Get(address); ok {
			return dev
		}
	}
	return nil
}

func (d *LinksDomain) channelTypeLabel(locale string, ch *device.Channel) string {
	if ch == nil || ch.Type == "" {
		return ""
	}
	if d.translations == nil {
		return ch.Type
	}
	return d.translations.ChannelType(locale, ch.Type)
}

func channelOf(dev *device.Device, channelAddress string) *device.Channel {
	if dev == nil {
		return nil
	}
	return dev.Channel(channelAddress)
}

func channelTypeOf(ch *device.Channel) string {
	if ch == nil {
		return ""
	}
	return ch.Type
}

func channelNameOf(ch *device.Channel) string {
	if ch == nil {
		return ""
	}
	return ch.Name
}

func deviceNameOr(dev *device.Device, fallback string) string {
	if dev == nil {
		return fallback
	}
	if dev.Name != "" {
		return dev.Name
	}
	return dev.Address
}

func modelOf(dev *device.Device) string {
	if dev == nil {
		return ""
	}
	return dev.Model
}
