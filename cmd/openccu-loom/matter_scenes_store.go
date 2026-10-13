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
// Matter store's settings, keyed by the stable key's String() — the
// daemon's keys are [matterendpoint.SourceKey] values, not StringKeys,
// and a type check for StringKey kept every scene table for the process
// only.
type matterScenesStore struct {
	store  *matterstore.Store
	logger *slog.Logger
}

// LoadScenes implements [fabricendpoint.ScenesStore].
func (p matterScenesStore) LoadScenes(key fabricendpoint.SourceKey) []byte {
	if key == nil || p.store == nil {
		return nil
	}
	v, found, err := p.store.GetSetting(context.Background(), matterScenesSettingPrefix+key.String())
	if err != nil || !found {
		return nil
	}
	return []byte(v)
}

// SaveScenes implements [fabricendpoint.ScenesStore].
func (p matterScenesStore) SaveScenes(key fabricendpoint.SourceKey, table []byte) {
	if key == nil || p.store == nil {
		return
	}
	if err := p.store.SetSetting(context.Background(), matterScenesSettingPrefix+key.String(), string(table)); err != nil && p.logger != nil {
		p.logger.Warn("matter.scenes.persist", slog.String("err", err.Error()))
	}
}
