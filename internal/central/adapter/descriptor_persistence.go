// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/registry"
	"github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
)

// DescriptorStores bundles the SQLite-backed descriptor persistence.
// The zero value disables the feature (nil stores are safe no-ops).
type DescriptorStores struct {
	Devices   *sqlite.DeviceStore
	Paramsets *sqlite.ParamsetStore
}

// enabled reports whether at least one store is present.
func (d DescriptorStores) enabled() bool {
	return d.Devices != nil || d.Paramsets != nil
}

// descriptorPersistTimeout bounds each best-effort write/delete. The
// sink runs synchronously on coordinator callback goroutines, so a
// wedged database must not stall event handling indefinitely.
const descriptorPersistTimeout = 5 * time.Second

// descriptorSink mirrors registry mutations into the SQLite stores.
// It implements [registry.DescriptionSink] and [registry.ParamsetSink].
// Persistence is best-effort: failures are logged and never propagate
// into the mutating path.
//
// The hash maps skip unchanged rewrites: the boot-time re-pull re-Puts
// every description into the registries, and without the content-hash
// check every boot would rewrite the entire table. Hydration seeds the
// maps from the persisted rows.
type descriptorSink struct {
	centralName string
	stores      DescriptorStores
	logger      *slog.Logger

	mu        sync.Mutex
	devHashes map[string]string // iface + "\x00" + address → hash
	psHashes  map[string]string // iface + "\x00" + channel + "\x00" + psKey → hash
}

func newDescriptorSink(centralName string, stores DescriptorStores, logger *slog.Logger) *descriptorSink {
	return &descriptorSink{
		centralName: centralName,
		stores:      stores,
		logger:      logger,
		devHashes:   make(map[string]string),
		psHashes:    make(map[string]string),
	}
}

func devHashKey(iface hmenum.Interface, address string) string {
	return string(iface) + "\x00" + address
}

func psHashKey(iface hmenum.Interface, channelAddress string, psKey hmenum.ParamsetKey) string {
	return string(iface) + "\x00" + channelAddress + "\x00" + string(psKey)
}

// unchanged reports whether key already carries hash, updating the map
// otherwise. An empty hash (hashing failed) never counts as unchanged.
func (s *descriptorSink) unchanged(m map[string]string, key, hash string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if hash != "" && m[key] == hash {
		return true
	}
	m[key] = hash
	return false
}

func (s *descriptorSink) forget(m map[string]string, key string) {
	s.mu.Lock()
	delete(m, key)
	s.mu.Unlock()
}

// PutDescription implements [registry.DescriptionSink].
func (s *descriptorSink) PutDescription(iface hmenum.Interface, desc hmproto.DeviceDescription) {
	if s.stores.Devices == nil {
		return
	}
	hash, err := hmproto.HashDevice(desc)
	if err != nil {
		s.logger.Warn("descriptor.persist.device_hash",
			slog.String("address", desc.Address), slog.String("err", err.Error()))
	}
	if s.unchanged(s.devHashes, devHashKey(iface, desc.Address), hash) {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), descriptorPersistTimeout)
	defer cancel()
	if err := s.stores.Devices.Upsert(ctx, sqlite.DeviceRecord{
		CentralName: s.centralName,
		InterfaceID: string(iface),
		Address:     desc.Address,
		Type:        desc.Type,
		Parent:      desc.Parent,
		Firmware:    desc.Firmware,
		Hash:        hash,
		Description: desc,
	}); err != nil {
		s.logger.Warn("descriptor.persist.device",
			slog.String("address", desc.Address), slog.String("err", err.Error()))
	}
}

// DeleteDescription implements [registry.DescriptionSink].
func (s *descriptorSink) DeleteDescription(iface hmenum.Interface, address string) {
	if s.stores.Devices == nil {
		return
	}
	s.forget(s.devHashes, devHashKey(iface, address))
	ctx, cancel := context.WithTimeout(context.Background(), descriptorPersistTimeout)
	defer cancel()
	if _, err := s.stores.Devices.Delete(ctx, s.centralName, string(iface), address); err != nil {
		s.logger.Warn("descriptor.persist.device_delete",
			slog.String("address", address), slog.String("err", err.Error()))
	}
}

// PutParamset implements [registry.ParamsetSink].
func (s *descriptorSink) PutParamset(iface hmenum.Interface, channelAddress string, psKey hmenum.ParamsetKey, ps hmproto.Paramset) {
	if s.stores.Paramsets == nil {
		return
	}
	hash, err := hmproto.HashParamset(ps)
	if err != nil {
		s.logger.Warn("descriptor.persist.paramset_hash",
			slog.String("channel", channelAddress), slog.String("err", err.Error()))
	}
	if s.unchanged(s.psHashes, psHashKey(iface, channelAddress, psKey), hash) {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), descriptorPersistTimeout)
	defer cancel()
	if err := s.stores.Paramsets.Upsert(ctx, sqlite.ParamsetRecord{
		CentralName:    s.centralName,
		InterfaceID:    string(iface),
		ChannelAddress: channelAddress,
		ParamsetKey:    psKey,
		Hash:           hash,
		Paramset:       ps,
	}); err != nil {
		s.logger.Warn("descriptor.persist.paramset",
			slog.String("channel", channelAddress), slog.String("err", err.Error()))
	}
}

// DeleteChannelParamsets implements [registry.ParamsetSink].
func (s *descriptorSink) DeleteChannelParamsets(iface hmenum.Interface, channelAddress string) {
	if s.stores.Paramsets == nil {
		return
	}
	s.mu.Lock()
	prefix := string(iface) + "\x00" + channelAddress + "\x00"
	for k := range s.psHashes {
		if strings.HasPrefix(k, prefix) {
			delete(s.psHashes, k)
		}
	}
	s.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), descriptorPersistTimeout)
	defer cancel()
	if err := s.stores.Paramsets.DeleteChannel(ctx, s.centralName, string(iface), channelAddress); err != nil {
		s.logger.Warn("descriptor.persist.paramset_delete",
			slog.String("channel", channelAddress), slog.String("err", err.Error()))
	}
}

// Compile-time assertions: the sink satisfies both registry contracts.
var (
	_ registry.DescriptionSink = (*descriptorSink)(nil)
	_ registry.ParamsetSink    = (*descriptorSink)(nil)
)

// WireDescriptorPersistence hydrates the central's device-description
// and paramset registries from the persistent store, then installs the
// mirror sinks so every later registry mutation is persisted. Order
// matters: hydration runs BEFORE the sinks attach, otherwise every
// loaded row would be written straight back.
//
// Hydration makes [coordinators.DeviceCoordinator.CheckAndCreateDevicesFromCache]
// effective on a warm boot and gives IsInMultipleChannels / UI-schema
// consumers their data before the CCU answered. Errors are logged and
// degrade to a cold registry — the live pull repopulates everything.
func WireDescriptorPersistence(ctx context.Context, unit *central.Unit, stores DescriptorStores, logger *slog.Logger) (devices, paramsets int) {
	if unit == nil || !stores.enabled() {
		return 0, 0
	}
	if logger == nil {
		logger = slog.Default()
	}
	name := unit.Name()

	sink := newDescriptorSink(name, stores, logger)

	if stores.Devices != nil && unit.DescRegistry != nil {
		ifaces, err := stores.Devices.GetInterfaceIDs(ctx, name)
		if err != nil {
			logger.Warn("descriptor.hydrate.device_interfaces", slog.String("err", err.Error()))
		}
		for _, ifaceID := range ifaces {
			recs, err := stores.Devices.ListByInterface(ctx, name, ifaceID)
			if err != nil {
				logger.Warn("descriptor.hydrate.devices",
					slog.String("interface", ifaceID), slog.String("err", err.Error()))
				continue
			}
			for i := range recs {
				rec := &recs[i]
				iface := hmenum.Interface(rec.InterfaceID)
				unit.DescRegistry.Put(iface, rec.Description)
				// Seed the change-detection hash so the boot re-pull's
				// identical Put does not rewrite the row.
				sink.devHashes[devHashKey(iface, rec.Address)] = rec.Hash
				devices++
			}
		}
	}

	if stores.Paramsets != nil && unit.ParamsetReg != nil {
		recs, err := stores.Paramsets.ListByCentral(ctx, name)
		if err != nil {
			logger.Warn("descriptor.hydrate.paramsets", slog.String("err", err.Error()))
		}
		for i := range recs {
			rec := &recs[i]
			iface := hmenum.Interface(rec.InterfaceID)
			// Put, not Add: rows were persisted post-normalisation and
			// post-patching, so re-applying patches here would double-patch.
			unit.ParamsetReg.Put(iface, rec.ChannelAddress, rec.ParamsetKey, rec.Paramset)
			sink.psHashes[psHashKey(iface, rec.ChannelAddress, rec.ParamsetKey)] = rec.Hash
			paramsets++
		}
	}

	if unit.DescRegistry != nil {
		unit.DescRegistry.SetSink(sink)
	}
	if unit.ParamsetReg != nil {
		unit.ParamsetReg.SetSink(sink)
	}
	return devices, paramsets
}
