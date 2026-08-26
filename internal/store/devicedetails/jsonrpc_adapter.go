// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package devicedetails

import (
	"context"
	"log/slog"

	"github.com/SukramJ/openccu-loom/internal/client/transport/jsonrpc"
)

// jsonClientAdapter bridges the production [*jsonrpc.Client] to the
// internal [jsonClientLike] interface the [Loader] consumes. The
// JSON-RPC client returns its own typed envelopes
// ([jsonrpc.RoomEntry] / [jsonrpc.SubsectionEntry]); this adapter
// converts them to the locally-owned [rawEntry] shape so the loader
// stays decoupled from the transport package's exported names.
type jsonClientAdapter struct {
	jc *jsonrpc.Client
}

// GetDeviceDetails delegates to the wire client unchanged.
func (a *jsonClientAdapter) GetDeviceDetails(ctx context.Context) ([]map[string]any, error) {
	return a.jc.GetDeviceDetails(ctx)
}

// GetAllRoomsRaw fetches every room and converts the typed
// [jsonrpc.RoomEntry] slice into the loader's [rawEntry] shape.
func (a *jsonClientAdapter) GetAllRoomsRaw(ctx context.Context) ([]rawEntry, error) {
	rooms, err := a.jc.GetAllRoomsRaw(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]rawEntry, len(rooms))
	for i, r := range rooms {
		out[i] = rawEntry{ID: r.ID, Name: r.Name, ChannelIDs: r.ChannelIDs}
	}
	return out, nil
}

// GetAllFunctionsRaw is the function-side counterpart; same shape
// conversion as [GetAllRoomsRaw].
func (a *jsonClientAdapter) GetAllFunctionsRaw(ctx context.Context) ([]rawEntry, error) {
	fns, err := a.jc.GetAllFunctionsRaw(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]rawEntry, len(fns))
	for i, f := range fns {
		out[i] = rawEntry{ID: f.ID, Name: f.Name, ChannelIDs: f.ChannelIDs}
	}
	return out, nil
}

// NewLoaderForJSONRPC is the production-wiring constructor: pass the
// daemon's [*jsonrpc.Client] and get back a fully-wired [Loader]. The
// daemon is the single caller; tests use [NewLoader] directly with a
// fake [jsonClientLike].
func NewLoaderForJSONRPC(cache *Cache, jc *jsonrpc.Client, centralName string, logger *slog.Logger) *Loader {
	return NewLoader(cache, &jsonClientAdapter{jc: jc}, centralName, logger)
}
