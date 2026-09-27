// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package litefake

import (
	"net/http"
	"strconv"
	"time"
)

// Defaults of the metadata change stream.
const (
	DefaultMetaHeartbeatInterval = 30 * time.Second
	DefaultMetaQueue             = 64
	DefaultMetaLog               = 1000
)

// attachMeta registers a change-stream subscriber and computes what it
// is owed first: the replay after since, or a resync. hasSince is false
// when the client sent no since, which starts it at the current
// revision without either.
func (s *metaStore) attachMeta(sinceText string, hasSince bool) (sub *metaSub, replay []MetaEvent, resync bool, revision int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sub = &metaSub{queue: make(chan MetaEvent, s.queue), kill: make(chan struct{})}
	s.subs[sub] = struct{}{}
	revision = s.revision
	if !hasSince {
		return sub, nil, false, revision
	}
	since, err := strconv.Atoi(sinceText)
	switch {
	case err != nil, since < 0, since > s.revision, since < s.droppedThrough:
		// Not an integer, out of range, or older than the retained log
		// (which is memory only, so a restart makes every since below
		// the current revision too old).
		return sub, nil, true, revision
	}
	for i := range s.log {
		if s.log[i].Revision > since {
			replay = append(replay, s.log[i])
		}
	}
	return sub, replay, false, revision
}

func (s *metaStore) detachMeta(sub *metaSub) {
	s.mu.Lock()
	delete(s.subs, sub)
	s.mu.Unlock()
	sub.stop()
}

// metaResync is the resync frame of the change stream.
type metaResync struct {
	Kind     string `json:"kind"`
	Revision int    `json:"revision"`
}

// handleMetaStream serves GET /api/meta/v1/events/sse: ": connected",
// then frames that carry only "data:" (no id, no event name), replay
// after ?since= or a resync, then live events with a ": ping"
// heartbeat. It is not counted against the lite-rpc stream limits.
func (f *Fake) handleMetaStream(w http.ResponseWriter, r *http.Request) {
	if _, _, ok := f.authorize(w, r, "meta:read", false); !ok {
		return
	}
	q := r.URL.Query()
	sub, replay, resync, revision := f.meta.attachMeta(q.Get("since"), q.Has("since"))
	defer f.meta.detachMeta(sub)

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	sw := &sseWriter{w: w, rc: http.NewResponseController(w)}
	data := func(b []byte) error { return sw.raw("data: " + string(b) + "\n\n") }

	if err := sw.raw(": connected\n\n"); err != nil {
		return
	}
	if resync {
		if err := data(mustJSON(metaResync{Kind: "resync", Revision: revision})); err != nil {
			return
		}
	}
	for i := range replay {
		if err := data(mustJSON(replay[i])); err != nil {
			return
		}
	}

	f.mu.Lock()
	interval := f.metaHeartbeat
	f.mu.Unlock()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	dropRev := -1
	for {
		select {
		case <-r.Context().Done():
			return
		case <-f.done:
			return
		case <-sub.kill:
			return
		case ev := <-sub.queue:
			if f.deviates(DeviateMetaDropEvent) && (dropRev < 0 || dropRev == ev.Revision) {
				// Lose every event of the first live revision, as a
				// subscriber whose queue overflowed does.
				dropRev = ev.Revision
				continue
			}
			if err := data(mustJSON(ev)); err != nil {
				return
			}
		case <-ticker.C:
			if err := sw.raw(": ping\n\n"); err != nil {
				return
			}
		}
	}
}
