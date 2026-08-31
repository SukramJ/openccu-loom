// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// link_profiles_adapter.go — bridges the linkprofile.Store onto the
// ws.LinkProfilesProvider interface declared in
// internal/north/rest/ws/commands_missing.go.
//
// The WS layer passes channel *addresses* (e.g. "VCU0001:1"); the
// linkprofile.Store keys on channel *types* (e.g. "KEY_TRANSCEIVER").
// This adapter resolves addresses → types via the central registry,
// delegates profile lookups to the store, and serialises results to
// []map[string]any for the WS layer.
//
// The active profile is not a stored fact: it is derived by reading the
// link's current LINK paramset and matching those values against the
// archive's constraint sets. That is why this adapter needs a paramset
// reader as well as the store — the answer cannot be produced from the
// archive alone.

package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/store/linkprofile"
)

// LinkParamsetReader reads the LINK paramset of one (channel, peer)
// pair. Declared here rather than imported because this package is the
// consumer; [ParamsetsDomain] satisfies it.
type LinkParamsetReader interface {
	GetLinkParamset(ctx context.Context, channelAddress, peerAddress string) (map[string]any, error)
}

// LinkProfilesAdapter implements ws.LinkProfilesProvider.
//
// Construct with [NewLinkProfilesAdapter]; the zero value is not
// useful.
type LinkProfilesAdapter struct {
	registry  *central.Registry
	store     *linkprofile.Store
	paramsets LinkParamsetReader
}

// NewLinkProfilesAdapter wires the adapter. A nil reader is tolerated:
// profiles still resolve, and the active profile is reported as none,
// which is what a caller that cannot read the link's current values
// must be told.
func NewLinkProfilesAdapter(r *central.Registry, s *linkprofile.Store, p LinkParamsetReader) *LinkProfilesAdapter {
	return &LinkProfilesAdapter{registry: r, store: s, paramsets: p}
}

// GetLinkProfiles implements ws.LinkProfilesProvider.
//
// Resolves receiver and sender channel addresses to their channel
// types, then delegates to the store. Returns an empty slice (and nil
// error) when no profiles are registered for the pair — the SPA
// falls back to the raw parameter editor.
//
// receiverChannelAddr / senderChannelAddr may be either a bare device
// address ("VCU0001") or a channel address ("VCU0001:1"). Both forms
// are resolved against the model registry.
func (a *LinkProfilesAdapter) GetLinkProfiles(
	ctx context.Context,
	receiverChannelAddr, senderChannelAddr, locale string,
) (profiles []map[string]any, activeID int, err error) {
	if a.store == nil {
		return nil, 0, nil
	}
	receiverType := a.resolveChannelType(receiverChannelAddr)
	senderType := a.resolveChannelType(senderChannelAddr)

	profs, err := a.store.GetLinkProfiles(ctx, receiverType, senderType, locale)
	if err != nil {
		return nil, 0, fmt.Errorf("link profiles: %w", err)
	}
	if len(profs) == 0 {
		return nil, 0, nil
	}
	activeID = a.activeProfileID(ctx, receiverChannelAddr, senderChannelAddr, receiverType, senderType)
	// Convert []linkprofile.Profile → []map[string]any via JSON so the
	// WS layer gets a uniform opaque shape.
	raw, err := json.Marshal(profs)
	if err != nil {
		return nil, 0, fmt.Errorf("link profiles: encode: %w", err)
	}
	var out []map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, 0, fmt.Errorf("link profiles: decode: %w", err)
	}
	return out, activeID, nil
}

// activeProfileID reads the link's current LINK paramset and asks the
// store which profile those values satisfy. Zero means none matched, and
// it is also the honest answer when the values cannot be read at all —
// a link whose current state is unknown has no known active profile.
// A read failure is not propagated: the profile list is still useful
// without it, and the caller has no remedy for a transport error here.
func (a *LinkProfilesAdapter) activeProfileID(
	ctx context.Context,
	receiverChannelAddr, senderChannelAddr, receiverType, senderType string,
) int {
	if a.paramsets == nil {
		return 0
	}
	// The LINK paramset is keyed by the pair, with the receiver as the
	// channel and the sender as the peer.
	values, err := a.paramsets.GetLinkParamset(ctx, receiverChannelAddr, senderChannelAddr)
	if err != nil || len(values) == 0 {
		return 0
	}
	return a.store.MatchActiveProfile(receiverType, senderType, values)
}

// TestLinkProfile implements ws.LinkProfilesProvider.
//
// Resolves receiver and sender channel addresses to their channel types,
// then fetches the fixed parameter values for the given profileID from the
// Embedded Returns a success map with applied_values
// when the profile is found, or a non-error map with unsupported=true when
// no profile data exists for the channel-type pair.
func (a *LinkProfilesAdapter) TestLinkProfile(
	ctx context.Context,
	interfaceID, senderAddr, receiverAddr string,
	profileID int,
) (map[string]any, error) {
	if a.store == nil {
		return nil, errors.New("link profiles: store not wired")
	}
	receiverType := a.resolveChannelType(receiverAddr)
	senderType := a.resolveChannelType(senderAddr)

	result, err := a.store.TestLinkProfile(ctx, receiverType, senderType, profileID)
	if err != nil {
		if errors.Is(err, linkprofile.ErrUnsupported) {
			return map[string]any{
				"success":        false,
				"applied_values": map[string]any{},
				"profile_id":     profileID,
				"unsupported":    true,
			}, nil
		}
		return nil, fmt.Errorf("link profiles: test: %w", err)
	}
	return map[string]any{
		"success":        true,
		"applied_values": result,
		"profile_id":     profileID,
		"interface_id":   interfaceID,
	}, nil
}

// resolveChannelType looks up the channel type for the given address.
// Returns the type string (e.g. "KEY_TRANSCEIVER") or "" when the
// address cannot be found in the model registry.
func (a *LinkProfilesAdapter) resolveChannelType(channelAddr string) string {
	if a.registry == nil || channelAddr == "" {
		return ""
	}
	devAddr := deviceAddressOf(channelAddr)
	for _, u := range a.registry.List() {
		dev, ok := u.ModelRegistry.Get(devAddr)
		if !ok {
			continue
		}
		// If the address is the device itself (no colon-suffix), return
		// the device model as a fallback type. Real profiles key on
		// channel types, not models, but this keeps the lookup consistent.
		if devAddr == channelAddr {
			return dev.Model
		}
		ch := dev.Channel(channelAddr)
		if ch == nil {
			return ""
		}
		return ch.Type
	}
	return ""
}
