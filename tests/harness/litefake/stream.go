// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package litefake

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/SukramJ/openccu-loom/internal/client/transport/xmlrpc"
)

// streamWriteDeadline bounds every write to an event stream.
const streamWriteDeadline = 30 * time.Second

// filter is the parsed query filter of an event stream: AND across
// kinds, OR within a kind. An empty kind matches everything.
type filter struct {
	ifaces    map[string]struct{}
	addresses []string
	keys      map[string]struct{}
	types     map[string]struct{}
}

// parseFilter reads interface=, address=, key= (alias datapoint=) and
// type=; each is repeatable and comma-separable.
func parseFilter(q url.Values) filter {
	set := func(names ...string) map[string]struct{} {
		out := map[string]struct{}{}
		for _, n := range names {
			for _, raw := range q[n] {
				for _, v := range strings.Split(raw, ",") {
					if v = strings.TrimSpace(v); v != "" {
						out[v] = struct{}{}
					}
				}
			}
		}
		return out
	}
	f := filter{
		ifaces: set("interface"),
		keys:   set("key", "datapoint"),
		types:  set("type"),
	}
	for a := range set("address") {
		f.addresses = append(f.addresses, a)
	}
	return f
}

// matchAddress matches X and every channel X:*.
func (f filter) matchAddress(address string) bool {
	if len(f.addresses) == 0 {
		return true
	}
	for _, a := range f.addresses {
		if address == a || strings.HasPrefix(address, a+":") {
			return true
		}
	}
	return false
}

func inSet(set map[string]struct{}, v string) bool {
	if len(set) == 0 {
		return true
	}
	_, ok := set[v]
	return ok
}

// match decides whether a stream message passes. hello and resync pass
// every filter; address and key apply to event and state messages only.
func (f filter) match(m message) bool {
	if m.typ == typeHello || m.typ == typeResync {
		return true
	}
	if !inSet(f.types, m.typ) || !inSet(f.ifaces, m.iface) {
		return false
	}
	if m.typ == typeEvent || m.typ == typeState {
		return f.matchAddress(m.address) && inSet(f.keys, m.key)
	}
	return true
}

// matchState applies the interface, address and key filters to a
// /api/rpc/v1/state entry.
func (f filter) matchState(iface, address, datapoint string) bool {
	return inSet(f.ifaces, iface) && f.matchAddress(address) && inSet(f.keys, datapoint)
}

// helloInterface is one interface row of the hello message.
type helloInterface struct {
	Name       string `json:"name"`
	URL        string `json:"url"`
	State      string `json:"state"`
	Registered bool   `json:"registered"`
	Events     uint64 `json:"events"`
	Calls      uint64 `json:"calls"`
}

type helloBuffer struct {
	Seconds int `json:"seconds"`
	Events  int `json:"events"`
}

// helloData is the data of the hello message. seq is the newest
// position at connect time, the one its id carries.
type helloData struct {
	BootID     string           `json:"boot_id"`
	Seq        uint64           `json:"seq"`
	Interfaces []helloInterface `json:"interfaces"`
	Buffer     helloBuffer      `json:"buffer"`
}

type resyncData struct {
	Reason string `json:"reason"`
}

// devicesSnapshot is one ?devices=1 frame: the interface's listDevices
// answer, or the error that prevented it.
type devicesSnapshot struct {
	Interface string          `json:"interface"`
	Devices   json.RawMessage `json:"devices,omitempty"`
	Error     string          `json:"error,omitempty"`
}

// sseWriter writes event-stream frames with a deadline per write.
type sseWriter struct {
	w  io.Writer
	rc *http.ResponseController
}

func (s *sseWriter) raw(text string) error {
	_ = s.rc.SetWriteDeadline(time.Now().Add(streamWriteDeadline))
	if _, err := io.WriteString(s.w, text); err != nil {
		return err
	}
	return s.rc.Flush()
}

// frame writes one message: id (omitted when empty), event, data.
func (s *sseWriter) frame(id, typ string, data []byte) error {
	var b strings.Builder
	if id != "" {
		b.WriteString("id: " + id + "\n")
	}
	b.WriteString("event: " + typ + "\n")
	b.WriteString("data: ")
	b.Write(data)
	b.WriteString("\n\n")
	return s.raw(b.String())
}

// handleEvents serves GET /api/rpc/v1/events: the stream limits, then
// ": connected", hello, the replay or a resync, the optional device
// snapshots, and live messages with a heartbeat until the client goes,
// the stream is dropped, it overflows, or its token is revoked.
func (f *Fake) handleEvents(w http.ResponseWriter, r *http.Request) {
	entry, secret, ok := f.authorize(w, r, scopeRPCRead, true)
	if !ok {
		return
	}
	lastID := r.Header.Get("Last-Event-ID")
	hasResume := lastID != ""
	if !hasResume && r.URL.Query().Has("last_event_id") {
		lastID, hasResume = r.URL.Query().Get("last_event_id"), true
	}
	rd, res, err := f.ring.attach("token:"+entry.name, lastID, hasResume)
	if err != nil {
		var lim *limitError
		if errors.As(err, &lim) {
			writeError(w, http.StatusTooManyRequests, "too-many-streams", lim.text)
			return
		}
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	defer f.ring.detach(rd)

	flt := parseFilter(r.URL.Query())
	h := w.Header()
	h.Set("Content-Type", "text/event-stream; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	sw := &sseWriter{w: w, rc: http.NewResponseController(w)}

	if err := sw.raw(": connected\n\n"); err != nil {
		return
	}
	idOf := func(seq uint64) string { return res.bootID + "-" + strconv.FormatUint(seq, 10) }
	if err := sw.frame(idOf(res.seq), typeHello, mustJSON(f.hello(res))); err != nil {
		return
	}
	if res.resync != "" {
		if err := sw.frame("", typeResync, mustJSON(resyncData{Reason: res.resync})); err != nil {
			return
		}
	}
	for _, m := range res.replay {
		if !flt.match(m) {
			continue
		}
		if err := sw.frame(idOf(m.seq), m.typ, m.data); err != nil {
			return
		}
	}
	if r.URL.Query().Get("devices") == "1" {
		if err := f.writeDeviceSnapshots(r.Context(), sw, flt); err != nil {
			return
		}
	}

	f.mu.Lock()
	interval := f.heartbeat
	f.mu.Unlock()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-f.done:
			return
		case <-rd.kill:
			return
		case m := <-rd.queue:
			if !flt.match(m) {
				continue
			}
			if err := sw.frame(idOf(m.seq), m.typ, m.data); err != nil {
				return
			}
		case <-ticker.C:
			if rd.dropped.Load() > 0 {
				_ = sw.frame("", typeResync, mustJSON(resyncData{Reason: resyncOverflow}))
				return
			}
			if e, ok := f.lookupToken(secret); !ok || !hasScope(e.scopes, scopeRPCRead) {
				// Every heartbeat re-validates the credential; a revoked
				// token ends the stream without a closing frame.
				return
			}
			if err := sw.raw(": ping\n\n"); err != nil {
				return
			}
		}
	}
}

// hello renders the hello data for an attach result.
func (f *Fake) hello(res attachResult) helloData {
	names := f.interfaceNames()
	rows := make([]helloInterface, 0, len(names))
	for _, n := range names {
		st, _ := f.ifaceSnapshot(n)
		state := "up"
		if st.down {
			state = "down"
		}
		rows = append(rows, helloInterface{
			Name:       n,
			URL:        st.daemonURL,
			State:      state,
			Registered: st.registered,
			Events:     st.events,
			Calls:      st.calls,
		})
	}
	return helloData{
		BootID:     res.bootID,
		Seq:        res.seq,
		Interfaces: rows,
		Buffer:     helloBuffer{Seconds: int(f.opts.RingAge / time.Second), Events: f.opts.RingEvents},
	}
}

// writeDeviceSnapshots writes one id-less newDevices frame per
// interface carrying the full listDevices answer (JSON-path typing), or
// the error that prevented reading it.
func (f *Fake) writeDeviceSnapshots(ctx context.Context, sw *sseWriter, flt filter) error {
	for _, n := range f.interfaceNames() {
		if !flt.match(message{typ: typeNewDevices, iface: n}) {
			continue
		}
		snap := devicesSnapshot{Interface: n}
		st, _ := f.ifaceSnapshot(n)
		if st.down {
			snap.Error = "the interface process does not answer: " + n + ": marked down"
		} else if devs, err := f.listDaemonDevices(ctx, st.daemonURL); err != nil {
			snap.Error = "the interface process does not answer: " + n + ": " + err.Error()
		} else {
			snap.Devices = jsonValue(devs, true)
		}
		if err := sw.frame("", typeNewDevices, mustJSON(snap)); err != nil {
			return err
		}
	}
	return nil
}

// listDaemonDevices asks an interface process for its device list.
func (f *Fake) listDaemonDevices(ctx context.Context, daemonURL string) (xmlrpc.Value, error) {
	ctx, cancel := context.WithTimeout(ctx, proxyTimeout)
	defer cancel()
	resp, err := forward(ctx, daemonURL, &xmlrpc.MethodCall{Method: "listDevices"})
	if err != nil {
		return nil, err
	}
	if resp.Fault != nil {
		return nil, resp.Fault
	}
	if len(resp.Params) == 0 {
		return xmlrpc.ArrayValue{}, nil
	}
	return resp.Params[0], nil
}
