// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package litefake

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/SukramJ/openccu-loom/internal/client/transport/xmlrpc"
)

// Subscriber role.
//
// On a real box occulited is the one XML-RPC client of the interface
// processes: it calls init with its own callback URL and republishes
// what the daemons push onto the event stream. Clients never call init
// themselves (the proxy refuses it). The fake does the same against the
// godevccu interface listeners, serving its callback endpoint with
// Loom's own xmlrpc server mux, which already speaks system.multicall.

// callerID is the interface id the subscriber registers under.
func callerID(iface string) string { return "occulited_" + iface }

// callbackHandler serves /cb/<iface>, one mux per interface so every
// callback is attributed to the interface process that sent it.
func (f *Fake) callbackHandler() http.Handler {
	mux := http.NewServeMux()
	for _, name := range f.opts.Interfaces {
		h := xmlrpc.NewHandler()
		h.Logger = f.logger
		f.registerCallbacks(h.Mux, name)
		mux.Handle("/cb/"+name, withBatch(h))
	}
	return mux
}

// batchKey carries the per-request batch slot through the mux.
type batchKey struct{}

// batchSlot remembers the seq of the first event of one daemon request.
// A daemon bundles pending events into one system.multicall; each event
// on the stream carries the seq of the first event of its bundle.
type batchSlot struct{ first uint64 }

// withBatch gives every callback request its own batch slot. One HTTP
// request is one daemon call, a multicall included, so the slot spans
// exactly one bundle.
func withBatch(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), batchKey{}, &batchSlot{})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// registerCallbacks wires the callback methods an interface process
// invokes on its subscriber.
func (f *Fake) registerCallbacks(mux *xmlrpc.Mux, iface string) {
	mux.RegisterSystemMethods()
	mux.Handle("listDevices", func(context.Context, []xmlrpc.Value) (xmlrpc.Value, error) {
		// The subscriber keeps no device list, so the daemon pushes its
		// whole catalogue as newDevices after every init.
		return xmlrpc.ArrayValue{}, nil
	})
	mux.Handle("event", func(ctx context.Context, params []xmlrpc.Value) (xmlrpc.Value, error) {
		if len(params) < 4 {
			return nil, fmt.Errorf("event: expected 4 params, got %d", len(params))
		}
		address, err := xmlrpc.AsString(params[1])
		if err != nil {
			return nil, fmt.Errorf("event: address: %w", err)
		}
		key, err := xmlrpc.AsString(params[2])
		if err != nil {
			return nil, fmt.Errorf("event: key: %w", err)
		}
		f.publishEvent(ctx, iface, address, key, params[3])
		return xmlrpc.StringValue(""), nil
	})
	mux.Handle("newDevices", func(_ context.Context, params []xmlrpc.Value) (xmlrpc.Value, error) {
		f.publishDevices(typeNewDevices, iface, addressesFrom(params, 1))
		return xmlrpc.StringValue(""), nil
	})
	mux.Handle("deleteDevices", func(_ context.Context, params []xmlrpc.Value) (xmlrpc.Value, error) {
		f.publishDevices(typeDeleteDevices, iface, addressesFrom(params, 1))
		return xmlrpc.StringValue(""), nil
	})
	mux.Handle("readdedDevice", func(_ context.Context, params []xmlrpc.Value) (xmlrpc.Value, error) {
		f.publishDevices(typeReaddedDevice, iface, addressesFrom(params, 1))
		return xmlrpc.StringValue(""), nil
	})
	mux.Handle("updateDevice", func(_ context.Context, params []xmlrpc.Value) (xmlrpc.Value, error) {
		// The hint (params[2]) is dropped: the stream carries the one
		// address only.
		f.publishDevices(typeUpdateDevice, iface, stringsAt(params, 1, 1))
		return xmlrpc.StringValue(""), nil
	})
	mux.Handle("replaceDevice", func(_ context.Context, params []xmlrpc.Value) (xmlrpc.Value, error) {
		// Positional arguments are kept in order: [old, new].
		f.publishDevices(typeReplaceDevice, iface, stringsAt(params, 1, 2))
		return xmlrpc.StringValue(""), nil
	})
}

// addressesFrom reads the address list at params[i]: either plain
// strings or device description structs carrying ADDRESS.
func addressesFrom(params []xmlrpc.Value, i int) []string {
	if len(params) <= i {
		return []string{}
	}
	arr, err := xmlrpc.AsArray(params[i])
	if err != nil {
		return []string{}
	}
	out := make([]string, 0, len(arr))
	for _, item := range arr {
		if s, err := xmlrpc.AsString(item); err == nil {
			out = append(out, s)
			continue
		}
		if a, err := xmlrpc.StructField[xmlrpc.StringValue](item, "ADDRESS"); err == nil {
			out = append(out, string(a))
		}
	}
	return out
}

// stringsAt reads n consecutive string params starting at params[i].
func stringsAt(params []xmlrpc.Value, i, n int) []string {
	out := make([]string, 0, n)
	for j := i; j < i+n && j < len(params); j++ {
		if s, err := xmlrpc.AsString(params[j]); err == nil {
			out = append(out, s)
		}
	}
	return out
}

// eventData is the data of an event message.
type eventData struct {
	Interface string          `json:"interface"`
	Address   string          `json:"address"`
	Key       string          `json:"key"`
	Value     json.RawMessage `json:"value"`
	TS        string          `json:"ts"`
	Batch     uint64          `json:"batch"`
	Confirmed bool            `json:"confirmed,omitempty"`
}

// publishEvent turns one daemon event into a stream message and, for a
// datapoint of the kept set, into a /state entry.
func (f *Fake) publishEvent(ctx context.Context, iface, address, key string, v xmlrpc.Value) {
	value := jsonValue(v, false)
	now := time.Now().UTC()
	kept := f.values.keeps(key)
	slot, _ := ctx.Value(batchKey{}).(*batchSlot)
	f.ring.publish(typeEvent, iface, address, key, func(seq uint64) []byte {
		batch := seq
		if slot != nil {
			if slot.first == 0 {
				slot.first = seq
			}
			batch = slot.first
		}
		return mustJSON(eventData{
			Interface: iface,
			Address:   address,
			Key:       key,
			Value:     value,
			TS:        now.Format(time.RFC3339Nano),
			Batch:     batch,
			Confirmed: kept,
		})
	})
	if kept {
		f.values.put(iface, address, key, value, now)
	}
	f.mu.Lock()
	if st, ok := f.ifaces[iface]; ok {
		st.events++
	}
	f.mu.Unlock()
}

// devicesData is the data of a device-list message: addresses only.
type devicesData struct {
	Interface string   `json:"interface"`
	Addresses []string `json:"addresses"`
	TS        string   `json:"ts"`
}

// publishDevices puts a device-list message on the ring.
func (f *Fake) publishDevices(typ, iface string, addresses []string) {
	ts := time.Now().UTC().Format(time.RFC3339Nano)
	f.ring.publish(typ, iface, "", "", func(uint64) []byte {
		return mustJSON(devicesData{Interface: iface, Addresses: addresses, TS: ts})
	})
}

// interfaceData is the data of an interface message.
type interfaceData struct {
	Interface string `json:"interface"`
	State     string `json:"state"`
	TS        string `json:"ts"`
}

// publishInterface puts an interface state message on the ring.
func (f *Fake) publishInterface(iface, state string) {
	ts := time.Now().UTC().Format(time.RFC3339Nano)
	f.ring.publish(typeInterface, iface, "", "", func(uint64) []byte {
		return mustJSON(interfaceData{Interface: iface, State: state, TS: ts})
	})
}

// subscribe calls init on one interface process with the fake's
// callback URL. The daemon then asks listDevices and pushes newDevices
// for its whole catalogue asynchronously.
func (f *Fake) subscribe(ctx context.Context, iface string) error {
	st, ok := f.ifaceSnapshot(iface)
	if !ok {
		return fmt.Errorf("litefake: unknown interface %s", iface)
	}
	client, err := xmlrpc.NewClient(xmlrpc.Config{URL: st.daemonURL, Interface: iface, Logger: f.logger})
	if err != nil {
		return fmt.Errorf("litefake: init client for %s: %w", iface, err)
	}
	callback := f.cb.URL + "/cb/" + iface
	if _, err := client.Call(ctx, "init", []xmlrpc.Value{
		xmlrpc.StringValue(callback), xmlrpc.StringValue(callerID(iface)),
	}); err != nil {
		return fmt.Errorf("litefake: init %s: %w", iface, err)
	}
	f.mu.Lock()
	if s, ok := f.ifaces[iface]; ok {
		s.registered = true
	}
	f.mu.Unlock()
	return nil
}

// mustJSON marshals a fake-owned payload type. Every payload is a plain
// struct of strings, numbers and raw JSON, so marshalling cannot fail;
// a failure would be a defect in the fake and is rendered as null.
func mustJSON[T any](v T) []byte { // T: any fake payload struct; json.Marshal's own constraint.
	raw, err := json.Marshal(v)
	if err != nil {
		return []byte("null")
	}
	return raw
}

// jsonValue converts a decoded XML-RPC value to JSON. The event stream
// and the ?devices=1 snapshot type two kinds differently: on the stream
// a dateTime stays the raw ISO-8601 basic string and base64 stays the
// raw base64 text; the snapshot uses the JSON path's typing, RFC 3339
// and {"base64": …}. A double is written as Go writes a float64, so 1.0
// becomes 1.
func jsonValue(v xmlrpc.Value, devicesTyping bool) json.RawMessage {
	var buf bytes.Buffer
	appendJSON(&buf, v, devicesTyping)
	return buf.Bytes()
}

func appendJSON(buf *bytes.Buffer, v xmlrpc.Value, devicesTyping bool) {
	switch t := v.(type) {
	case xmlrpc.IntValue:
		buf.WriteString(strconv.FormatInt(int64(t), 10))
	case xmlrpc.BoolValue:
		buf.WriteString(strconv.FormatBool(bool(t)))
	case xmlrpc.DoubleValue:
		d := float64(t)
		if math.IsNaN(d) || math.IsInf(d, 0) {
			buf.WriteString("null")
			return
		}
		buf.Write(mustJSON(d))
	case xmlrpc.StringValue:
		buf.Write(mustJSON(string(t)))
	case xmlrpc.DateTimeValue:
		if devicesTyping {
			buf.Write(mustJSON(t.Time().Format(time.RFC3339)))
			return
		}
		buf.Write(mustJSON(t.Time().Format("20060102T15:04:05")))
	case xmlrpc.Base64Value:
		enc := base64.StdEncoding.EncodeToString(t)
		if devicesTyping {
			buf.WriteString(`{"base64":`)
			buf.Write(mustJSON(enc))
			buf.WriteByte('}')
			return
		}
		buf.Write(mustJSON(enc))
	case xmlrpc.StructValue:
		buf.WriteByte('{')
		for i, m := range t.Members {
			if i > 0 {
				buf.WriteByte(',')
			}
			buf.Write(mustJSON(m.Name))
			buf.WriteByte(':')
			appendJSON(buf, m.Value, devicesTyping)
		}
		buf.WriteByte('}')
	case xmlrpc.ArrayValue:
		buf.WriteByte('[')
		for i, item := range t {
			if i > 0 {
				buf.WriteByte(',')
			}
			appendJSON(buf, item, devicesTyping)
		}
		buf.WriteByte(']')
	default:
		buf.WriteString("null")
	}
}

// stateEntry is one kept datapoint value.
type stateEntry struct {
	Interface string          `json:"interface"`
	Address   string          `json:"address"`
	Datapoint string          `json:"datapoint"`
	Value     json.RawMessage `json:"value"`
	TS        string          `json:"ts"`
	Confirmed bool            `json:"confirmed"`
	Source    string          `json:"source"`
}

// valueStore holds the latest value of every kept datapoint, the data
// behind /api/rpc/v1/state. It is a chosen set, never a full value
// source.
type valueStore struct {
	mu      sync.Mutex
	keep    map[string]struct{}
	entries map[string]*stateEntry
}

func newValueStore(datapoints []string) *valueStore {
	keep := make(map[string]struct{}, len(datapoints))
	for _, d := range datapoints {
		keep[d] = struct{}{}
	}
	return &valueStore{keep: keep, entries: make(map[string]*stateEntry)}
}

// keeps reports whether datapoint belongs to the kept set.
func (s *valueStore) keeps(datapoint string) bool {
	if strings.HasPrefix(datapoint, "ERROR_") {
		return true
	}
	_, ok := s.keep[datapoint]
	return ok
}

// put records a value that arrived as a daemon event.
func (s *valueStore) put(iface, address, datapoint string, value json.RawMessage, at time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries[iface+"\x00"+address+"\x00"+datapoint] = &stateEntry{
		Interface: iface,
		Address:   address,
		Datapoint: datapoint,
		Value:     value,
		TS:        at.Format(time.RFC3339Nano),
		Confirmed: true,
		Source:    "event",
	}
}

// markRestored turns every entry into one read back from disk after a
// restart: unconfirmed until the next event for it arrives.
func (s *valueStore) markRestored() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.entries {
		e.Confirmed = false
		e.Source = "restored"
	}
}

// snapshot returns copies of all entries sorted by interface, address
// and datapoint.
func (s *valueStore) snapshot() []stateEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]stateEntry, 0, len(s.entries))
	for _, e := range s.entries {
		out = append(out, *e)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Interface != b.Interface {
			return a.Interface < b.Interface
		}
		if a.Address != b.Address {
			return a.Address < b.Address
		}
		return a.Datapoint < b.Datapoint
	})
	return out
}
