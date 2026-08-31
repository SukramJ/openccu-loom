// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// tools_link_paramset_test.go pins the LINK paramset surface ADR 0069
// requires: write_paramset refuses LINK (routing it to write_link_paramset
// instead), write_link_paramset reaches the domain with the receiver and
// sender in the right order, and the per-pair edit-lock grammar
// (channel:{receiver}:LINK:{sender}) is enforced rather than the retired
// channel-wide (channel:{receiver}:LINK) key.

package mcp_test

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/mcp"
	"github.com/SukramJ/openccu-loom/internal/north/rest/handlers"
)

// fakeEditLocksExact grants Verify only for one exact (key, token) pair,
// pinning the per-pair LINK lock grammar rather than merely "a lock
// exists". Open/Close are not exercised by these tests.
type fakeEditLocksExact struct {
	key   string
	token string
}

func (f *fakeEditLocksExact) Verify(key, token string) bool {
	return key == f.key && token == f.token
}

func (f *fakeEditLocksExact) Open(string, string) (handlers.EditLock, bool) {
	return handlers.EditLock{}, false
}

func (f *fakeEditLocksExact) Close(string, string) bool {
	return false
}

// TestWriteParamset_RefusesLinkKey is guard G1. write_paramset must refuse
// key LINK without ever reaching the paramset domain (neither PutParamset
// nor PutLinkParamset), and the refusal must not be a blanket "everything
// fails": a MASTER write with a valid token must still land.
func TestWriteParamset_RefusesLinkKey(t *testing.T) {
	ps := newFakeParamsets()
	devs, _, _ := makeDeviceFixture()
	locks := &fakeEditLocks{allow: true}

	deps := mcp.Deps{
		Centrals:    &fakeCentrals{names: []string{"ccu1", "ccu2"}},
		Devices:     devs,
		Paramsets:   ps,
		EditLocks:   locks,
		AllowWrites: true,
	}
	cs := connect(t, deps)
	defer cs.Close()

	res := callTool(t, cs, "write_paramset", map[string]any{
		"central_name": "ccu1",
		"address":      "ADDR001",
		"key":          "LINK",
		"values":       map[string]any{"SHORT_ACTION_TIME": 1.0},
	})
	if !res.IsError {
		t.Fatal("expected IsError=true for key LINK on write_paramset")
	}
	if len(ps.putCalls) != 0 {
		t.Errorf("PutParamset must not run for a refused LINK write, got %d calls", len(ps.putCalls))
	}
	if len(ps.putLinkCalls) != 0 {
		t.Errorf("PutLinkParamset must not run for a refused LINK write via write_paramset, got %d calls", len(ps.putLinkCalls))
	}

	// Second arm: MASTER with a valid token still writes — the refusal
	// above must not be a guard that happens to fail on everything.
	res2 := callTool(t, cs, "write_paramset", map[string]any{
		"central_name": "ccu1",
		"address":      "ADDR001",
		"key":          "MASTER",
		"values":       map[string]any{"MIN_SETPOINT": 10.0},
		"edit_token":   "tok-123",
	})
	if res2.IsError {
		t.Fatalf("MASTER write with a valid token must still succeed: %v", res2.Content)
	}
	if len(ps.putCalls) != 1 {
		t.Fatalf("expected 1 PutParamset call for the MASTER write, got %d", len(ps.putCalls))
	}
}

// TestWriteLinkParamset_PassesReceiverThenSender is guard G2.
// write_link_paramset must call PutLinkParamset with the receiver channel
// first and the sender (peer) channel second — the exact pair swap ADR
// 0069 documents as the defect.
func TestWriteLinkParamset_PassesReceiverThenSender(t *testing.T) {
	ps := newFakeParamsets()
	devs, _, _ := makeDeviceFixture()
	locks := &fakeEditLocks{allow: true}

	deps := mcp.Deps{
		Centrals:    &fakeCentrals{names: []string{"ccu1", "ccu2"}},
		Devices:     devs,
		Paramsets:   ps,
		EditLocks:   locks,
		AllowWrites: true,
	}
	cs := connect(t, deps)
	defer cs.Close()

	res := callTool(t, cs, "write_link_paramset", map[string]any{
		"central_name":             "ccu1",
		"receiver_channel_address": "ADDR001",
		"sender_channel_address":   "ADDR002",
		"values":                   map[string]any{"SHORT_ACTION_TIME": 1.0},
		"edit_token":               "tok-abc",
	})
	if res.IsError {
		t.Fatalf("write_link_paramset returned error: %v", res.Content)
	}
	if len(ps.putLinkCalls) != 1 {
		t.Fatalf("expected 1 PutLinkParamset call, got %d", len(ps.putLinkCalls))
	}
	got := ps.putLinkCalls[0]
	if got.channelAddress != "ADDR001" || got.peerAddress != "ADDR002" {
		t.Fatalf(
			"PutLinkParamset called with (channel=%q, peer=%q); want (channel=ADDR001, peer=ADDR002) — "+
				"a swapped pair is the exact ADR 0069 defect", got.channelAddress, got.peerAddress,
		)
	}
}

// TestWriteLinkParamset_LockGrammar is guard G3. It pins the three-arm
// per-pair lock grammar: no token refuses the write, a token valid for the
// per-pair key channel:{recv}:LINK:{send} lets it land, and a token valid
// only for the retired channel-wide key channel:{recv}:LINK still refuses
// it. The third arm is the one that pins the grammar rather than the mere
// existence of a lock.
func TestWriteLinkParamset_LockGrammar(t *testing.T) {
	devs, _, _ := makeDeviceFixture()
	args := map[string]any{
		"central_name":             "ccu1",
		"receiver_channel_address": "ADDR001",
		"sender_channel_address":   "ADDR002",
		"values":                   map[string]any{"SHORT_ACTION_TIME": 1.0},
	}

	// Arm 1: no token → refused, nothing written.
	t.Run("no_token_refused", func(t *testing.T) {
		ps := newFakeParamsets()
		locks := &fakeEditLocksExact{key: "channel:ADDR001:LINK:ADDR002", token: "tok-1"}
		deps := mcp.Deps{
			Centrals:    &fakeCentrals{names: []string{"ccu1", "ccu2"}},
			Devices:     devs,
			Paramsets:   ps,
			EditLocks:   locks,
			AllowWrites: true,
		}
		cs := connect(t, deps)
		defer cs.Close()

		res := callTool(t, cs, "write_link_paramset", args)
		if !res.IsError {
			t.Fatal("expected IsError=true with no edit_token")
		}
		if len(ps.putLinkCalls) != 0 {
			t.Fatalf("expected 0 PutLinkParamset calls with no token, got %d", len(ps.putLinkCalls))
		}
	})

	// Arm 2: a token valid for the per-pair key → the write lands.
	t.Run("per_pair_token_succeeds", func(t *testing.T) {
		ps := newFakeParamsets()
		locks := &fakeEditLocksExact{key: "channel:ADDR001:LINK:ADDR002", token: "tok-1"}
		deps := mcp.Deps{
			Centrals:    &fakeCentrals{names: []string{"ccu1", "ccu2"}},
			Devices:     devs,
			Paramsets:   ps,
			EditLocks:   locks,
			AllowWrites: true,
		}
		cs := connect(t, deps)
		defer cs.Close()

		withToken := map[string]any{}
		for k, v := range args {
			withToken[k] = v
		}
		withToken["edit_token"] = "tok-1"

		res := callTool(t, cs, "write_link_paramset", withToken)
		if res.IsError {
			t.Fatalf("a token valid for the per-pair key must succeed: %v", res.Content)
		}
		if len(ps.putLinkCalls) != 1 {
			t.Fatalf("expected 1 PutLinkParamset call, got %d", len(ps.putLinkCalls))
		}
	})

	// Arm 3: a token valid only for the retired channel-wide key → still
	// refused. This is the arm that pins the grammar: a lock check that
	// merely asks "is anything held" would pass here by mistake.
	t.Run("channel_wide_token_refused", func(t *testing.T) {
		ps := newFakeParamsets()
		locks := &fakeEditLocksExact{key: "channel:ADDR001:LINK", token: "tok-1"}
		deps := mcp.Deps{
			Centrals:    &fakeCentrals{names: []string{"ccu1", "ccu2"}},
			Devices:     devs,
			Paramsets:   ps,
			EditLocks:   locks,
			AllowWrites: true,
		}
		cs := connect(t, deps)
		defer cs.Close()

		withToken := map[string]any{}
		for k, v := range args {
			withToken[k] = v
		}
		withToken["edit_token"] = "tok-1"

		res := callTool(t, cs, "write_link_paramset", withToken)
		if !res.IsError {
			t.Fatal("expected IsError=true when only the channel-wide LINK key is held")
		}
		if len(ps.putLinkCalls) != 0 {
			t.Fatalf("expected 0 PutLinkParamset calls when only the channel-wide key is held, got %d", len(ps.putLinkCalls))
		}
	})
}

// TestReadLinkParamset_ReadsThroughParamsetsDomain pins that
// read_link_paramset reads through ParamsetService.GetLinkParamset with
// the receiver/sender pair, the same domain method WS links.get_paramset
// uses. Reads carry no lock.
func TestReadLinkParamset_ReadsThroughParamsetsDomain(t *testing.T) {
	ps := newFakeParamsets()
	ps.linkStore["ADDR001|ADDR002"] = map[string]any{"SHORT_ACTION_TIME": 1.0}

	deps := mcp.Deps{
		Centrals:  &fakeCentrals{names: []string{"ccu1"}},
		Paramsets: ps,
	}
	cs := connect(t, deps)
	defer cs.Close()

	res := callTool(t, cs, "read_link_paramset", map[string]any{
		"receiver_channel_address": "ADDR001",
		"sender_channel_address":   "ADDR002",
	})
	if res.IsError {
		t.Fatalf("read_link_paramset returned error: %v", res.Content)
	}
	var out struct {
		Values map[string]any `json:"values"`
	}
	unmarshalStructured(t, res, &out)
	if v, ok := out.Values["SHORT_ACTION_TIME"]; !ok || v != 1.0 {
		t.Fatalf("out.Values=%+v, want SHORT_ACTION_TIME=1.0", out.Values)
	}
}

// TestOpenEditSession_AcceptsLinkWithPeer pins that open_edit_session
// builds the per-pair key when key=LINK and peer_address is supplied.
func TestOpenEditSession_AcceptsLinkWithPeer(t *testing.T) {
	locks := &fakeEditLocks{openOK: true, openResult: handlers.EditLock{Token: "tok-abc"}}
	deps := mcp.Deps{
		Centrals:    &fakeCentrals{names: []string{"ccu1"}},
		EditLocks:   locks,
		AllowWrites: true,
	}
	cs := connect(t, deps)
	defer cs.Close()

	res := callTool(t, cs, "open_edit_session", map[string]any{
		"address":      "ADDR001",
		"key":          "LINK",
		"peer_address": "ADDR002",
	})
	if res.IsError {
		t.Fatalf("open_edit_session with LINK+peer_address returned error: %v", res.Content)
	}
	if locks.openKey != "channel:ADDR001:LINK:ADDR002" {
		t.Errorf("Open called with %q, want channel:ADDR001:LINK:ADDR002", locks.openKey)
	}
}

// TestOpenEditSession_RejectsLinkWithoutPeer pins that LINK without a peer
// is a hard error rather than a silent fall-back to a channel-wide lock —
// that grammar was retired.
func TestOpenEditSession_RejectsLinkWithoutPeer(t *testing.T) {
	locks := &fakeEditLocks{openOK: true}
	deps := mcp.Deps{
		Centrals:    &fakeCentrals{names: []string{"ccu1"}},
		EditLocks:   locks,
		AllowWrites: true,
	}
	cs := connect(t, deps)
	defer cs.Close()

	res := callTool(t, cs, "open_edit_session", map[string]any{
		"address": "ADDR001",
		"key":     "LINK",
	})
	if !res.IsError {
		t.Fatal("expected IsError=true for LINK without peer_address")
	}
	if locks.openKey != "" {
		t.Errorf("Open must not be called without peer_address, saw %q", locks.openKey)
	}
}
