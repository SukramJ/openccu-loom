// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package middleware

import (
	"bytes"
	"net/http"
	"strings"
	"sync"
	"time"
)

// IdempotencyTTL is how long a cached response stays fresh. Spec §16.2
// pins this at 5 minutes.
const IdempotencyTTL = 5 * time.Minute

// Idempotency caches the last response per (method + path +
// Idempotency-Key) tuple. Only mutating methods are cached; GET
// falls through.
func Idempotency() func(http.Handler) http.Handler {
	cache := newIdempotencyCache()
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
			id := cacheKey(r.Method, r.URL.Path, key)
			if entry, ok := cache.get(id); ok {
				w.Header().Set("Idempotent-Replay", "true")
				for k, vs := range entry.header {
					for _, v := range vs {
						w.Header().Add(k, v)
					}
				}
				w.WriteHeader(entry.status)
				_, _ = w.Write(entry.body)
				return
			}
			rec := &recorder{ResponseWriter: w, header: http.Header{}, status: 200}
			next.ServeHTTP(rec, r)
			cache.put(id, rec.snapshot())
		})
	}
}

func isMutating(method string) bool {
	switch strings.ToUpper(method) {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	}
	return false
}

func cacheKey(method, path, key string) string { return method + " " + path + " " + key }

type idempotentEntry struct {
	status int
	header http.Header
	body   []byte
	at     time.Time
}

type idempotencyCache struct {
	mu    sync.Mutex
	items map[string]idempotentEntry
	now   func() time.Time
}

func newIdempotencyCache() *idempotencyCache {
	return &idempotencyCache{items: make(map[string]idempotentEntry), now: time.Now}
}

func (c *idempotencyCache) get(id string) (idempotentEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.items[id]
	if !ok {
		return idempotentEntry{}, false
	}
	if c.now().Sub(e.at) > IdempotencyTTL {
		delete(c.items, id)
		return idempotentEntry{}, false
	}
	return e, true
}

func (c *idempotencyCache) put(id string, e idempotentEntry) {
	e.at = c.now()
	c.mu.Lock()
	c.items[id] = e
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
