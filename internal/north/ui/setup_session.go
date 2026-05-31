// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package ui

import (
	"crypto/rand"
	"encoding/base64"
	"sync"
	"time"
)

// SetupSessionStore is a small in-memory store that keeps wizard state
// across the multi-step POST chain. Sessions are keyed by a random,
// unpredictable token and expire after TTL (default 30 minutes) of
// inactivity. The store sweeps stale entries lazily on every read.
type SetupSessionStore struct {
	mu    sync.Mutex
	items map[string]*SetupSession
	ttl   time.Duration
}

// NewSetupSessionStore returns a store with a 30-minute TTL.
func NewSetupSessionStore() *SetupSessionStore {
	return &SetupSessionStore{
		items: make(map[string]*SetupSession),
		ttl:   30 * time.Minute,
	}
}

// SetupSession is one wizard session. ID is the cookie value; State
// carries the accumulated form data. Expires is updated on every Save.
type SetupSession struct {
	// ID is the unpredictable token stored in the browser cookie.
	ID string
	// Created records the session birth time.
	Created time.Time
	// Expires is extended on every Save call.
	Expires time.Time
	// State holds all wizard-step data collected so far.
	State SetupState
}

// SetupState accumulates data across the four wizard steps.
type SetupState struct {
	// Step is the next step the user should see (1..4).
	Step int

	// Step 1 — admin account.
	AdminUsername string
	AdminPassword string // held only in memory; never written to disk in this struct

	// Step 2 — UI locale and theme.
	Locale string // "de" | "en"
	Theme  string // "light" | "dark" | "system"

	// Step 3 — first CCU.
	CCUName       string
	CCUHost       string
	CCUUsername   string
	CCUPassword   string
	CCUInterfaces []string // e.g. ["HmIP-RF", "BidCos-RF"]
	SkipCCU       bool

	// Step 4 — MQTT.
	MQTTEnabled   bool
	MQTTBrokerURL string
	MQTTUsername  string
	MQTTPassword  string
	SkipMQTT      bool
}

// setupCookieName is the name of the wizard session cookie.
const setupCookieName = "openccu_loom_setup"

// Issue creates a fresh session with a random ID and returns it.
func (s *SetupSessionStore) Issue() *SetupSession {
	id := randomToken()
	now := time.Now()
	sess := &SetupSession{
		ID:      id,
		Created: now,
		Expires: now.Add(s.ttl),
		State:   SetupState{Step: 1},
	}
	s.mu.Lock()
	s.items[id] = sess
	s.mu.Unlock()
	return sess
}

// Lookup returns the session for id or nil when the session is missing
// or has expired. Stale sessions are swept on every call.
func (s *SetupSessionStore) Lookup(id string) *SetupSession {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweep()
	sess, ok := s.items[id]
	if !ok {
		return nil
	}
	if time.Now().After(sess.Expires) {
		delete(s.items, id)
		return nil
	}
	return sess
}

// Save updates the state for an existing session and resets its TTL.
// If the session no longer exists it is silently ignored.
func (s *SetupSessionStore) Save(id string, state SetupState) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.items[id]
	if !ok {
		return
	}
	sess.State = state
	sess.Expires = time.Now().Add(s.ttl)
}

// Drop removes the session, ending the wizard flow.
func (s *SetupSessionStore) Drop(id string) {
	s.mu.Lock()
	delete(s.items, id)
	s.mu.Unlock()
}

// sweep removes all expired entries. Must be called with s.mu held.
func (s *SetupSessionStore) sweep() {
	now := time.Now()
	for id, sess := range s.items {
		if now.After(sess.Expires) {
			delete(s.items, id)
		}
	}
}

// randomToken returns a URL-safe base64-encoded random 256-bit value.
func randomToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic("setup_session: rand.Read: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
