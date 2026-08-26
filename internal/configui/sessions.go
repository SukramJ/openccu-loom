// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package configui

import (
	"sync"
	"time"

	"github.com/SukramJ/openccu-loom/internal/clock"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// SessionKey uniquely identifies one in-flight edit session.
//
// The triple (channel-address, paramset-key, central-name) matches
// the reference config panel's session keying scheme. CentralName lets
// the store carry sessions for several CCUs at once.
type SessionKey struct {
	CentralName    string
	ChannelAddress string
	ParamsetKey    hmenum.ParamsetKey
}

// sessionMaxAge bounds how long an opened-but-abandoned session (no save,
// no discard) is kept. A channel-config editor left open in a browser tab
// otherwise pins its paramset snapshot in memory for the rest of the
// daemon's uptime — there is no other reclaim path, since the WebSocket
// disconnect that closed the tab carries no session-teardown hook. 30
// minutes comfortably covers a real editing session while bounding the
// leak to "abandoned in roughly the last half hour" instead of "forever".
const sessionMaxAge = 30 * time.Minute

// SessionStore tracks the active edit sessions for a daemon.
type SessionStore struct {
	mu       sync.Mutex
	sessions map[SessionKey]*Session
	// openedAt records when each session entered the map, so Put's
	// amortised sweep can reap ones nobody saved or discarded. Keyed
	// alongside sessions rather than folded into [Session] because the
	// session's own fields mirror the reference config panel's shape and
	// are exercised by paramset diffing — the store, not the session, owns
	// its own lifecycle bookkeeping.
	openedAt map[SessionKey]time.Time
	clk      clock.Clock
}

// NewSessionStore returns an empty store.
func NewSessionStore() *SessionStore {
	return &SessionStore{
		sessions: make(map[SessionKey]*Session),
		openedAt: make(map[SessionKey]time.Time),
		clk:      clock.New(),
	}
}

// Get returns the session bound to key, or nil when none is open.
func (s *SessionStore) Get(key SessionKey) *Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessions[key]
}

// Put stores session under key, replacing any prior session for the
// same key, and stamps the entry's open time.
//
// Every call also sweeps every session past [sessionMaxAge] out of the
// store. Amortising the sweep onto Put — rather than a separate periodic
// goroutine — needs no extra wiring through the composition root: sessions
// only grow through this one call, which is already on the daemon's live
// WebSocket path, so an abandoned session is bounded without a new
// scheduled job to reason about.
func (s *SessionStore) Put(key SessionKey, session *Session) {
	if session == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.clk.Now()
	s.sweepLocked(now)
	s.sessions[key] = session
	s.openedAt[key] = now
}

// Delete drops the session bound to key. Returns true when one was
// present.
func (s *SessionStore) Delete(key SessionKey) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.sessions[key]; !ok {
		return false
	}
	delete(s.sessions, key)
	delete(s.openedAt, key)
	return true
}

// Len reports the number of active sessions. For diagnostics.
func (s *SessionStore) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.sessions)
}

// Keys returns a snapshot of every active session key. The result is
// freshly allocated so callers may sort or filter freely.
func (s *SessionStore) Keys() []SessionKey {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]SessionKey, 0, len(s.sessions))
	for k := range s.sessions {
		out = append(out, k)
	}
	return out
}

// sweepLocked removes every session opened more than [sessionMaxAge]
// before now. Callers must hold s.mu.
func (s *SessionStore) sweepLocked(now time.Time) {
	for k, opened := range s.openedAt {
		if now.Sub(opened) >= sessionMaxAge {
			delete(s.sessions, k)
			delete(s.openedAt, k)
		}
	}
}
