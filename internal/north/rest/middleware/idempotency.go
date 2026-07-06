// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package middleware

import (
	"bytes"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/SukramJ/openccu-loom/internal/auth"
	"github.com/SukramJ/openccu-loom/internal/north/rest/problem"
)

// IdempotencyTTL is how long a cached response stays fresh. Spec §16.2
// pins this at 5 minutes.
const IdempotencyTTL = 5 * time.Minute

// Idempotency caches the last response per (method + path + resolved
// identity + Idempotency-Key) tuple. Only mutating methods are
// cached; GET falls through.
//
// The identity is folded into the cache key because the
// Idempotency-Key header value is caller-chosen and not guaranteed
// unique across users: without the identity component, two different
// users issuing the same method+path+key would replay each other's
// cached response. [Router.NewRouter] mounts this middleware after
// AuthResolve so [auth.IdentityFrom] can resolve the caller before
// the key is computed.
//
// A second request for a key whose first attempt has not yet
// finished does not run the handler again — it gets a 409 Conflict
// instead of either double-executing the mutation or blocking. This
// mirrors the "in-flight" half of the Idempotency-Key contract
// without adding cross-request blocking to the hot path.
func Idempotency() func(http.Handler) http.Handler {
	return idempotencyMiddleware(newIdempotencyCache())
}

// idempotencyMiddleware builds the middleware against an
// explicitly-supplied cache so tests can drive a controllable clock.
func idempotencyMiddleware(cache *idempotencyCache) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !isMutating(r.Method) {
				next.ServeHTTP(w, r)
				return
			}
			key := r.Header.Get("Idempotency-Key")
			if key == "" {
				next.ServeHTTP(w, r)
				return
			}
			id := cacheKey(r.Method, r.URL.Path, identitySubject(r), key)
			entry, state := cache.reserve(id)
			switch state {
			case cacheStateHit:
				w.Header().Set("Idempotent-Replay", "true")
				for k, vs := range entry.header {
					for _, v := range vs {
						w.Header().Add(k, v)
					}
				}
				w.WriteHeader(entry.status)
				_, _ = w.Write(entry.body)
				return
			case cacheStatePending:
				problem.Write(w, http.StatusConflict,
					problem.New(problem.TypeConflict, r, "Request already in flight",
						"a request with this Idempotency-Key is already being processed; retry once it completes"))
				return
			case cacheStateMiss:
				// First request for this key — fall through, execute the
				// handler and record the response below.
			}
			rec := &recorder{ResponseWriter: w, header: http.Header{}, status: 200}
			done := false
			defer func() {
				// Release the reserved slot if the handler panicked or
				// otherwise never reached the completion write below —
				// otherwise every retry of this key would 409 forever.
				if !done {
					cache.release(id)
				}
			}()
			next.ServeHTTP(rec, r)
			cache.complete(id, rec.snapshot())
			done = true
		})
	}
}

// identitySubject resolves the caller identity attached to r's
// context by the auth-resolve middleware, or "" when the request is
// unauthenticated (or Idempotency is used without auth wired, e.g.
// in tests).
func identitySubject(r *http.Request) string {
	if id, ok := auth.IdentityFrom(r.Context()); ok {
		return id.Subject
	}
	return ""
}

func isMutating(method string) bool {
	switch strings.ToUpper(method) {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	}
	return false
}

func cacheKey(method, path, subject, key string) string {
	return method + " " + path + " " + subject + " " + key
}

type idempotentEntry struct {
	status  int
	header  http.Header
	body    []byte
	at      time.Time
	pending bool
}

// cacheState is the result of [idempotencyCache.reserve]: whether the
// caller should replay a completed entry, reject a still-in-flight
// duplicate, or proceed to run the handler.
type cacheState int

const (
	cacheStateMiss cacheState = iota
	cacheStateHit
	cacheStatePending
)

type idempotencyCache struct {
	mu    sync.Mutex
	items map[string]idempotentEntry
	now   func() time.Time
}

func newIdempotencyCache() *idempotencyCache {
	return &idempotencyCache{items: make(map[string]idempotentEntry), now: time.Now}
}

// reserve atomically inspects the slot for id: a completed, still-fresh
// entry is returned as a hit; an in-flight (pending) entry is reported
// so the caller can reject the duplicate; anything else (absent or
// expired) is claimed as pending on behalf of the caller and reported
// as a miss, so exactly one request proceeds to execute the handler.
func (c *idempotencyCache) reserve(id string) (idempotentEntry, cacheState) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.items[id]; ok {
		if e.pending {
			return idempotentEntry{}, cacheStatePending
		}
		if c.now().Sub(e.at) <= IdempotencyTTL {
			return e, cacheStateHit
		}
		delete(c.items, id)
	}
	c.items[id] = idempotentEntry{pending: true, at: c.now()}
	return idempotentEntry{}, cacheStateMiss
}

// complete stores the finished response and clears the pending flag.
func (c *idempotencyCache) complete(id string, e idempotentEntry) {
	e.at = c.now()
	c.mu.Lock()
	c.items[id] = e
	c.mu.Unlock()
}

// release drops a pending reservation without recording a result —
// used when the handler panics or otherwise never completes, so a
// later retry of the same key is not permanently blocked.
func (c *idempotencyCache) release(id string) {
	c.mu.Lock()
	if e, ok := c.items[id]; ok && e.pending {
		delete(c.items, id)
	}
	c.mu.Unlock()
}

type recorder struct {
	http.ResponseWriter
	header http.Header
	status int
	body   bytes.Buffer
}

func (r *recorder) Header() http.Header {
	// Merge into the recorder-level header as well so snapshot()
	// captures the final state.
	h := r.ResponseWriter.Header()
	r.header = h
	return h
}

func (r *recorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func (r *recorder) Write(p []byte) (int, error) {
	r.body.Write(p)
	return r.ResponseWriter.Write(p)
}

func (r *recorder) snapshot() idempotentEntry {
	hdr := make(http.Header, len(r.header))
	for k, vs := range r.header {
		hdr[k] = append([]string(nil), vs...)
	}
	return idempotentEntry{status: r.status, header: hdr, body: append([]byte(nil), r.body.Bytes()...)}
}
