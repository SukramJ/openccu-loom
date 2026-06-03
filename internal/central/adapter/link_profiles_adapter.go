// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

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
// TestLinkProfile is currently a pass-through to the store's stub
// Implementation. When the
// JSON the store will be populated and the stub will be replaced by a
// real PutLinkParamset call; this adapter is designed to be
// transparent to that change.

package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/store/linkprofile"
)

// LinkProfilesAdapter implements ws.LinkProfilesProvider.
//
// Construct with [NewLinkProfilesAdapter]; the zero value is not
// useful.
type LinkProfilesAdapter struct {
	registry *central.Registry
	store    *linkprofile.Store
}

// NewLinkProfilesAdapter wires the adapter.
func NewLinkProfilesAdapter(r *central.Registry, s *linkprofile.Store) *LinkProfilesAdapter {
	return &LinkProfilesAdapter{registry: r, store: s}
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
) ([]map[string]any, error) {
	if a.store == nil {
		return nil, nil
	}
	receiverType := a.resolveChannelType(receiverChannelAddr)
	senderType := a.resolveChannelType(senderChannelAddr)

	profs, err := a.store.GetLinkProfiles(ctx, receiverType, senderType, locale)
	if err != nil {
		return nil, fmt.Errorf("link profiles: %w", err)
	}
	if len(profs) == 0 {
		return nil, nil
	}
	// Convert []linkprofile.Profile → []map[string]any via JSON so the
	// WS layer gets a uniform opaque shape.
	raw, err := json.Marshal(profs)
	if err != nil {
		return nil, fmt.Errorf("link profiles: encode: %w", err)
	}
	var out []map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("link profiles: decode: %w", err)
	}
	return out, nil
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
