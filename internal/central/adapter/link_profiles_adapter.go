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
// loom:reachable:reason="the type of LinkProfilesAdapter.paramsets and of NewLinkProfilesAdapter's third parameter, wired in production at cmd/openccu-loom/ws_adapters.go to the paramsets domain; an interface production holds as a struct field rather than calling through a named variable, which the analyzer's type heuristic cannot see used"
type LinkParamsetReader interface {
	GetLinkParamset(ctx context.Context, channelAddress, peerAddress string) (map[string]any, error)
}

// LinkParamsetWriter writes the LINK paramset of one (channel, peer) pair —
// the single write path ADR 0069 names: it applies the visibility gate,
// coerces against the descriptor, and records the changed values in the
// audit entry. [ParamsetsDomain] satisfies it. Declared separately from
// [LinkParamsetReader] because most callers of this adapter need only the
// read side; [LinkProfilesAdapter.ApplyLinkProfile] type-asserts the shared
// paramsets collaborator to this interface at call time.
// loom:reachable:reason="the target of the type assertion in ApplyLinkProfile that narrows the reader port to its write half; an interface reached only through an assertion, which the analyzer's type heuristic cannot see used"
type LinkParamsetWriter interface {
	PutLinkParamset(ctx context.Context, channelAddress, peerAddress string, values map[string]any) error
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

// ApplyLinkProfile implements ws.LinkProfilesProvider.
//
// Resolves receiver and sender channel addresses to their channel types,
// looks up the named profile, and writes its value set — see
// [linkprofile.Profile.ApplyValues] for exactly what that set contains —
// through [ParamsetsDomain.PutLinkParamset], the single LINK write path
// (ADR 0069): the receiver channel address is the paramset's channel, the
// sender channel address is its peer. Returns the number of parameters
// written.
func (a *LinkProfilesAdapter) ApplyLinkProfile(
	ctx context.Context,
	receiverChannelAddr, senderChannelAddr string,
	profileID int,
) (int, error) {
	if a.store == nil {
		return 0, errors.New("link profiles: store not wired")
	}
	writer, ok := a.paramsets.(LinkParamsetWriter)
	if !ok || writer == nil {
		return 0, errors.New("link profiles: paramset writer not wired")
	}
	receiverType := a.resolveChannelType(receiverChannelAddr)
	senderType := a.resolveChannelType(senderChannelAddr)

	profile, found := a.store.GetProfileByID(receiverType, senderType, profileID)
	if !found {
		return 0, fmt.Errorf("linkprofile: profile id=%d not found for %s/%s: %w",
			profileID, receiverType, senderType, linkprofile.ErrUnsupported)
	}
	values := profile.ApplyValues()
	if err := writer.PutLinkParamset(ctx, receiverChannelAddr, senderChannelAddr, values); err != nil {
		return 0, fmt.Errorf("link profiles: apply: %w", err)
	}
	return len(values), nil
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
