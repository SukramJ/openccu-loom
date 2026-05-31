// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package configui

import (
	"sync"

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

// SessionStore tracks the active edit sessions for a daemon.
type SessionStore struct {
	mu       sync.Mutex
	sessions map[SessionKey]*Session
}

// NewSessionStore returns an empty store.
func NewSessionStore() *SessionStore {
	return &SessionStore{sessions: make(map[SessionKey]*Session)}
}

// Get returns the session bound to key, or nil when none is open.
func (s *SessionStore) Get(key SessionKey) *Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessions[key]
}

// Put stores session under key, replacing any prior session for the
// same key.
func (s *SessionStore) Put(key SessionKey, session *Session) {
	if session == nil {
		return
	}
	s.mu.Lock()
	s.sessions[key] = session
	s.mu.Unlock()
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
