// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package addonupdate

import (
	"context"
	"log/slog"
	"sync"

	"github.com/SukramJ/openccu-loom/internal/build"
	"github.com/SukramJ/openccu-loom/internal/clock"
)

// Deps bundles [NewUpdater]'s dependencies. Only Capability is
// meaningful to set explicitly in production (the probe result); every
// other field falls back to its real-world default when zero/nil,
// mirroring the rest of the package's constructor shape.
type Deps struct {
	Capability CapabilityProbe
	Checker    *Checker
	Downloader *Downloader
	Installer  *Installer
	Clock      clock.Clock
	Logger     *slog.Logger
	// CurrentVersion overrides build.Version — the running daemon's
	// version to compare releases against. Empty uses build.Version.
	CurrentVersion string
	// Context is the daemon-lifetime context [Updater.InstallAsync]'s
	// detached goroutine runs on — deliberately NOT the caller's
	// request-scoped context, since the download/verify/stage/spawn
	// sequence can run well past the triggering HTTP request. Nil uses
	// context.Background().
	Context context.Context
}

// Updater composes the capability probe, release checker, downloader
// and installer behind a mutex-guarded state machine (idle / checking
// / downloading / installing / failed). It is the single domain-facing
// object the REST, WebSocket and MQTT surfaces observe via [OnChange].
type Updater struct {
	checker        *Checker
	downloader     *Downloader
	installer      *Installer
	clk            clock.Clock
	logger         *slog.Logger
	currentVersion string
	lifecycleCtx   context.Context

	mu             sync.Mutex
	status         Status
	lastRelease    ReleaseInfo
	listeners      map[int]func(Status)
	nextListenerID int
}

// NewUpdater constructs an Updater. The capability probe is evaluated
// once, at construction time, and its result becomes the Supported
// field of every Status snapshot for this Updater's lifetime — a
// capability change (e.g. the installer binary disappearing at
// runtime) is picked up on the next daemon restart, not live.
func NewUpdater(deps Deps) *Updater {
	checker := deps.Checker
	if checker == nil {
		checker = NewChecker()
	}
	downloader := deps.Downloader
	if downloader == nil {
		downloader = NewDownloader()
	}
	installer := deps.Installer
	if installer == nil {
		installer = NewInstaller()
	}
	clk := deps.Clock
	if clk == nil {
		clk = clock.New()
	}
	logger := deps.Logger
	if logger == nil {
		logger = slog.Default()
	}
	currentVersion := deps.CurrentVersion
	if currentVersion == "" {
		currentVersion = build.Version
	}
	lifecycleCtx := deps.Context
	if lifecycleCtx == nil {
		lifecycleCtx = context.Background()
	}
	u := &Updater{
		checker:        checker,
		downloader:     downloader,
		installer:      installer,
		clk:            clk,
		logger:         logger,
		currentVersion: currentVersion,
		lifecycleCtx:   lifecycleCtx,
	}
	u.status = Status{
		Supported:      deps.Capability.Supported(),
		CurrentVersion: currentVersion,
		State:          StateIdle,
	}
	return u
}

// Status returns the current snapshot. Safe to call concurrently with
// any in-flight [Check] or [Install].
func (u *Updater) Status() Status {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.status
}

// LastRelease returns the release info resolved by the most recent
// successful Check (zero value before the first one).
func (u *Updater) LastRelease() ReleaseInfo {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.lastRelease
}

// OnChange registers fn to run with the new Status snapshot after
// every transition. fn runs synchronously on the goroutine that
// caused the transition — it must not block or call back into the
// Updater (Check/Install/Status from within fn would deadlock).
// Returns an unsubscribe func; safe to call more than once.
func (u *Updater) OnChange(fn func(Status)) (unsubscribe func()) {
	if fn == nil {
		return func() {}
	}
	u.mu.Lock()
	id := u.nextListenerID
	u.nextListenerID++
	if u.listeners == nil {
		u.listeners = make(map[int]func(Status))
	}
	u.listeners[id] = fn
	u.mu.Unlock()
	return func() {
		u.mu.Lock()
		delete(u.listeners, id)
		u.mu.Unlock()
	}
}

// isBusyState reports whether s is a state that excludes a new
// Check/Install from starting.
func isBusyState(s State) bool {
	switch s {
	case StateChecking, StateDownloading, StateInstalling:
		return true
	default:
		return false
	}
}

// enterBusy atomically checks that no operation is running and, if
// so, switches State to next (clearing any previous Error) under a
// single critical section, then notifies listeners outside the lock.
// Returns false when another Check/Install is already in flight.
func (u *Updater) enterBusy(next State) bool {
	u.mu.Lock()
	if isBusyState(u.status.State) {
		u.mu.Unlock()
		return false
	}
	u.status.State = next
	u.status.Error = ""
	snap, fns := u.snapshotLocked()
	u.mu.Unlock()
	notify(fns, snap)
	return true
}

// transition mutates status under the lock, then notifies every
// registered listener with the resulting snapshot outside the lock so
// a listener can safely call Status()/OnChange without deadlocking.
func (u *Updater) transition(mutate func(*Status)) Status {
	u.mu.Lock()
	mutate(&u.status)
	snap, fns := u.snapshotLocked()
	u.mu.Unlock()
	notify(fns, snap)
	return snap
}

// snapshotLocked returns the current status plus a stable slice of
// listener funcs. Callers must hold u.mu.
func (u *Updater) snapshotLocked() (snapshot Status, listeners []func(Status)) {
	listeners = make([]func(Status), 0, len(u.listeners))
	for _, fn := range u.listeners {
		listeners = append(listeners, fn)
	}
	return u.status, listeners
}

func notify(fns []func(Status), snap Status) {
	for _, fn := range fns {
		fn(snap)
	}
}

// fail transitions to StateFailed with err's message.
func (u *Updater) fail(err error) {
	u.transition(func(s *Status) {
		s.State = StateFailed
		s.Error = err.Error()
	})
}

// Check runs one release-check cycle: resolves the latest GitHub
// release, compares it against the running version, and updates the
// snapshot (LatestVersion, UpdateAvailable, ReleaseURL, LastCheck).
//
// Returns [ErrUnsupported] when the platform capability check failed
// and [ErrBusy] when another Check/Install is already running.
func (u *Updater) Check(ctx context.Context) error {
	if !u.Status().Supported {
		return ErrUnsupported
	}
	if !u.enterBusy(StateChecking) {
		return ErrBusy
	}
	info, err := u.checker.LatestRelease(ctx)
	if err != nil {
		u.fail(err)
		return err
	}
	u.mu.Lock()
	u.lastRelease = info
	u.mu.Unlock()
	now := u.clk.Now()
	current := u.currentVersion
	u.transition(func(s *Status) {
		s.State = StateIdle
		s.LatestVersion = info.Version
		s.ReleaseURL = info.ReleaseURL
		s.LastCheck = now
		s.UpdateAvailable = IsNewer(current, info.Version)
		s.Error = ""
	})
	return nil
}

// Install downloads, verifies and stages the release resolved by the
// most recent successful [Check], then hands it to the firmware
// installer in a detached session (ADR 0057 decision 3). It blocks
// until the whole sequence finishes — callers that must not block a
// request for that long (the download alone can take tens of
// seconds) should use [Updater.InstallAsync] instead.
//
// Returns [ErrUnsupported] when the platform capability check failed,
// [ErrNoUpdateAvailable] when the last known check found no newer
// release, and [ErrBusy] when another Check/Install is already
// running. On success State stays StateInstalling — there is no
// further observable transition because the installer's update_script
// stops this very daemon process.
func (u *Updater) Install(ctx context.Context) error {
	if err := u.beginInstall(); err != nil {
		return err
	}
	return u.runInstall(ctx)
}

// InstallAsync runs the same pre-flight checks as [Install]
// (capability, update-available, busy) synchronously, then — if they
// pass — runs the actual download/verify/stage/spawn sequence on a
// detached goroutine bound to the Updater's own lifecycle context
// (see [Deps.Context]), not ctx. ctx is only checked for early
// cancellation before starting.
//
// This is the shape the REST `POST …/install` handler wants: the
// documented "202 Accepted" response means "the sequence started", not
// "the sequence finished" — the outcome is only observable via
// [Updater.Status] or [Updater.OnChange], exactly like [Install]'s own
// terminal StateInstalling.
func (u *Updater) InstallAsync(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := u.beginInstall(); err != nil {
		return err
	}
	//nolint:contextcheck,errcheck // deliberately NOT derived from ctx — the goroutine must outlive the triggering request; see [Deps.Context]. Outcome is observed via Status()/OnChange, not this goroutine's return.
	go u.runInstall(u.lifecycleCtx)
	return nil
}

// beginInstall runs Install/InstallAsync's shared synchronous
// pre-flight: capability, update-available, and the busy gate. On
// success the state machine has already transitioned to
// StateDownloading.
func (u *Updater) beginInstall() error {
	status := u.Status()
	if !status.Supported {
		return ErrUnsupported
	}
	if !status.UpdateAvailable {
		return ErrNoUpdateAvailable
	}
	if !u.enterBusy(StateDownloading) {
		return ErrBusy
	}
	return nil
}

// runInstall performs the actual download/verify/stage/spawn sequence.
// Callers must have already won the busy gate via [Updater.beginInstall].
func (u *Updater) runInstall(ctx context.Context) error {
	u.mu.Lock()
	info := u.lastRelease
	u.mu.Unlock()

	if err := u.downloader.DownloadAndStage(ctx, info); err != nil {
		u.fail(err)
		return err
	}
	u.transition(func(s *Status) { s.State = StateInstalling })
	if err := u.installer.Spawn(ctx); err != nil {
		u.fail(err)
		return err
	}
	return nil
}
