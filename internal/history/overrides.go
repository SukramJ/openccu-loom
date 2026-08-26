// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package history

import (
	"context"
	"sync"

	"github.com/SukramJ/openccu-loom/internal/store/sqlite"
)

// overrideKey identifies one data-point instance for a recording
// override. It mirrors the measurement primary key minus the timestamp.
type overrideKey struct {
	central   string
	iface     string
	channel   string
	parameter string
}

// RecordingOverrides is the in-memory overlay the recorder consults on
// the hot path. A present entry forces recording on/off for one data
// point, overriding the parameter-name glob policy; an absent entry
// falls back to that policy. It write-throughs mutations to the backing
// store and keeps the map in sync so the hot path never touches disk.
//
// All methods are safe on a nil receiver (the feature-off case), so the
// recorder can call [RecordingOverrides.Decide] unconditionally.
type RecordingOverrides struct {
	store   *sqlite.RecordingOverrideStore
	include []string
	exclude []string

	mu sync.RWMutex
	m  map[overrideKey]bool
}

// NewRecordingOverrides builds the overlay bound to a store and the glob
// policy (Include/Exclude) that applies when no override is present.
func NewRecordingOverrides(store *sqlite.RecordingOverrideStore, include, exclude []string) *RecordingOverrides {
	return &RecordingOverrides{
		store:   store,
		include: include,
		exclude: exclude,
		m:       make(map[overrideKey]bool),
	}
}

// Load populates the overlay from the store. Called once at wire time.
func (o *RecordingOverrides) Load(ctx context.Context) error {
	if o == nil || o.store == nil {
		return nil
	}
	rows, err := o.store.List(ctx)
	if err != nil {
		return err
	}
	m := make(map[overrideKey]bool, len(rows))
	for _, r := range rows {
		m[overrideKey{r.CentralName, r.InterfaceID, r.ChannelAddress, r.Parameter}] = r.Record
	}
	o.mu.Lock()
	o.m = m
	o.mu.Unlock()
	return nil
}

// Decide returns whether a sample should be recorded: a present override
// wins, otherwise the caller's glob-policy decision stands. Runs on the
// publisher goroutine — a single RLock + map lookup, no I/O.
func (o *RecordingOverrides) Decide(central, iface, channel, parameter string, policyAllow bool) bool {
	if o == nil {
		return policyAllow
	}
	o.mu.RLock()
	v, ok := o.m[overrideKey{central, iface, channel, parameter}]
	o.mu.RUnlock()
	if ok {
		return v
	}
	return policyAllow
}

// Effective reports the current recording decision for one data point
// plus its source: "override" when an explicit override row exists,
// "policy" when the parameter-name glob policy applies.
func (o *RecordingOverrides) Effective(central, iface, channel, parameter string) (record bool, source string) {
	if o == nil {
		return false, "policy"
	}
	o.mu.RLock()
	v, ok := o.m[overrideKey{central, iface, channel, parameter}]
	o.mu.RUnlock()
	if ok {
		return v, "override"
	}
	return allow(parameter, o.include, o.exclude), "policy"
}

// Set persists a recording override and updates the live overlay.
func (o *RecordingOverrides) Set(
	ctx context.Context, central, iface, channel, parameter string, record bool, updatedBy string,
) error {
	if o == nil || o.store == nil {
		return nil
	}
	if err := o.store.Set(ctx, central, iface, channel, parameter, record, updatedBy); err != nil {
		return err
	}
	o.mu.Lock()
	o.m[overrideKey{central, iface, channel, parameter}] = record
	o.mu.Unlock()
	return nil
}

// Clear removes a recording override (reverting the data point to the
// glob policy) from both the store and the live overlay.
func (o *RecordingOverrides) Clear(
	ctx context.Context, central, iface, channel, parameter string,
) error {
	if o == nil || o.store == nil {
		return nil
	}
	if err := o.store.Clear(ctx, central, iface, channel, parameter); err != nil {
		return err
	}
	o.mu.Lock()
	delete(o.m, overrideKey{central, iface, channel, parameter})
	o.mu.Unlock()
	return nil
}

// DeleteCentral drops every override this overlay holds for central from
// the in-memory map, without touching the backing store. The caller — the
// central-removal path — deletes the durable rows itself (via
// [sqlite.RecordingOverrideStore.DeleteForCentral]) and must call this
// right after, so the two halves stay in sync: [Load] only runs once at
// wire time, so a central removed and re-adopted under the same name would
// otherwise keep serving stale "never record" verdicts from rows the store
// no longer has, until the next daemon restart. Returns the number of keys
// removed, for logging. Safe on a nil receiver.
func (o *RecordingOverrides) DeleteCentral(central string) int {
	if o == nil {
		return 0
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	n := 0
	for k := range o.m {
		if k.central == central {
			delete(o.m, k)
			n++
		}
	}
	return n
}
