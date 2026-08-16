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

const (
	// idempotencyCacheCap bounds the number of live cache slots. The key
	// is `METHOD path subject Idempotency-Key` and the header value is
	// client-chosen, so a caller minting a fresh UUID per request would
	// otherwise add one full cached response body per request for the
	// lifetime of the process. The TTL alone governs replay freshness,
	// never memory: nothing re-reads a key that is never retried.
	idempotencyCacheCap = 1024

	// idempotencyPendingCeiling is the wall-clock ceiling on an in-flight
	// reservation. Slots are normally released by complete/release, so
	// this only catches a wedged handler — without it a stuck request
	// could pin a slot forever and survive every sweep.
	idempotencyPendingCeiling = 15 * time.Minute

	// idempotencyBodyLimit caps the response body a slot may retain, so
	// one large mutation response cannot amplify the table's footprint.
	// A larger response simply is not replayable; the retry re-runs.
	idempotencyBodyLimit = 64 << 10
)

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
//
// The table is hard-bounded at [idempotencyCacheCap] slots. Its key
// carries a client-chosen header value, so nothing else limits how many
// distinct keys a caller mints; a request that finds no free slot runs
// uncached, which the published contract covers ("evicted under memory
// pressure ... flow through as fresh requests").
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
			case cacheStateBypass:
				// The table is full of still-fresh slots. Run the request
				// uncached rather than evicting somebody else's result:
				// a retry then re-executes, which the published contract
				// already allows for a key "evicted under memory
				// pressure", while replaying a wrong response never is.
				next.ServeHTTP(w, r)
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
	// cacheStateBypass means no slot could be claimed because the table
	// is at capacity and holds nothing reclaimable. The caller runs the
	// handler without recording a result.
	cacheStateBypass
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
//
// Claiming a slot is what bounds the table: at capacity the reclaimable
// entries go first, and a table of nothing but live slots answers
// [cacheStateBypass] rather than growing.
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
	now := c.now()
	if len(c.items) >= idempotencyCacheCap {
		c.sweepLocked(now)
		if len(c.items) >= idempotencyCacheCap {
			return idempotentEntry{}, cacheStateBypass
		}
	}
	c.items[id] = idempotentEntry{pending: true, at: now}
	return idempotentEntry{}, cacheStateMiss
}

// sweepLocked drops every slot that can no longer serve a replay: a
// completed entry past [IdempotencyTTL], and a reservation whose handler
// has been in flight past [idempotencyPendingCeiling]. Callers hold c.mu.
//
// It runs only when the table is at capacity, so its O(n) cost is paid
// once per flood rather than once per request.
func (c *idempotencyCache) sweepLocked(now time.Time) {
	for id, e := range c.items {
		ttl := IdempotencyTTL
		if e.pending {
			ttl = idempotencyPendingCeiling
		}
		if now.Sub(e.at) > ttl {
			delete(c.items, id)
		}
	}
}

// complete stores the finished response and clears the pending flag.
//
// A response nothing can meaningfully deduplicate does not earn a slot:
// a status the request never got past the router or the auth chain for
// carries no mutation to replay, and an oversized body would let one
// call dominate the table. Both drop the reservation instead, so a
// retry of the same key re-executes rather than answering 409 forever.
//
// A response that mints a credential is dropped for a different reason: the
// Idempotency-Key is caller-chosen and the cache key folds in a subject that
// is empty on the anonymous mutating routes — the login POST above all — so a stored
// Set-Cookie would be replayed verbatim to whoever presents the same key
// next — handing out a live session. Re-running the login is the correct
// answer to that retry.
func (c *idempotencyCache) complete(id string, e idempotentEntry) {
	if !isReplayableStatus(e.status) || len(e.body) > idempotencyBodyLimit || carriesCredential(e.header) {
		c.release(id)
		return
	}
	e.at = c.now()
	c.mu.Lock()
	c.items[id] = e
	c.mu.Unlock()
}

// isReplayableStatus reports whether a response is worth caching. The
// rejected statuses are produced before any handler mutated anything —
// an unauthenticated or misrouted request has nothing to deduplicate,
// and caching them is what lets a pre-auth flood fill the table.
func isReplayableStatus(status int) bool {
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound,
		http.StatusMethodNotAllowed, http.StatusTooManyRequests:
		return false
	}
	return true
}

// carriesCredential reports whether a response hands the caller a cookie.
// Such a response is never replayable: replaying it would give a second
// caller the first caller's credential.
func carriesCredential(h http.Header) bool {
	return len(h.Values("Set-Cookie")) > 0
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
