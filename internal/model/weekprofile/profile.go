// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package weekprofile

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"

	"github.com/SukramJ/openccu-loom/internal/model/schedule"
)

// ErrNotLoaded is returned by [Profile.Current] before the first
// successful load.
var ErrNotLoaded = errors.New("weekprofile: not loaded")

// Loader is the wire-level contract that reads a schedule for a
// specific device/channel from the CCU. Implementations live in the
// domain layer.
type Loader[T any] interface {
	Load(ctx context.Context) (T, error)
}

// Saver writes a schedule back to the CCU.
type Saver[T any] interface {
	Save(ctx context.Context, schedule T) error
}

// Profile is the generic observable wrapper around a week-profile
// schedule of type T. It owns the in-memory copy plus change
// subscribers; concrete packages (DefaultProfile, ClimateProfile)
// layer domain-specific semantics on top.
type Profile[T any] struct {
	loader Loader[T]
	saver  Saver[T]

	mu        sync.RWMutex
	current   T
	loaded    bool
	callbacks []func(prev, next T)
	// publishHook is an optional hook fired after every successful Load or Save.
	// Set via [Profile.SetPublishHook]. Used by north-bound adapters (MQTT) to
	// receive profile changes via a push hook rather than OnChange callbacks.
	publishHook func()
}

// New constructs a Profile. Loader and/or saver may be nil for
// read-only or write-only surfaces.
func New[T any](loader Loader[T], saver Saver[T]) *Profile[T] {
	return &Profile[T]{loader: loader, saver: saver}
}

// Current returns the last loaded/saved schedule. ErrNotLoaded is
// returned before the first successful access.
func (p *Profile[T]) Current() (T, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if !p.loaded {
		var zero T
		return zero, ErrNotLoaded
	}
	return p.current, nil
}

// CurrentOrLoad returns the cached schedule when one was previously
// loaded (cheap, no I/O) and falls through to [Load] on the first
// access. Mirrors Python `WeekProfile.get_schedule(force_load=False)`
// — UI / REST handlers that just need the current snapshot avoid a
// CCU round-trip per request, while paths that need a fresh fetch
// keep calling [Load] (force_load=True equivalent) directly.
func (p *Profile[T]) CurrentOrLoad(ctx context.Context) (T, error) {
	p.mu.RLock()
	if p.loaded {
		v := p.current
		p.mu.RUnlock()
		return v, nil
	}
	p.mu.RUnlock()
	return p.Load(ctx)
}

// Load fetches the schedule via the loader and publishes the new
// value to subscribers.
func (p *Profile[T]) Load(ctx context.Context) (T, error) {
	if p.loader == nil {
		var zero T
		return zero, errors.New("weekprofile: no loader configured")
	}
	v, err := p.loader.Load(ctx)
	if err != nil {
		var zero T
		return zero, err
	}
	p.publish(v, true)
	return v, nil
}

// Save persists the schedule via the saver and updates the local
// cache.
func (p *Profile[T]) Save(ctx context.Context, v T) error {
	if p.saver == nil {
		return errors.New("weekprofile: no saver configured")
	}
	if err := p.saver.Save(ctx, v); err != nil {
		return err
	}
	p.publish(v, false)
	return nil
}

// OnChange subscribes a handler for every schedule change: every [Save], the
// first [Load], and afterwards only a Load whose value actually differs from
// the one held. The returned closure is idempotent.
//
// That last clause is load-bearing, not an optimisation. Load publishes
// whatever it fetched, and the north-bound snapshot pass warms every climate
// channel's profile with a background Load on boot, on every broker reconnect,
// on every device-created pass and on every rename. Notifying subscribers each
// time told them a schedule had changed when nothing had — and the WebSocket
// contract that carries it says, in as many words, that a week profile
// changed. A client that believes it re-reads the profile from the CCU once
// per climate channel per reconnect.
//
// A Save always notifies, even when it writes back a value equal to the one
// held: it is an intentional write that has just gone to the device, and the
// copy here is not authoritative enough to call it a no-op.
func (p *Profile[T]) OnChange(fn func(prev, next T)) func() {
	p.mu.Lock()
	p.callbacks = append(p.callbacks, fn)
	idx := len(p.callbacks) - 1
	p.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			p.mu.Lock()
			defer p.mu.Unlock()
			if idx < len(p.callbacks) {
				p.callbacks[idx] = nil
			}
		})
	}
}

// SetPublishHook registers a callback that is fired after every successful
// [Profile.Load] or [Profile.Save] call, after all OnChange subscribers have
// been notified. The hook is called without any lock held. Pass nil to clear
// the hook. Replaces any previously registered hook (only one is kept).
//
// Intended for north-bound adapters (e.g. MQTT) that need a push signal on
// profile changes without coupling to the generic Profile type.
func (p *Profile[T]) SetPublishHook(fn func()) {
	p.mu.Lock()
	p.publishHook = fn
	p.mu.Unlock()
}

func (p *Profile[T]) publish(v T, fromLoad bool) {
	p.mu.Lock()
	prev := p.current
	wasLoaded := p.loaded
	p.current = v
	p.loaded = true
	cbs := make([]func(prev, next T), len(p.callbacks))
	copy(cbs, p.callbacks)
	hook := p.publishHook
	p.mu.Unlock()

	// The state above is updated either way — a warm-up Load has to mark the
	// profile loaded so CurrentOrLoad stops hitting the CCU. What a re-load
	// must not do is tell anyone the schedule changed when it fetched the same
	// profile back. A Save is different and always notifies: it is an
	// intentional write, and the value it carries has just been sent to the
	// device whether or not it matches the copy held here.
	if fromLoad && wasLoaded && reflect.DeepEqual(prev, v) {
		return
	}

	for _, cb := range cbs {
		if cb != nil {
			cb(prev, v)
		}
	}
	if hook != nil {
		hook()
	}
}

// DefaultProfile is the non-climate (switch/light/cover/valve/lock)
// week profile working with a [schedule.Simple] payload.
type DefaultProfile = Profile[*schedule.Simple]

// ClimateProfile wraps a [schedule.Climate] payload.
type ClimateProfile = Profile[*schedule.Climate]

// NewDefault constructs a [DefaultProfile].
func NewDefault(loader Loader[*schedule.Simple], saver Saver[*schedule.Simple]) *DefaultProfile {
	return New[*schedule.Simple](loader, saver)
}

// NewClimate constructs a [ClimateProfile].
func NewClimate(loader Loader[*schedule.Climate], saver Saver[*schedule.Climate]) *ClimateProfile {
	return New[*schedule.Climate](loader, saver)
}

// ---------------------------------------------------------------------------
// Climate schedule copy helpers
// ---------------------------------------------------------------------------

// CopyClimateProfileKey copies the per-key profile data (one of P1..P6) from
// src into dst, storing it under dstKey. Both src and dst must have a loaded
// schedule; srcKey must exist in the source; dstKey must be a valid profile
// key ("P1".."P6"). The destination profile is saved via its Saver if one is
// configured.
//
// Semantically mirrors copy_profile_to(source_profile, target_profile, target_week_profile)
// from week_profile.py: fetch the named profile from src, write it into dst
// under the target key.
func CopyClimateProfileKey(ctx context.Context, src *ClimateProfile, srcKey string, dst *ClimateProfile, dstKey string) error {
	srcSched, err := src.Current()
	if err != nil {
		return fmt.Errorf("weekprofile: copy profile key: source not loaded: %w", err)
	}
	dstSched, err := dst.Current()
	if err != nil {
		return fmt.Errorf("weekprofile: copy profile key: destination not loaded: %w", err)
	}
	if srcSched == nil {
		return errors.New("weekprofile: copy profile key: source schedule is nil")
	}
	if dstSched == nil {
		return errors.New("weekprofile: copy profile key: destination schedule is nil")
	}
	srcProf, ok := srcSched.Profiles[srcKey]
	if !ok {
		return fmt.Errorf("weekprofile: copy profile key: source key %q not found", srcKey)
	}
	// Deep-copy the source profile so mutations to dst do not alias src.
	copied := copyClimateProfile(srcProf)
	dstSched.Profiles[dstKey] = copied
	return dst.Save(ctx, dstSched)
}

// CopyClimateSchedule copies all profile keys from src into dst, replacing
// dst's entire schedule with a deep copy of src's schedule. Both profiles
// must have a loaded schedule. The destination profile is saved via its Saver
// if one is configured.
//
// Semantically mirrors copy_schedule_to(target_week_profile) from
// week_profile.py: fetch the raw schedule from src and write the full
// paramset to the target device.
func CopyClimateSchedule(ctx context.Context, src, dst *ClimateProfile) error {
	srcSched, err := src.Current()
	if err != nil {
		return fmt.Errorf("weekprofile: copy schedule: source not loaded: %w", err)
	}
	if srcSched == nil {
		return errors.New("weekprofile: copy schedule: source schedule is nil")
	}
	// Deep-copy the entire schedule so the two profiles remain independent.
	newSched := schedule.NewClimate()
	for key, prof := range srcSched.Profiles {
		if prof != nil {
			newSched.Profiles[key] = copyClimateProfile(prof)
		}
	}
	return dst.Save(ctx, newSched)
}

// copyClimateProfile returns a deep copy of a [schedule.ClimateProfile].
func copyClimateProfile(src *schedule.ClimateProfile) *schedule.ClimateProfile {
	dst := schedule.NewClimateProfile()
	for day, weekday := range src.Days {
		periods := make([]schedule.ClimatePeriod, len(weekday.Periods))
		copy(periods, weekday.Periods)
		dst.Days[day] = schedule.ClimateWeekday{
			BaseTemperature: weekday.BaseTemperature,
			Periods:         periods,
		}
	}
	return dst
}
