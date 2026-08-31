// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

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
	"github.com/SukramJ/openccu-loom/pkg/hmapi"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
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

// ListLinks implements [interfaces.LinksService].ListLinks.
//
// Enumerates every link (incoming + outgoing) for a device,
// deduplicates by the (sender, receiver) pair, and enriches each
// entry with device + channel names, localised channel-type labels,
// and direction ("outgoing" / "incoming") relative to the queried
// device.
func (d *LinksDomain) ListLinks(ctx context.Context, deviceAddress, locale string) ([]hmapi.Link, error) {
	c, dev, err := d.lookupDevice(deviceAddress)
	if err != nil {
		return nil, err
	}
	backend, ok := d.writer.Backend(c.Name(), hmtypes.ParseWireInterfaceID(dev.InterfaceID))
	if !ok {
		return nil, fmt.Errorf("%w: %s/%s", ErrNoLinkBackend, c.Name(), dev.InterfaceID)
	}

	seen := make(map[string]struct{})
	out := make([]hmapi.Link, 0)
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
func (d *LinksDomain) enrichLink(_ context.Context, dev *device.Device, link hmproto.LinkDescription, locale string) hmapi.Link {
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

	return hmapi.Link{
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

// ListAllLinks aggregates every direct link across all registered
// centrals (or a single central when centralName is non-empty). It
// issues one empty-address getLinks per (central, interface) — the
// interface-wide roster the CCU WebUI's own links.cgi uses — instead of
// the per-channel loop of [ListLinks], deduplicates per central by the
// (sender, receiver) pair, and enriches each entry symmetrically (no
// "queried device", so no relative direction). Every returned link
// carries its owning central_name + interface_id.
//
// An unknown central returns [hmerr.ErrUnknownCentral]; in aggregate
// mode a central whose interface backend is missing, unsupported
// (CUxD), or offline contributes nothing rather than aborting the whole
// listing.
func (d *LinksDomain) ListAllLinks(ctx context.Context, centralName, locale string) ([]hmapi.Link, error) {
	if d.registry == nil || d.writer == nil {
		return nil, ErrNoLinkBackend
	}
	var units []*central.Unit
	if centralName != "" {
		unit, ok := d.registry.Get(centralName)
		if !ok || unit == nil {
			return nil, hmerr.ErrUnknownCentral
		}
		units = []*central.Unit{unit}
	} else {
		units = d.registry.List()
	}

	out := make([]hmapi.Link, 0)
	for _, unit := range units {
		if unit == nil {
			continue
		}
		// Dedup is scoped per central: BidCos / VCU addresses repeat
		// across CCUs, so a global key must not collapse them.
		seen := make(map[string]struct{})
		for _, ifaceID := range d.linkInterfaceIDs(unit) {
			backend, ok := d.writer.Backend(unit.Name(), hmtypes.ParseWireInterfaceID(ifaceID))
			if !ok {
				continue
			}
			raw, err := backend.GetLinks(ctx, "")
			if err != nil {
				continue
			}
			for _, link := range raw {
				key := link.Sender + "->" + link.Receiver
				if _, dup := seen[key]; dup {
					continue
				}
				seen[key] = struct{}{}
				out = append(out, d.enrichGlobalLink(unit, ifaceID, link, locale))
			}
		}
	}
	return out, nil
}

// linkInterfaceIDs returns the distinct interface ids of a central's
// devices. Derived from the model registry so each id equals the exact
// [client.ValueWriter.Backend] key (avoids the wire-id vs
// [hmenum.Interface] mismatch a description-registry enumeration would
// introduce). A link-bearing interface always has devices, so nothing
// link-relevant is missed.
func (d *LinksDomain) linkInterfaceIDs(unit *central.Unit) []string {
	seen := make(map[string]struct{})
	out := make([]string, 0)
	for _, dev := range unit.ModelRegistry.List() {
		if dev.InterfaceID == "" {
			continue
		}
		if _, dup := seen[dev.InterfaceID]; dup {
			continue
		}
		seen[dev.InterfaceID] = struct{}{}
		out = append(out, dev.InterfaceID)
	}
	return out
}

// enrichGlobalLink fills the presentation fields of a link for the
// global overview. Unlike [enrichLink] it has no queried device, so it
// resolves both endpoints symmetrically and leaves Direction /
// PeerAddress empty; the SPA renders sender→receiver canonically.
func (d *LinksDomain) enrichGlobalLink(
	unit *central.Unit, interfaceID string, link hmproto.LinkDescription, locale string,
) hmapi.Link {
	senderDevAddr := deviceAddressOf(link.Sender)
	receiverDevAddr := deviceAddressOf(link.Receiver)
	senderDev := d.findDevice(senderDevAddr)
	receiverDev := d.findDevice(receiverDevAddr)
	senderChannel := channelOf(senderDev, link.Sender)
	receiverChannel := channelOf(receiverDev, link.Receiver)

	return hmapi.Link{
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
		CentralName:              unit.Name(),
		InterfaceID:              interfaceID,
	}
}

// AddLink creates a link. name is auto-generated when empty.
func (d *LinksDomain) AddLink(ctx context.Context, senderAddress, receiverAddress, name, description string) error {
	senderDev := deviceAddressOf(senderAddress)
	c, dev, err := d.lookupDevice(senderDev)
	if err != nil {
		return err
	}
	backend, ok := d.writer.Backend(c.Name(), hmtypes.ParseWireInterfaceID(dev.InterfaceID))
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

// SetLinkInfo updates the human-readable name and description of an
// existing direct link between two channels. The sender's device
// resolves the owning central and interface (mirrors [ListLinks] /
// [AddLink]); the interface id is forwarded to the JSON-RPC
// Interface.setLinkInfo call. name and description are written verbatim
// so an operator can also clear either field by passing an empty string.
func (d *LinksDomain) SetLinkInfo(ctx context.Context, senderAddress, receiverAddress, name, description string) error {
	senderDev := deviceAddressOf(senderAddress)
	c, dev, err := d.lookupDevice(senderDev)
	if err != nil {
		return err
	}
	backend, ok := d.writer.Backend(c.Name(), hmtypes.ParseWireInterfaceID(dev.InterfaceID))
	if !ok {
		return fmt.Errorf("%w: %s/%s", ErrNoLinkBackend, c.Name(), dev.InterfaceID)
	}
	if _, err := backend.SetLinkInfo(ctx, dev.InterfaceID, senderAddress, receiverAddress, name, description); err != nil {
		return err
	}
	d.audit.Record(audit.Entry{
		Action:        audit.ActionLinkUpdate,
		DeviceAddress: deviceAddressOf(senderAddress),
		ChannelNo:     channelNumberOf(senderAddress),
		Peer:          receiverAddress,
		Note:          name,
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
	backend, ok := d.writer.Backend(c.Name(), hmtypes.ParseWireInterfaceID(dev.InterfaceID))
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

// ActivateLink triggers the receiver's LINK-paramset behaviour for the
// given sender — the CCU's "test link" / simulate-keypress probe. It
// physically actuates the receiver. The LINK paramset lives on the
// RECEIVER, so the owning central + interface are resolved from the
// receiver device (unlike AddLink/RemoveLink, which resolve from the
// sender). longPress selects the LONG_* action group.
func (d *LinksDomain) ActivateLink(ctx context.Context, receiverChannelAddress, senderChannelAddress string, longPress bool) error {
	receiverDev := deviceAddressOf(receiverChannelAddress)
	c, dev, err := d.lookupDevice(receiverDev)
	if err != nil {
		return err
	}
	backend, ok := d.writer.Backend(c.Name(), hmtypes.ParseWireInterfaceID(dev.InterfaceID))
	if !ok {
		return fmt.Errorf("%w: %s/%s", ErrNoLinkBackend, c.Name(), dev.InterfaceID)
	}
	if err := backend.ActivateLinkParamset(ctx, receiverChannelAddress, senderChannelAddress, longPress); err != nil {
		return err
	}
	note := "short"
	if longPress {
		note = "long"
	}
	d.audit.Record(audit.Entry{
		Action:        audit.ActionLinkActivate,
		DeviceAddress: receiverDev,
		ChannelNo:     channelNumberOf(receiverChannelAddress),
		Peer:          senderChannelAddress,
		Note:          note,
	})
	return nil
}

// LinkableChannels walks every device in the central registry and
// returns the channels that are valid peers for the source channel.
//
// The heuristic:
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
// The full role classification uses the `link_peer_source_categories` /
// `link_peer_target_categories` sets that are derived from the
// receiver-profile catalogue. We shortcut with a simpler check for
// 0.1.0: presence of a LINK paramset peer list on the channel. Refine
// later when we port the category data.
func (d *LinksDomain) LinkableChannels(
	_ context.Context,
	interfaceID, sourceChannelAddress, role, locale string,
) ([]hmapi.LinkableChannel, error) {
	if d.registry == nil {
		return nil, ErrNoLinkBackend
	}
	// Resolve the source channel once and pick its role set for the
	// requested direction: a "sender" source is matched by its
	// LinkSourceRoles against each candidate's LinkTargetRoles; a
	// "receiver" source by its LinkTargetRoles against LinkSourceRoles.
	srcCh := channelOf(d.findDevice(deviceAddressOf(sourceChannelAddress)), sourceChannelAddress)
	var srcRoles []string
	if srcCh != nil {
		switch role {
		case "sender":
			srcRoles = srcCh.LinkSourceRoles()
		case "receiver":
			srcRoles = srcCh.LinkTargetRoles()
		}
	}
	out := make([]hmapi.LinkableChannel, 0)
	for _, u := range d.registry.List() {
		for _, dev := range u.ModelRegistry.List() {
			if dev.InterfaceID != interfaceID {
				continue
			}
			for _, ch := range dev.Channels() {
				if ch.Address == sourceChannelAddress {
					continue
				}
				if !channelMatchesRole(role, srcRoles, srcCh != nil, ch) {
					continue
				}
				out = append(out, hmapi.LinkableChannel{
					Address:          ch.Address,
					ChannelType:      ch.Type,
					ChannelTypeLabel: d.channelTypeLabel(locale, ch),
					ChannelName:      ch.Name(),
					DeviceAddress:    dev.Address,
					DeviceName:       deviceNameOr(dev, dev.Address),
					DeviceModel:      dev.Model,
				})
			}
		}
	}
	return out, nil
}

// channelMatchesRole reports whether a candidate channel can be linked
// to the source channel in the requested direction. It intersects the
// raw CCU LINK_*_ROLES tokens exactly like the CCU WebUI's
// check_role_match (occu WebUI/www/tools/devconfig.cgi:970): a "sender"
// source is paired with a candidate that can RECEIVE (its
// LinkTargetRoles), a "receiver" source with a candidate that can SEND
// (its LinkSourceRoles).
//
// A role other than sender/receiver (the device-level WS probe passes
// "") matches every channel, preserving that path's behaviour. When the
// source carries roles for the direction a true token intersection is
// required. When it carries none: a present source that is genuinely
// role-less for the direction cannot act in it (excluded); a totally
// absent source channel (the WS device-address probe) degrades to a
// directional presence check so the list stays useful.
func channelMatchesRole(role string, srcRoles []string, srcPresent bool, ch *device.Channel) bool {
	if role != "sender" && role != "receiver" {
		return true
	}
	var candRoles []string
	if role == "sender" {
		candRoles = ch.LinkTargetRoles()
	} else {
		candRoles = ch.LinkSourceRoles()
	}
	if len(srcRoles) > 0 {
		return intersects(srcRoles, candRoles)
	}
	if srcPresent {
		return false
	}
	return len(candRoles) > 0
}

// intersects reports whether a and b share at least one token — the
// non-empty set intersection the CCU uses to match link roles.
func intersects(a, b []string) bool {
	if len(a) == 0 || len(b) == 0 {
		return false
	}
	set := make(map[string]struct{}, len(a))
	for _, x := range a {
		set[x] = struct{}{}
	}
	for _, y := range b {
		if _, ok := set[y]; ok {
			return true
		}
	}
	return false
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
	return ch.Name()
}

func deviceNameOr(dev *device.Device, fallback string) string {
	if dev == nil {
		return fallback
	}
	if dev.Name() != "" {
		return dev.Name()
	}
	return dev.Address
}

func modelOf(dev *device.Device) string {
	if dev == nil {
		return ""
	}
	return dev.Model
}
