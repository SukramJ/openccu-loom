// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"errors"
	"fmt"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// ErrConfigExportCentralMismatch is returned when the requested central
// does not own the channel the call names. The channel address alone is
// enough to resolve the backend, but honouring the caller's central
// scope is what keeps a multi-CCU import from writing to a same-named
// channel on the wrong CCU.
var ErrConfigExportCentralMismatch = errors.New("config_export: channel does not belong to the named central")

// ErrConfigExportParamsetKey is returned for a paramset key the export
// surface does not serve. Only the two the channel-config editor knows
// are accepted; LINK paramsets need a peer address the export format has
// no field for.
var ErrConfigExportParamsetKey = errors.New("config_export: paramset key must be MASTER or VALUES")

// ConfigExportDomain implements handlers.ConfigExportService for the
// channel configuration export / import endpoints.
//
// It deliberately routes through [ParamsetsDomain] rather than reaching
// for a backend directly: the import side is a paramset write, so it has
// to pass the same visibility gate and leave the same audit trail as the
// REST paramset PUT. A raw backend call would let an import set
// parameters that PUT would reject.
type ConfigExportDomain struct {
	registry  *central.Registry
	paramsets *ParamsetsDomain
}

// NewConfigExportDomain wires the domain to the shared registry and the
// paramset write path.
func NewConfigExportDomain(r *central.Registry, p *ParamsetsDomain) *ConfigExportDomain {
	return &ConfigExportDomain{registry: r, paramsets: p}
}

// ReadParamset fetches the named paramset of channelAddress on
// centralName, restricted to the parameters the write side would accept.
//
// The filter is what makes the snapshot importable: PutParamset rejects
// the whole write on the first hidden parameter, so an unfiltered export
// produces a file the import endpoint can only ever refuse.
func (c *ConfigExportDomain) ReadParamset(
	ctx context.Context, centralName, channelAddress, paramsetKey string,
) (map[string]any, error) {
	key, err := c.resolve(centralName, channelAddress, paramsetKey)
	if err != nil {
		return nil, err
	}
	values, err := c.paramsets.GetParamset(ctx, channelAddress, key)
	if err != nil {
		return nil, err
	}
	return c.paramsets.VisibleValues(channelAddress, key, values), nil
}

// WriteParamset applies values to the named paramset of channelAddress
// on centralName, through the same gated path the REST paramset PUT
// uses.
func (c *ConfigExportDomain) WriteParamset(
	ctx context.Context, centralName, channelAddress, paramsetKey string, values map[string]any,
) error {
	key, err := c.resolve(centralName, channelAddress, paramsetKey)
	if err != nil {
		return err
	}
	return c.paramsets.PutParamset(ctx, channelAddress, key, values)
}

// resolve validates the paramset key and the central scope. An empty
// centralName means "whichever central owns the channel", which is what
// a single-CCU export payload carries.
func (c *ConfigExportDomain) resolve(centralName, channelAddress, paramsetKey string) (hmenum.ParamsetKey, error) {
	if c == nil || c.paramsets == nil {
		return "", ErrNoParamsetBackend
	}
	var key hmenum.ParamsetKey
	switch paramsetKey {
	case string(hmenum.ParamsetKeyMaster):
		key = hmenum.ParamsetKeyMaster
	case string(hmenum.ParamsetKeyValues):
		key = hmenum.ParamsetKeyValues
	default:
		return "", fmt.Errorf("%w: %q", ErrConfigExportParamsetKey, paramsetKey)
	}
	if centralName == "" {
		return key, nil
	}
	if owner := c.centralOf(channelAddress); owner != "" && owner != centralName {
		return "", fmt.Errorf("%w: %s owns %s", ErrConfigExportCentralMismatch, owner, channelAddress)
	}
	return key, nil
}

// centralOf names the central that owns the channel's device, or "" when
// no central has it in its model.
func (c *ConfigExportDomain) centralOf(channelAddress string) string {
	if c.registry == nil {
		return ""
	}
	devAddr := deviceAddressOf(channelAddress)
	for _, u := range c.registry.List() {
		if _, ok := u.ModelRegistry.Get(devAddr); ok {
			return u.Name()
		}
	}
	return ""
}
