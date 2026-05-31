// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mqtt

import (
	"context"
	"errors"
	"log/slog"
	"math/rand"
	"strings"
	"sync"
	"time"
)

// Connector is the narrow lifecycle contract a broker adapter must
// satisfy. `Connect` establishes the session (including the LWT
// handshake); `Disconnect` unregisters the session gracefully.
type Connector interface {
	Connect(ctx context.Context) error
	Disconnect(ctx context.Context) error
}

// LifecycleConfig governs the reconnect loop.
type LifecycleConfig struct {
	InitialBackoff time.Duration
	MaxBackoff     time.Duration
	Jitter         time.Duration
	Logger         *slog.Logger
}

// DefaultLifecycle returns the MVP default timings: 1s → 30s
// exponential backoff with ±500ms jitter.
func DefaultLifecycle() LifecycleConfig {
	return LifecycleConfig{
		InitialBackoff: 1 * time.Second,
		MaxBackoff:     30 * time.Second,
		Jitter:         500 * time.Millisecond,
	}
}

// Lifecycle drives a [Connector] with automatic reconnect. It is
// intentionally transport-agnostic: paho, nhooyr, or any future
// adapter just implements Connector.
type Lifecycle struct {
	cfg       LifecycleConfig
	connector Connector

	mu        sync.Mutex
	started   bool
	cancel    context.CancelFunc
	onConnect []func(context.Context)
}

// NewLifecycle constructs a lifecycle around connector.
func NewLifecycle(cfg LifecycleConfig, connector Connector) *Lifecycle {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.InitialBackoff == 0 {
		cfg.InitialBackoff = DefaultLifecycle().InitialBackoff
	}
	if cfg.MaxBackoff == 0 {
		cfg.MaxBackoff = DefaultLifecycle().MaxBackoff
	}
	return &Lifecycle{cfg: cfg, connector: connector}
}

// OnConnect registers a callback fired on every successful (re)connect.
// Typical use: `bridge.AnnounceOnline` + resubscribe.
func (l *Lifecycle) OnConnect(fn func(context.Context)) {
	l.mu.Lock()
	l.onConnect = append(l.onConnect, fn)
	l.mu.Unlock()
}

// Start boots the reconnect loop and returns once the first connect
// has succeeded (or ctx was cancelled). Subsequent drops reconnect
// in the background.
func (l *Lifecycle) Start(ctx context.Context) error {
	l.mu.Lock()
	if l.started {
		l.mu.Unlock()
		return errors.New("mqtt.lifecycle: already started")
	}
	runCtx, cancel := context.WithCancel(ctx)
	l.cancel = cancel
	l.started = true
	l.mu.Unlock()

	// First connect is synchronous so the caller can decide whether
	// to proceed on a hard failure.
	if err := l.connectOnce(runCtx); err != nil {
		cancel()
		l.mu.Lock()
		l.started = false
		l.mu.Unlock()
		return err
	}
	go l.loop(runCtx)
	return nil
}

// Stop cancels the loop and disconnects the session.
func (l *Lifecycle) Stop(ctx context.Context) error {
	l.mu.Lock()
	cancel := l.cancel
	l.started = false
	l.cancel = nil
	l.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if l.connector != nil {
		return l.connector.Disconnect(ctx)
	}
	return nil
}

func (l *Lifecycle) loop(ctx context.Context) {
	backoff := l.cfg.InitialBackoff
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(l.jittered(backoff)):
		}
		if err := l.connectOnce(ctx); err != nil {
			// "already connected" is the connector's idempotency
			// signal: the underlying socket is still healthy, the
			// last Connect() succeeded, nothing to do. Silence the
			// warn-level noise and keep the backoff at MaxBackoff
			// so the next idle probe is far in the future. Only
			// real failures (dial / connack) should drive the
			// exponential growth — those carry a different message.
			if isAlreadyConnectedErr(err) {
				backoff = l.cfg.MaxBackoff
				continue
			}
			l.cfg.Logger.Warn("mqtt.reconnect", slog.String("err", err.Error()))
			backoff *= 2
			if backoff > l.cfg.MaxBackoff {
				backoff = l.cfg.MaxBackoff
			}
			continue
		}
		backoff = l.cfg.InitialBackoff
	}
}

// isAlreadyConnectedErr reports whether err is the connector's
// "still connected" signal. Matches by suffix to stay robust against
// future error-wrapping; the actual error originates in
// [TCPClient.Connect] and bubbles up through [Lifecycle.connectOnce].
func isAlreadyConnectedErr(err error) bool {
	return err != nil && strings.HasSuffix(err.Error(), "already connected")
}

func (l *Lifecycle) connectOnce(ctx context.Context) error {
	if err := l.connector.Connect(ctx); err != nil {
		return err
	}
	l.mu.Lock()
	cbs := make([]func(context.Context), len(l.onConnect))
	copy(cbs, l.onConnect)
	l.mu.Unlock()
	for _, cb := range cbs {
		cb(ctx)
	}
	return nil
}

func (l *Lifecycle) jittered(d time.Duration) time.Duration {
	if l.cfg.Jitter <= 0 {
		return d
	}
	delta := time.Duration(rand.Int63n(int64(l.cfg.Jitter*2))) - l.cfg.Jitter //nolint:gosec // jitter only
	return d + delta
}
