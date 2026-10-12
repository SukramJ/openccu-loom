// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package main

import (
	"context"
	"log/slog"

	fabricendpoint "github.com/SukramJ/go-fabric/endpoint"
	matterstore "github.com/SukramJ/go-fabric/store"
)

// matterScenesSettingPrefix keys one endpoint's ScenesManagement scene
// table in the Matter store's settings table — the same key shape
// go-fabric's node facade uses (node/persist.go scenesKeyRoot), so a
// database either host wrote reads back under the other.
const matterScenesSettingPrefix = "scenes."

// matterScenesStore persists the scene tables of the bridged endpoints
// that serve ScenesManagement, as matter.js keeps them in the endpoint's
// nonvolatile state. It implements [fabricendpoint.ScenesStore] over the
// Matter store's settings; a key that is not a [fabricendpoint.StringKey]
// cannot be named in a setting and is kept for the process only.
type matterScenesStore struct {
	store  *matterstore.Store
	logger *slog.Logger
}

// LoadScenes implements [fabricendpoint.ScenesStore].
func (p matterScenesStore) LoadScenes(key fabricendpoint.SourceKey) []byte {
	k, ok := key.(fabricendpoint.StringKey)
	if !ok || p.store == nil {
		return nil
	}
	v, found, err := p.store.GetSetting(context.Background(), matterScenesSettingPrefix+string(k))
	if err != nil || !found {
		return nil
	}
	return []byte(v)
}

// SaveScenes implements [fabricendpoint.ScenesStore].
func (p matterScenesStore) SaveScenes(key fabricendpoint.SourceKey, table []byte) {
	k, ok := key.(fabricendpoint.StringKey)
	if !ok || p.store == nil {
		return
	}
	if err := p.store.SetSetting(context.Background(), matterScenesSettingPrefix+string(k), string(table)); err != nil && p.logger != nil {
		p.logger.Warn("matter.scenes.persist", slog.String("err", err.Error()))
	}
}
