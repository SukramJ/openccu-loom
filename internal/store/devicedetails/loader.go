// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package devicedetails

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// MaxCacheAgeSec is MAX_CACHE_AGE from py (10 s).
// The load gate uses MAX_CACHE_AGE/3 ≈ 3 s, matching details.py:126.
const maxCacheAgeSec = 10

// loadWindow is the cache-age gate: skip reload unless the cache is
// older than this. Mirrors `int(MAX_CACHE_AGE / 3)` from details.py.
const loadWindow = time.Duration(maxCacheAgeSec/3) * time.Second

// jsonClientLike is the narrow slice of the JSON-RPC Client that the
// Loader actually calls. Defined here (consumer package) so tests can
// substitute a fake without pulling in the real transport.
//
// Mirrors the three CCU round-trips performed
// load() path:
//
//   - GetDeviceDetails  → Device.listAllDetail
//   - GetAllRoomsRaw    → Room.getAll
//   - GetAllFunctionsRaw → Subsection.getAll
type jsonClientLike interface {
	// GetDeviceDetails returns Device.listAllDetail as a slice of
	// raw maps (DeviceDetail = map[string]any). Each map contains at
	// least "address", "name", "id", "interface", and "channels".
	GetDeviceDetails(ctx context.Context) ([]map[string]any, error)
	// GetAllRoomsRaw returns the raw Room.getAll slice: each element
	// carries "id", "name", and "channelIds" (ISE-IDs as strings).
	GetAllRoomsRaw(ctx context.Context) ([]rawEntry, error)
	// GetAllFunctionsRaw returns the raw Subsection.getAll slice in
	// the same shape as GetAllRoomsRaw.
	GetAllFunctionsRaw(ctx context.Context) ([]rawEntry, error)
}

// rawEntry is the shared wire shape for Room.getAll / Subsection.getAll.
// Mirrors hub_wiring.go's roomEntry / subsectionEntry types.
type rawEntry struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	ChannelIDs []string `json:"channelIds"`
}

// deviceDetailWire holds the typed fields the Loader reads from each
// entry in the Device.listAllDetail response.
type deviceDetailWire struct {
	Address   string             `json:"address"`
	Name      string             `json:"name"`
	ID        string             `json:"id"`
	Interface string             `json:"interface"`
	Channels  []channelWireEntry `json:"channels"`
}

type channelWireEntry struct {
	Address string `json:"address"`
	Name    string `json:"name"`
	ID      string `json:"id"`
}

// Loader populates a [Cache] with device details fetched from the CCU
// Via JSON-RPC. It mirrors load
// (store/dynamic/details.py:123-141) and the CCU backend helpers
// (client/backends/ccu.py:232-256).
//
// The Loader is intentionally thin: it has no scheduler of its own
// and is called by the cache coordinator at the right time.
type Loader struct {
	cache       *Cache
	client      jsonClientLike
	centralName string
	logger      *slog.Logger
}

// NewLoader constructs a Loader wired to the given cache and JSON-RPC
// client. `central` is the Unit name (used in log messages).
func NewLoader(cache *Cache, client jsonClientLike, central string, logger *slog.Logger) *Loader {
	return &Loader{
		cache:       cache,
		client:      client,
		centralName: central,
		logger:      logger,
	}
}

// Load fetches fresh device-detail data from the CCU and populates the
// cache. It mirrors details.py:123-141.
//
// When directCall is false, the call is skipped if the cache was
// refreshed within the last [loadWindow] (= MAX_CACHE_AGE/3 ≈ 3 s).
// When directCall is true, the load always runs.
func (l *Loader) Load(ctx context.Context, directCall bool) error {
	if !directCall {
		age := time.Since(l.cache.RefreshedAt())
		if age < loadWindow {
			l.logger.Debug("devicedetails.load.skip",
				slog.String("central", l.centralName),
				slog.Duration("age", age),
				slog.Duration("window", loadWindow))
			return nil
		}
	}

	l.cache.Clear()

	// ── Step 1: names, ISE-IDs, interfaces ──────────────────────────
	l.logger.Debug("devicedetails.load.names",
		slog.String("central", l.centralName))

	rawDetails, err := l.client.GetDeviceDetails(ctx)
	if err != nil {
		return fmt.Errorf("devicedetails.load %s: GetDeviceDetails: %w", l.centralName, err)
	}

	// iseToAddress is built here so the room / function phases can
	// resolve channel ISE-IDs back to addresses. Mirrors
	// hub_wiring.go's IseAddressMap pattern.
	iseToAddress := make(map[string]string, len(rawDetails)*8)

	for _, raw := range rawDetails {
		d, err := decodeDeviceDetail(raw)
		if err != nil {
			l.logger.Warn("devicedetails.load.decode",
				slog.String("central", l.centralName),
				slog.String("err", err.Error()))
			continue
		}
		if d.Address != "" {
			if d.Name != "" {
				l.cache.AddName(d.Address, d.Name)
			}
			if d.ID != "" {
				l.cache.AddAddressISEID(d.Address, int(parseIntStr(d.ID)))
				iseToAddress[d.ID] = d.Address
			}
			iface := hmenum.Interface(d.Interface)
			if d.Interface != "" && isKnownInterface(iface) {
				l.cache.AddInterface(d.Address, iface)
			} else {
				// Fallback: omitted or unrecognised interface → BidCos-RF,
				l.cache.AddInterface(d.Address, hmenum.InterfaceBidCosRF)
			}
		}
		for _, ch := range d.Channels {
			if ch.Address == "" {
				continue
			}
			if ch.Name != "" {
				l.cache.AddName(ch.Address, ch.Name)
			}
			if ch.ID != "" {
				l.cache.AddAddressISEID(ch.Address, int(parseIntStr(ch.ID)))
				iseToAddress[ch.ID] = ch.Address
			}
		}
	}

	// ── Step 2: room assignments ─────────────────────────────────────
	l.logger.Debug("devicedetails.load.rooms",
		slog.String("central", l.centralName))

	rooms, err := l.client.GetAllRoomsRaw(ctx)
	if err != nil {
		return fmt.Errorf("devicedetails.load %s: GetAllRoomsRaw: %w", l.centralName, err)
	}

	for _, r := range rooms {
		if r.Name == "" {
			continue
		}
		for _, chISE := range r.ChannelIDs {
			if addr, ok := iseToAddress[chISE]; ok {
				l.cache.AddChannelRoom(addr, r.Name)
			}
		}
	}

	// ── Step 3: function (Gewerk) assignments ────────────────────────
	l.logger.Debug("devicedetails.load.functions",
		slog.String("central", l.centralName))

	fns, err := l.client.GetAllFunctionsRaw(ctx)
	if err != nil {
		return fmt.Errorf("devicedetails.load %s: GetAllFunctionsRaw: %w", l.centralName, err)
	}

	for _, f := range fns {
		if f.Name == "" {
			continue
		}
		for _, chISE := range f.ChannelIDs {
			if addr, ok := iseToAddress[chISE]; ok {
				l.cache.AddFunction(addr, f.Name)
			}
		}
	}

	l.cache.MarkRefreshed(time.Now())
	l.logger.Debug("devicedetails.load.done",
		slog.String("central", l.centralName))
	return nil
}

// isKnownInterface reports whether iface is one of the CCU interface tokens
// openccu-loom recognises. InterfacesSupportingRPCCallback is the union of all
// XMLRPC + BINRPC interfaces, so it covers every valid Interface constant.
func isKnownInterface(iface hmenum.Interface) bool {
	_, ok := hmenum.InterfacesSupportingRPCCallback[iface]
	return ok
}

// decodeDeviceDetail re-encodes the raw map[string]any as JSON and
// decodes into a typed struct. This approach is robust to the CCU
// adding extra fields and avoids manual type assertions for every key.
func decodeDeviceDetail(raw map[string]any) (deviceDetailWire, error) {
	b, err := json.Marshal(raw)
	if err != nil {
		return deviceDetailWire{}, fmt.Errorf("marshal: %w", err)
	}
	var d deviceDetailWire
	if err := json.Unmarshal(b, &d); err != nil {
		return deviceDetailWire{}, fmt.Errorf("unmarshal: %w", err)
	}
	return d, nil
}

// parseIntStr converts a CCU ISE-ID string (numeric) to int64.
// Returns 0 on parse failure — callers treat 0 as "no ID".
func parseIntStr(s string) int64 {
	var v int64
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0
		}
		v = v*10 + int64(c-'0')
	}
	return v
}
