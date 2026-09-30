// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package ws

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/configui"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// keyRecordingQuery records the session key of every paramset read so a
// test can assert that a rejected key never reached the service.
type keyRecordingQuery struct {
	stubDeviceQuery
	keys []configui.SessionKey
}

func (q *keyRecordingQuery) GetParamsetDescription(_ context.Context, key configui.SessionKey) (map[string]any, error) {
	q.keys = append(q.keys, key)
	return map[string]any{}, nil
}

func (q *keyRecordingQuery) GetParamset(_ context.Context, key configui.SessionKey) (map[string]any, error) {
	q.keys = append(q.keys, key)
	return map[string]any{"P": 1}, nil
}

// keyRecordingRW records every read and write key for paramset.copy.
type keyRecordingRW struct {
	reads, writes []configui.SessionKey
}

func (s *keyRecordingRW) GetParamset(_ context.Context, key configui.SessionKey) (map[string]any, error) {
	s.reads = append(s.reads, key)
	return map[string]any{"P": 1}, nil
}

func (s *keyRecordingRW) PutParamset(
	_ context.Context, key configui.SessionKey, _ map[string]any,
) (*interfaces.ParamsetWriteReport, error) {
	s.writes = append(s.writes, key)
	return nil, nil
}

// keyRecordingBackend records the session key of every config.session.open.
type keyRecordingBackend struct {
	opens []configui.SessionKey
}

func (b *keyRecordingBackend) Open(_ context.Context, key configui.SessionKey) (values, descriptions map[string]any, err error) {
	b.opens = append(b.opens, key)
	return map[string]any{}, map[string]any{"P": 1}, nil
}

func (b *keyRecordingBackend) PutParamset(context.Context, configui.SessionKey, map[string]any) error {
	return nil
}

// unrecognisedKeys are strings a caller might send that are not one of the
// three wire keys. Each would otherwise be forwarded verbatim to the CCU.
var unrecognisedKeys = []string{"master", "Values", "SERVICE", "CALCULATED", "JEQ0123456:1", " MASTER"}

func wantBadRequestNamingKeys(t *testing.T, err error) {
	t.Helper()
	var ce *CommandError
	if !errors.As(err, &ce) {
		t.Fatalf("want *CommandError, got %v", err)
	}
	if ce.Code != CommandErrorBadRequest {
		t.Fatalf("code = %q, want %q", ce.Code, CommandErrorBadRequest)
	}
	if !strings.Contains(ce.Message, "MASTER") || !strings.Contains(ce.Message, "VALUES") {
		t.Fatalf("message %q does not name the allowed keys", ce.Message)
	}
}

func rawArgs(t *testing.T, v map[string]any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

func TestParamsetPutRejectsUnrecognisedKeyBeforeWrite(t *testing.T) {
	t.Parallel()
	for _, k := range unrecognisedKeys {
		pw := &stubParamsetWriter{}
		_, err := paramsetPutHandler(pw, nil)(context.Background(),
			rawArgs(t, map[string]any{"channel_address": "ABC0001:1", "paramset_key": k, "values": map[string]any{"P": 1}}))
		wantBadRequestNamingKeys(t, err)
		if len(pw.calls) != 0 {
			t.Fatalf("key %q reached PutParamset: %+v", k, pw.calls)
		}
	}
}

func TestParamsetPutAcceptsWireKeys(t *testing.T) {
	t.Parallel()
	for _, k := range []hmenum.ParamsetKey{hmenum.ParamsetKeyMaster, hmenum.ParamsetKeyValues} {
		pw := &stubParamsetWriter{}
		_, err := paramsetPutHandler(pw, nil)(context.Background(),
			rawArgs(t, map[string]any{"channel_address": "ABC0001:1", "paramset_key": string(k), "values": map[string]any{"P": 1}}))
		if err != nil {
			t.Fatalf("key %s: %v", k, err)
		}
		if len(pw.calls) != 1 || pw.calls[0].key.ParamsetKey != k {
			t.Fatalf("key %s: calls = %+v", k, pw.calls)
		}
	}
	// LINK keeps its dedicated refusal, which points at links.put_paramset.
	pw := &stubParamsetWriter{}
	_, err := paramsetPutHandler(pw, nil)(context.Background(),
		rawArgs(t, map[string]any{"channel_address": "ABC0001:1", "paramset_key": "LINK", "values": map[string]any{"P": 1}}))
	if err == nil || !strings.Contains(err.Error(), "links.put_paramset") {
		t.Fatalf("LINK: want dedicated refusal, got %v", err)
	}
	if len(pw.calls) != 0 {
		t.Fatalf("LINK reached PutParamset")
	}
}

func TestParamsetCopyRejectsUnrecognisedKeyBeforeRead(t *testing.T) {
	t.Parallel()
	for _, k := range append([]string{"LINK"}, unrecognisedKeys...) {
		rw := &keyRecordingRW{}
		_, err := paramsetCopyHandler(rw, rw, nil)(context.Background(), rawArgs(t, map[string]any{
			"source_channel_address": "ABC0001:1", "target_channel_address": "ABC0002:1", "paramset_key": k,
		}))
		wantBadRequestNamingKeys(t, err)
		if len(rw.reads)+len(rw.writes) != 0 {
			t.Fatalf("key %q reached the service: reads=%v writes=%v", k, rw.reads, rw.writes)
		}
	}
}

func TestParamsetCopyAcceptsWireKeysAndDefaultsToMaster(t *testing.T) {
	t.Parallel()
	cases := map[string]hmenum.ParamsetKey{
		"":       hmenum.ParamsetKeyMaster,
		"MASTER": hmenum.ParamsetKeyMaster,
		"VALUES": hmenum.ParamsetKeyValues,
	}
	for in, want := range cases {
		rw := &keyRecordingRW{}
		_, err := paramsetCopyHandler(rw, rw, nil)(context.Background(), rawArgs(t, map[string]any{
			"source_channel_address": "ABC0001:1", "target_channel_address": "ABC0002:1", "paramset_key": in,
		}))
		if err != nil {
			t.Fatalf("key %q: %v", in, err)
		}
		if len(rw.reads) != 1 || rw.reads[0].ParamsetKey != want || len(rw.writes) != 1 || rw.writes[0].ParamsetKey != want {
			t.Fatalf("key %q: reads=%v writes=%v, want %s on both", in, rw.reads, rw.writes, want)
		}
	}
}

func TestParamsetReadCommandsValidateKey(t *testing.T) {
	t.Parallel()
	handlers := map[string]func(DeviceQuery) CommandHandler{
		"paramset.get":         paramsetGetHandler,
		"paramset.description": paramsetDescriptionHandler,
	}
	valid := map[string]hmenum.ParamsetKey{
		"":       hmenum.ParamsetKeyMaster,
		"MASTER": hmenum.ParamsetKeyMaster,
		"VALUES": hmenum.ParamsetKeyValues,
		"LINK":   hmenum.ParamsetKeyLink,
	}
	for name, mk := range handlers {
		for _, k := range unrecognisedKeys {
			q := &keyRecordingQuery{}
			_, err := mk(q)(context.Background(), rawArgs(t, map[string]any{"channel_address": "ABC0001:1", "paramset_key": k}))
			wantBadRequestNamingKeys(t, err)
			if len(q.keys) != 0 {
				t.Fatalf("%s: key %q reached the service", name, k)
			}
		}
		for in, want := range valid {
			q := &keyRecordingQuery{}
			if _, err := mk(q)(context.Background(), rawArgs(t, map[string]any{"channel_address": "ABC0001:1", "paramset_key": in})); err != nil {
				t.Fatalf("%s: key %q: %v", name, in, err)
			}
			if len(q.keys) != 1 || q.keys[0].ParamsetKey != want {
				t.Fatalf("%s: key %q: got %v, want %s", name, in, q.keys, want)
			}
		}
	}
}

func TestSessionOpenValidatesKey(t *testing.T) {
	t.Parallel()
	for _, k := range unrecognisedKeys {
		b := &keyRecordingBackend{}
		_, err := sessionOpenHandler(configui.NewSessionStore(), b)(context.Background(),
			rawArgs(t, map[string]any{"channel_address": "ABC0001:1", "paramset_key": k}))
		wantBadRequestNamingKeys(t, err)
		if len(b.opens) != 0 {
			t.Fatalf("key %q reached the backend", k)
		}
	}
	valid := map[string]hmenum.ParamsetKey{
		"":       hmenum.ParamsetKeyMaster,
		"MASTER": hmenum.ParamsetKeyMaster,
		"VALUES": hmenum.ParamsetKeyValues,
		"LINK":   hmenum.ParamsetKeyLink,
	}
	for in, want := range valid {
		b := &keyRecordingBackend{}
		if _, err := sessionOpenHandler(configui.NewSessionStore(), b)(context.Background(),
			rawArgs(t, map[string]any{"channel_address": "ABC0001:1", "paramset_key": in})); err != nil {
			t.Fatalf("key %q: %v", in, err)
		}
		if len(b.opens) != 1 || b.opens[0].ParamsetKey != want {
			t.Fatalf("key %q: opens=%v, want %s", in, b.opens, want)
		}
	}
}

// TestSessionMutateCommandsValidateKey drives every sessionMutateArgs
// consumer: a bad key is refused as such, not reported as a missing session,
// while an empty key still finds the session opened under MASTER.
func TestSessionMutateCommandsValidateKey(t *testing.T) {
	t.Parallel()
	mk := map[string]func(*configui.SessionStore) CommandHandler{
		"set":     sessionSetHandler,
		"undo":    func(s *configui.SessionStore) CommandHandler { return sessionStackHandler(s, true) },
		"redo":    func(s *configui.SessionStore) CommandHandler { return sessionStackHandler(s, false) },
		"discard": sessionDiscardHandler,
		"changes": sessionChangesHandler,
		"save": func(s *configui.SessionStore) CommandHandler {
			return sessionSaveHandler(s, &keyRecordingBackend{}, nil, nil)
		},
	}
	masterKey := configui.SessionKey{ChannelAddress: "ABC0001:1", ParamsetKey: hmenum.ParamsetKeyMaster}
	for name, h := range mk {
		for _, k := range unrecognisedKeys {
			store := configui.NewSessionStore()
			store.Put(masterKey, configui.NewSession(nil, map[string]any{"P": 1}))
			_, err := h(store)(context.Background(), rawArgs(t, map[string]any{
				"channel_address": "ABC0001:1", "paramset_key": k, "parameter": "P", "value": 2,
			}))
			wantBadRequestNamingKeys(t, err)
		}
		store := configui.NewSessionStore()
		store.Put(masterKey, configui.NewSession(nil, map[string]any{"P": 1}))
		if _, err := h(store)(context.Background(), rawArgs(t, map[string]any{
			"channel_address": "ABC0001:1", "parameter": "P", "value": 2,
		})); err != nil {
			t.Fatalf("%s: empty key should address the MASTER session: %v", name, err)
		}
	}
}
