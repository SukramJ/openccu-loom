// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/SukramJ/openccu-loom/internal/north/rest/problem"
)

// EditSessionTTL is how long a lock survives without a heartbeat.
const EditSessionTTL = 5 * time.Minute

// EditSessions is the in-memory lock registry. One lock key uniquely
// identifies an editor (channel:paramset[:peer]) and is held by the
// session token returned at open(). Heartbeats refresh the deadline;
// a different session asking for the same key gets a 423 Locked.
type EditSessions struct {
	mu    sync.Mutex
	locks map[string]*EditLock
}

// EditLock describes the live session record returned by
// [EditSessions.Open] and [EditSessions.Heartbeat].
type EditLock struct {
	Token   string
	Subject string
	Expires time.Time
}

// NewEditSessions constructs an empty registry.
func NewEditSessions() *EditSessions {
	return &EditSessions{locks: make(map[string]*EditLock)}
}

// EditSessionOpenRequest is the body for `POST /sessions/edit`.
type EditSessionOpenRequest struct {
	Key     string `json:"key"`
	Subject string `json:"subject,omitempty"`
}

// EditSessionResponse describes the lock state returned on open and
// heartbeat. Token is the opaque value the client must pass on
// subsequent calls.
type EditSessionResponse struct {
	Token   string    `json:"token"`
	Key     string    `json:"key"`
	Subject string    `json:"subject,omitempty"`
	Expires time.Time `json:"expires"`
}

func (s *EditSessions) prune(now time.Time) {
	for k, l := range s.locks {
		if l.Expires.Before(now) {
			delete(s.locks, k)
		}
	}
}

// Open acquires a lock for `key` on behalf of `subject`. Returns 423
// when another live session already holds the key.
func (s *EditSessions) Open(key, subject string) (*EditLock, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	s.prune(now)
	if cur, ok := s.locks[key]; ok && cur.Expires.After(now) {
		return cur, false
	}
	lock := &EditLock{
		Token:   uuid.NewString(),
		Subject: subject,
		Expires: now.Add(EditSessionTTL),
	}
	s.locks[key] = lock
	return lock, true
}

// Heartbeat refreshes the lock's deadline. Returns the updated lock
// or false when the token does not match.
func (s *EditSessions) Heartbeat(key, token string) (*EditLock, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	s.prune(now)
	cur, ok := s.locks[key]
	if !ok || cur.Token != token {
		return nil, false
	}
	cur.Expires = now.Add(EditSessionTTL)
	return cur, true
}

// Verify reports whether a live (non-expired) lock for `key` is
// currently held by exactly `token`. Unlike [EditSessions.Heartbeat]
// it does NOT refresh the deadline — enforcement callers only ask
// "does this token still hold the lock?" and must not extend it as a
// side effect. A nil registry or empty token can never hold a lock,
// so both short-circuit to false (the nil case keeps the strict
// paramset-write gate fail-closed once wired).
func (s *EditSessions) Verify(key, token string) bool {
	if s == nil || token == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	s.prune(now)
	cur, ok := s.locks[key]
	return ok && cur.Token == token && cur.Expires.After(now)
}

// Close drops the lock if `token` matches.
func (s *EditSessions) Close(key, token string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur, ok := s.locks[key]
	if !ok || cur.Token != token {
		return false
	}
	delete(s.locks, key)
	return true
}

// ForceClose drops the lock without checking the token. Intended
// for the SPA's "take over"-recovery flow: when a user wants to
// acquire a lock another session abandoned (browser tab closed,
// network drop), they call this with the key alone. The handler
// gates this behind operator role on the route.
func (s *EditSessions) ForceClose(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.locks[key]; !ok {
		return false
	}
	delete(s.locks, key)
	return true
}

// OpenEditSession serves `POST /api/v1/sessions/edit`.
func OpenEditSession(s *EditSessions) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "Session locks unavailable", ""))
			return
		}
		var req EditSessionOpenRequest
		if err := DecodeJSON(r, &req); err != nil {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeBadRequest, r, "Invalid JSON", err.Error()))
			return
		}
		if req.Key == "" {
			problem.Write(w, http.StatusUnprocessableEntity,
				problem.New(problem.TypeValidation, r, "key required", ""))
			return
		}
		lock, ok := s.Open(req.Key, req.Subject)
		if !ok {
			problem.Write(w, http.StatusLocked,
				problem.New(problem.TypeConflict, r, "Resource already locked", lock.Subject))
			return
		}
		JSON(w, http.StatusOK, EditSessionResponse{
			Token: lock.Token, Key: req.Key, Subject: lock.Subject, Expires: lock.Expires,
		})
	}
}

// HeartbeatEditSession serves `POST /api/v1/sessions/edit/heartbeat`.
func HeartbeatEditSession(s *EditSessions) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "Session locks unavailable", ""))
			return
		}
		var req EditSessionResponse
		if err := DecodeJSON(r, &req); err != nil {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeBadRequest, r, "Invalid JSON", err.Error()))
			return
		}
		lock, ok := s.Heartbeat(req.Key, req.Token)
		if !ok {
			problem.Write(w, http.StatusGone,
				problem.New(problem.TypeNotFound, r, "Session expired", req.Key))
			return
		}
		JSON(w, http.StatusOK, EditSessionResponse{
			Token: lock.Token, Key: req.Key, Subject: lock.Subject, Expires: lock.Expires,
		})
	}
}

// ForceCloseEditSession serves `POST /api/v1/sessions/edit/take-over`.
// Drops the lock at `key` regardless of who owns it — intended for
// the SPA's recovery flow when a foreign session is stale.
func ForceCloseEditSession(s *EditSessions) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "Session locks unavailable", ""))
			return
		}
		var req struct {
			Key string `json:"key"`
		}
		if err := DecodeJSON(r, &req); err != nil {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeBadRequest, r, "Invalid JSON", err.Error()))
			return
		}
		s.ForceClose(req.Key)
		w.WriteHeader(http.StatusNoContent)
	}
}

// CloseEditSession serves `DELETE /api/v1/sessions/edit`.
func CloseEditSession(s *EditSessions) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s == nil {
			problem.Write(w, http.StatusServiceUnavailable,
				problem.New(problem.TypeServiceUnready, r, "Session locks unavailable", ""))
			return
		}
		var req EditSessionResponse
		if err := DecodeJSON(r, &req); err != nil {
			problem.Write(w, http.StatusBadRequest,
				problem.New(problem.TypeBadRequest, r, "Invalid JSON", err.Error()))
			return
		}
		s.Close(req.Key, req.Token)
		w.WriteHeader(http.StatusNoContent)
	}
}
