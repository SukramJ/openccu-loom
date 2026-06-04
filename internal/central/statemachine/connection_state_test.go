// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package statemachine

import (
	"sync"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

func TestConnectionStateAddRemoveClear(t *testing.T) {
	cs := NewConnectionState()
	if cs.HasAnyIssue() {
		t.Fatal("fresh ConnectionState must report no issue")
	}
	cs.AddIssue("HmIP-RF", hmenum.FailureReasonNetwork, "ping timeout")
	cs.AddIssue("BidCos-RF", hmenum.FailureReasonAuth, "401")
	if got := cs.IssueCount(); got != 2 {
		t.Fatalf("IssueCount=%d, want 2", got)
	}
	if !cs.HasAnyIssue() {
		t.Fatal("HasAnyIssue must report true after AddIssue")
	}
	// Re-adding same source → bumps Count, keeps FirstSeen.
	cs.AddIssue("HmIP-RF", hmenum.FailureReasonNetwork, "ping timeout")
	got, ok := cs.IssueFor("HmIP-RF")
	if !ok || got.Count != 2 {
		t.Fatalf("re-add must bump count: %+v", got)
	}
	if !cs.RemoveIssue("HmIP-RF") {
		t.Fatal("RemoveIssue should return true for known source")
	}
	cs.ClearAllIssues()
	if cs.HasAnyIssue() {
		t.Fatal("ClearAllIssues must drop everything")
	}
}

// --- C-CSTATE-1: per-issuer kind classification ---

// TestClassifySourceRPCProxyPrefixes verifies that well-known
// interface-ID prefixes are classified as IssueKindRPCProxy.
func TestClassifySourceRPCProxyPrefixes(t *testing.T) {
	t.Parallel()
	rpcSources := []string{
		"HmIP-RF",
		"HmIP-RF:0",
		"BidCos-RF",
		"BidCos-Wired",
		"VirtualDevices",
		"CUxD",
		"Homegear",
		"Homegear:1",
	}
	for _, src := range rpcSources {
		if got := classifySource(src); got != IssueKindRPCProxy {
			t.Errorf("classifySource(%q) = %v, want IssueKindRPCProxy", src, got)
		}
	}
}

// TestClassifySourceJSONForUnknown verifies that sources that do not
// match any RPC-proxy prefix are classified as IssueKindJSON.
func TestClassifySourceJSONForUnknown(t *testing.T) {
	t.Parallel()
	jsonSources := []string{"rega", "json-rpc", "unknown", ""}
	for _, src := range jsonSources {
		if got := classifySource(src); got != IssueKindJSON {
			t.Errorf("classifySource(%q) = %v, want IssueKindJSON", src, got)
		}
	}
}

// TestIsRPCProxyIssueAndIsJSONIssue verifies C-CSTATE-1: per-issuer
// classification is correctly reported via IsRPCProxyIssue / IsJSONIssue.
func TestIsRPCProxyIssueAndIsJSONIssue(t *testing.T) {
	t.Parallel()
	cs := NewConnectionState()

	cs.AddIssue("HmIP-RF", hmenum.FailureReasonNetwork, "ping timeout")
	cs.AddIssue("rega", hmenum.FailureReasonInternal, "script failed")

	if !cs.IsRPCProxyIssue("HmIP-RF") {
		t.Error("IsRPCProxyIssue(HmIP-RF) must be true")
	}
	if cs.IsJSONIssue("HmIP-RF") {
		t.Error("IsJSONIssue(HmIP-RF) must be false")
	}
	if !cs.IsJSONIssue("rega") {
		t.Error("IsJSONIssue(rega) must be true")
	}
	if cs.IsRPCProxyIssue("rega") {
		t.Error("IsRPCProxyIssue(rega) must be false")
	}
	if cs.RPCProxyIssueCount() != 1 {
		t.Errorf("RPCProxyIssueCount()=%d, want 1", cs.RPCProxyIssueCount())
	}
	if cs.JSONIssueCount() != 1 {
		t.Errorf("JSONIssueCount()=%d, want 1", cs.JSONIssueCount())
	}
}

// --- C-CSTATE-2: SetOnChanged / publish hook ---

// TestSetOnChangedFiredOnAddIssue verifies that the registered
// onChanged callback fires with connected=false when a new issue is added.
func TestSetOnChangedFiredOnAddIssue(t *testing.T) {
	t.Parallel()
	cs := NewConnectionState()

	var calls []struct {
		source    string
		connected bool
	}
	cs.SetOnChanged(func(src string, conn bool) {
		calls = append(calls, struct {
			source    string
			connected bool
		}{src, conn})
	})

	cs.AddIssue("HmIP-RF", hmenum.FailureReasonNetwork, "timeout")
	if len(calls) != 1 || calls[0].source != "HmIP-RF" || calls[0].connected {
		t.Fatalf("onChanged: got %+v, want [{HmIP-RF false}]", calls)
	}

	// Re-adding same source must NOT fire onChanged again.
	cs.AddIssue("HmIP-RF", hmenum.FailureReasonNetwork, "timeout again")
	if len(calls) != 1 {
		t.Fatalf("onChanged fired again on re-add: %+v", calls)
	}
}

// TestSetOnChangedFiredOnRemoveIssue verifies that the registered
// onChanged callback fires with connected=true when an issue is removed.
func TestSetOnChangedFiredOnRemoveIssue(t *testing.T) {
	t.Parallel()
	cs := NewConnectionState()
	cs.AddIssue("BidCos-RF", hmenum.FailureReasonNetwork, "lost")

	var calls []struct {
		source    string
		connected bool
	}
	cs.SetOnChanged(func(src string, conn bool) {
		calls = append(calls, struct {
			source    string
			connected bool
		}{src, conn})
	})

	cs.RemoveIssue("BidCos-RF")
	if len(calls) != 1 || calls[0].source != "BidCos-RF" || !calls[0].connected {
		t.Fatalf("onChanged: got %+v, want [{BidCos-RF true}]", calls)
	}

	// Removing unknown source must not fire.
	cs.RemoveIssue("NoSuchSource")
	if len(calls) != 1 {
		t.Fatalf("onChanged fired for unknown source: %+v", calls)
	}
}

// TestSetOnChangedNilSafe verifies that a nil onChanged does not panic.
func TestSetOnChangedNilSafe(t *testing.T) {
	t.Parallel()
	cs := NewConnectionState()
	cs.SetOnChanged(nil)
	// Must not panic.
	cs.AddIssue("HmIP-RF", hmenum.FailureReasonNetwork, "timeout")
	cs.RemoveIssue("HmIP-RF")
}

// ---------------------------------------------------------------------------
// ConnectionState — additional accessor and edge-case tests
// ---------------------------------------------------------------------------

func TestConnectionStateIssues(t *testing.T) {
	cs := NewConnectionState()
	if got := cs.Issues(); got != nil {
		t.Fatal("Issues() must be nil when empty")
	}
	cs.AddIssue("src-a", hmenum.FailureReasonNetwork, "msg-a")
	cs.AddIssue("src-b", hmenum.FailureReasonAuth, "msg-b")
	issues := cs.Issues()
	if len(issues) != 2 {
		t.Fatalf("Issues len=%d, want 2", len(issues))
	}
}

func TestConnectionStateRemoveIssueReturnsFalseForUnknown(t *testing.T) {
	cs := NewConnectionState()
	if cs.RemoveIssue("no-such-source") {
		t.Fatal("RemoveIssue must return false for unknown source")
	}
}

func TestConnectionStateAddIssueEmptySourceIsNoop(t *testing.T) {
	cs := NewConnectionState()
	cs.AddIssue("", hmenum.FailureReasonNetwork, "ignored")
	if cs.HasAnyIssue() {
		t.Fatal("empty source must be silently ignored")
	}
}

func TestConnectionStateConcurrentAddRemove(t *testing.T) {
	t.Parallel()
	cs := NewConnectionState()
	var wg sync.WaitGroup
	for i := range 60 {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			src := "src"
			cs.AddIssue(src, hmenum.FailureReasonNetwork, "x")
			_ = cs.HasAnyIssue()
			_ = cs.IssueCount()
			_ = cs.Issues()
			_, _ = cs.IssueFor(src)
			_ = cs.RemoveIssue(src)
		}(i)
	}
	wg.Wait()
}

func TestConnectionStateFirstSeenPreservedOnReAdd(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	t1 := t0.Add(time.Hour)
	var call int
	cs := NewConnectionState()
	cs.now = func() time.Time {
		call++
		if call == 1 {
			return t0
		}
		return t1
	}
	cs.AddIssue("x", hmenum.FailureReasonNetwork, "first")
	cs.AddIssue("x", hmenum.FailureReasonNetwork, "second")
	issue, ok := cs.IssueFor("x")
	if !ok {
		t.Fatal("issue not found")
	}
	if !issue.FirstSeen.Equal(t0) {
		t.Fatalf("FirstSeen=%v, want %v", issue.FirstSeen, t0)
	}
	if !issue.LastSeen.Equal(t1) {
		t.Fatalf("LastSeen=%v, want %v", issue.LastSeen, t1)
	}
	if issue.Count != 2 {
		t.Fatalf("Count=%d, want 2", issue.Count)
	}
}
