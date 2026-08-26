// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package config

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

// DefaultWatchInterval is the polling cadence of [Watcher.Run].
// Polling beats fsnotify for our use case: stays stdlib-only (no
// dependency on github.com/fsnotify/fsnotify, which CLAUDE.md flags
// as "discuss before adding"), survives editor-rename atomicity
// quirks, and the daemon does not need millisecond-precise reload.
const DefaultWatchInterval = 5 * time.Second

// ReloadHandler is called once per detected config change. The
// handler may compare prev and next, decide which subsystems to
// re-wire, and return an error to abort the reload. On error the
// watcher logs and keeps the previous config — i.e. a malformed
// config never replaces a working one in-place.
//
// Out of scope (deliberately): atomic / staged reloads, partial
// reloads, in-flight request draining. The watcher is a thin
// notification layer; the daemon owns the rewiring policy.
type ReloadHandler func(prev, next *Config) error

// Watcher polls a config file for size+mtime changes and invokes a
// handler when the content has actually changed. The current config
// is exposed through [Watcher.Current] for consumers that want to
// re-read at their own cadence rather than subscribing to changes.
//
// Closes audit O14: the original Load path required a daemon
// restart for any config change. The watcher gives operators a
// hot-reload escape hatch for non-structural tweaks (log level,
// REST listen address, central credentials) without dropping
// CCU connections.
type Watcher struct {
	path     string
	interval time.Duration
	logger   *slog.Logger
	handler  ReloadHandler

	current  atomic.Pointer[Config]
	mu       sync.Mutex
	lastSize int64
	lastTime time.Time
}

// NewWatcher returns a [Watcher] anchored at the given path with the
// initial config already loaded. Returns the watcher + the loaded
// config so callers can use it before [Watcher.Run] starts polling.
//
// path must point at a YAML file readable by [Load]. The watcher
// applies env-overlay automatically on every reload (mirroring
// [LoadWithEnv]) so secret rotations via env variables also take
// effect.
func NewWatcher(path string, opts ...WatcherOption) (*Watcher, *Config, error) {
	if path == "" {
		return nil, nil, errors.New("config: NewWatcher: empty path")
	}
	w := &Watcher{
		path:     path,
		interval: DefaultWatchInterval,
		logger:   slog.Default(),
	}
	for _, o := range opts {
		o(w)
	}
	cfg, err := LoadWithEnv(path)
	if err != nil {
		return nil, nil, err
	}
	w.current.Store(cfg)
	w.captureFingerprint()
	return w, cfg, nil
}

// WatcherOption configures a [Watcher] at construction.
type WatcherOption func(*Watcher)

// WithInterval overrides the polling cadence. Values < 1 s are
// clamped to 1 s.
func WithInterval(d time.Duration) WatcherOption {
	return func(w *Watcher) {
		if d < time.Second {
			d = time.Second
		}
		w.interval = d
	}
}

// WithLogger overrides the watcher's slog logger. Nil restores the
// default.
func WithLogger(l *slog.Logger) WatcherOption {
	return func(w *Watcher) {
		if l == nil {
			l = slog.Default()
		}
		w.logger = l
	}
}

// WithHandler installs a [ReloadHandler] that fires on every
// successful reload. Multiple handlers are not supported in 0.1.0;
// the daemon's composition root chains its own fan-out if it needs
// more than one consumer.
func WithHandler(h ReloadHandler) WatcherOption {
	return func(w *Watcher) {
		w.handler = h
	}
}

// Current returns the most recently loaded [Config]. The pointer
// is stable for the lifetime of the daemon; callers must not mutate
// the returned value (treat it as read-only).
func (w *Watcher) Current() *Config {
	return w.current.Load()
}

// Run blocks until ctx is cancelled. Every [Watcher.interval] it
// stats the config file, re-parses on change, and notifies the
// configured [ReloadHandler]. Errors during reload are logged but
// never abort the watcher — a single broken edit must not bring
// the daemon down.
func (w *Watcher) Run(ctx context.Context) error {
	t := time.NewTicker(w.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			w.tick()
		}
	}
}

// tick performs one stat + reload-on-change cycle.
func (w *Watcher) tick() {
	st, err := os.Stat(w.path)
	if err != nil {
		w.logger.Warn("config.watch.stat",
			slog.String("path", w.path),
			slog.String("err", err.Error()))
		return
	}
	w.mu.Lock()
	changed := st.Size() != w.lastSize || !st.ModTime().Equal(w.lastTime)
	w.mu.Unlock()
	if !changed {
		return
	}
	prev := w.current.Load()
	next, err := LoadWithEnv(w.path)
	if err != nil {
		w.logger.Warn("config.watch.reload_failed",
			slog.String("path", w.path),
			slog.String("err", err.Error()),
			slog.String("hint", "previous config kept"))
		// Capture the new fingerprint anyway so we don't busy-loop
		// the warning on every tick while the file is broken.
		w.captureFingerprint()
		return
	}
	if w.handler != nil {
		if hErr := w.handler(prev, next); hErr != nil {
			w.logger.Warn("config.watch.handler_rejected",
				slog.String("err", hErr.Error()),
				slog.String("hint", "previous config kept"))
			w.captureFingerprint()
			return
		}
	}
	w.current.Store(next)
	w.captureFingerprint()
	w.logger.Info("config.watch.reloaded",
		slog.String("path", w.path))
}

// captureFingerprint records the current size+mtime so the next
// tick can detect a fresh edit. Safe to call from any goroutine.
func (w *Watcher) captureFingerprint() {
	st, err := os.Stat(w.path)
	if err != nil {
		return
	}
	w.mu.Lock()
	w.lastSize = st.Size()
	w.lastTime = st.ModTime()
	w.mu.Unlock()
}
