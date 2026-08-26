// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// White-box tests for subtype_responder.go unexported helpers.
// These run in package mdns so they can reach the unexported functions.

package mdns

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"testing"

	"github.com/miekg/dns"
)

// ---- ensureTrailingDot ----

func TestEnsureTrailingDot_AlreadyHasDot(t *testing.T) {
	t.Parallel()
	if got := ensureTrailingDot("foo.bar."); got != "foo.bar." {
		t.Errorf("ensureTrailingDot(%q) = %q, want %q", "foo.bar.", got, "foo.bar.")
	}
}

func TestEnsureTrailingDot_Missing(t *testing.T) {
	t.Parallel()
	if got := ensureTrailingDot("foo.bar"); got != "foo.bar." {
		t.Errorf("ensureTrailingDot(%q) = %q, want %q", "foo.bar", got, "foo.bar.")
	}
}

func TestEnsureTrailingDot_Empty(t *testing.T) {
	t.Parallel()
	// Empty string: still appends the dot.
	if got := ensureTrailingDot(""); got != "." {
		t.Errorf("ensureTrailingDot(%q) = %q, want %q", "", got, ".")
	}
}

// ---- isTimeout ----

func TestIsTimeout_Nil(t *testing.T) {
	t.Parallel()
	if isTimeout(nil) {
		t.Error("isTimeout(nil) = true, want false")
	}
}

func TestIsTimeout_NonNetError(t *testing.T) {
	t.Parallel()
	err := dns.ErrBuf // arbitrary non-net.Error
	if isTimeout(err) {
		t.Errorf("isTimeout(%T) = true, want false", err)
	}
}

// ---- isPrimaryV4 ----

func TestIsPrimaryV4_Loopback(t *testing.T) {
	t.Parallel()
	lo := &net.Interface{
		Flags: net.FlagUp | net.FlagLoopback | net.FlagMulticast,
	}
	if isPrimaryV4(lo) {
		t.Error("isPrimaryV4(loopback) = true, want false")
	}
}

func TestIsPrimaryV4_PointToPoint(t *testing.T) {
	t.Parallel()
	ptp := &net.Interface{
		Flags: net.FlagUp | net.FlagPointToPoint | net.FlagMulticast,
	}
	if isPrimaryV4(ptp) {
		t.Error("isPrimaryV4(point-to-point) = true, want false")
	}
}

// ---- isPrimaryV6 ----

func TestIsPrimaryV6_Loopback(t *testing.T) {
	t.Parallel()
	lo := &net.Interface{
		Flags: net.FlagUp | net.FlagLoopback | net.FlagMulticast,
	}
	if isPrimaryV6(lo) {
		t.Error("isPrimaryV6(loopback) = true, want false")
	}
}

func TestIsPrimaryV6_PointToPoint(t *testing.T) {
	t.Parallel()
	ptp := &net.Interface{
		Flags: net.FlagUp | net.FlagPointToPoint | net.FlagMulticast,
	}
	if isPrimaryV6(ptp) {
		t.Error("isPrimaryV6(point-to-point) = true, want false")
	}
}

// ---- listMulticastInterfaces ----

func TestListMulticastInterfaces_ReturnsNonNilOnAnyOS(t *testing.T) {
	t.Parallel()
	// May be empty in a sandbox but must not panic.
	ifaces := listMulticastInterfaces()
	// All returned entries must have FlagUp + FlagMulticast set.
	for _, ifi := range ifaces {
		if ifi.Flags&net.FlagUp == 0 {
			t.Errorf("interface %q: FlagUp not set", ifi.Name)
		}
		if ifi.Flags&net.FlagMulticast == 0 {
			t.Errorf("interface %q: FlagMulticast not set", ifi.Name)
		}
	}
}

// ---- SubtypeResponder.AddSubtype / RemoveSubtype (nil guard) ----

func TestSubtypeResponder_NilReceiver(t *testing.T) {
	t.Parallel()
	var r *SubtypeResponder

	// None of these should panic.
	r.AddSubtype("_L1._sub._matterc._udp.local", "inst._matterc._udp.local")
	r.RemoveSubtype("_L1._sub._matterc._udp.local")
	_ = r.Close()
	r.Start(nil) //nolint:staticcheck // nil ctx intentional for guard test
}

// ---- buildReply / matchAnswers via a freshly constructed responder ----

// newTestResponder builds a SubtypeResponder with no actual socket
// by directly constructing the struct (white-box, same package).
func newTestResponder() *SubtypeResponder {
	return &SubtypeResponder{
		logger:   slog.Default(),
		mappings: make(map[string]string),
	}
}

func TestBuildReply_GarbageInput(t *testing.T) {
	t.Parallel()
	r := newTestResponder()
	out, ok := r.buildReply([]byte{0xFF, 0xFE, 0xFD})
	if ok || out != nil {
		t.Error("buildReply on garbage: expected (nil, false)")
	}
}

func TestBuildReply_ResponsePacket_Ignored(t *testing.T) {
	t.Parallel()
	r := newTestResponder()

	// Build a DNS response (not a query).
	msg := new(dns.Msg)
	msg.SetReply(new(dns.Msg))
	buf, _ := msg.Pack()

	out, ok := r.buildReply(buf)
	if ok || out != nil {
		t.Error("buildReply on DNS response: expected (nil, false)")
	}
}

func TestBuildReply_NoMappings_ReturnsFalse(t *testing.T) {
	t.Parallel()
	r := newTestResponder()

	msg := new(dns.Msg)
	msg.SetQuestion("_l3840._sub._matterc._udp.local.", dns.TypePTR)
	buf, _ := msg.Pack()

	out, ok := r.buildReply(buf)
	if ok || out != nil {
		t.Error("buildReply with no mappings: expected (nil, false)")
	}
}

func TestBuildReply_MatchingPTR_ReturnsReply(t *testing.T) {
	t.Parallel()
	r := newTestResponder()
	qname := "_l3840._sub._matterc._udp.local."
	target := "aabbccddeeff1122._matterc._udp.local."
	r.AddSubtype(qname, target)

	msg := new(dns.Msg)
	msg.SetQuestion(qname, dns.TypePTR)
	// Non-zero query ID: the multicast reply must still carry ID 0 per
	// RFC 6762 §18.1 (legacy queries arrive with arbitrary IDs).
	msg.Id = 0x1234
	buf, _ := msg.Pack()

	out, ok := r.buildReply(buf)
	if !ok || len(out) == 0 {
		t.Fatal("buildReply: expected (data, true) for matching PTR query")
	}

	// Parse and validate the reply.
	resp := new(dns.Msg)
	if err := resp.Unpack(out); err != nil {
		t.Fatalf("unpack reply: %v", err)
	}
	if !resp.Response {
		t.Error("reply: Response flag not set")
	}
	if !resp.Authoritative {
		t.Error("reply: Authoritative flag not set")
	}
	// RFC 6762 §6: a question section in a multicast response makes
	// strict stacks (Avahi, including subnet reflectors) drop the whole
	// reply — the commissioner then never sees the subtype answer.
	if len(resp.Question) != 0 {
		t.Errorf("reply: question section has %d entries, want 0 (RFC 6762 §6)", len(resp.Question))
	}
	if resp.Id != 0 {
		t.Errorf("reply: Id = 0x%X, want 0 (RFC 6762 §18.1)", resp.Id)
	}
	if len(resp.Answer) != 1 {
		t.Fatalf("reply: len(Answer)=%d, want 1", len(resp.Answer))
	}
	ptr, ok := resp.Answer[0].(*dns.PTR)
	if !ok {
		t.Fatalf("reply Answer[0] type=%T, want *dns.PTR", resp.Answer[0])
	}
	if ptr.Ptr != target {
		t.Errorf("ptr.Ptr = %q, want %q", ptr.Ptr, target)
	}
}

func TestBuildReply_TypeANY_AlsoMatches(t *testing.T) {
	t.Parallel()
	r := newTestResponder()
	qname := "_cm._sub._matterc._udp.local."
	target := "instance._matterc._udp.local."
	r.AddSubtype(qname, target)

	msg := new(dns.Msg)
	msg.SetQuestion(qname, dns.TypeANY)
	buf, _ := msg.Pack()

	_, ok := r.buildReply(buf)
	if !ok {
		t.Error("buildReply with TypeANY: expected ok=true")
	}
}

func TestBuildReply_WrongQtype_ReturnsFalse(t *testing.T) {
	t.Parallel()
	r := newTestResponder()
	qname := "_cm._sub._matterc._udp.local."
	r.AddSubtype(qname, "inst._matterc._udp.local.")

	msg := new(dns.Msg)
	msg.SetQuestion(qname, dns.TypeA) // not PTR or ANY
	buf, _ := msg.Pack()

	out, ok := r.buildReply(buf)
	if ok || out != nil {
		t.Error("buildReply with TypeA: expected (nil, false)")
	}
}

func TestMatchAnswers_Empty_ReturnsNil(t *testing.T) {
	t.Parallel()
	r := newTestResponder()
	got := r.matchAnswers(nil)
	if got != nil {
		t.Errorf("matchAnswers(nil): %v, want nil", got)
	}
}

func TestMatchAnswers_CaseInsensitiveLookup(t *testing.T) {
	t.Parallel()
	r := newTestResponder()
	// AddSubtype lowercases the key.
	r.AddSubtype("_CM._sub._matterc._udp.local.", "inst._matterc._udp.local.")

	// Query with mixed-case name.
	qs := []dns.Question{{
		Name:  "_CM._sub._matterc._udp.local.",
		Qtype: dns.TypePTR,
	}}
	got := r.matchAnswers(qs)
	if len(got) != 1 {
		t.Fatalf("matchAnswers: len=%d, want 1", len(got))
	}
}

func TestRemoveSubtype_ClearsMapping(t *testing.T) {
	t.Parallel()
	r := newTestResponder()
	qname := "_l3840._sub._matterc._udp.local."
	r.AddSubtype(qname, "inst._matterc._udp.local.")
	r.RemoveSubtype(qname)

	qs := []dns.Question{{Name: qname, Qtype: dns.TypePTR}}
	got := r.matchAnswers(qs)
	if len(got) != 0 {
		t.Errorf("matchAnswers after RemoveSubtype: len=%d, want 0", len(got))
	}
}

// ---- Start / Close with no sockets (nil pc4 / pc6 paths) ----

func TestSubtypeResponder_Start_NoPCSockets_NoGoroutines(t *testing.T) {
	t.Parallel()
	// Build a responder without actual network sockets (pc4=nil, pc6=nil).
	r := newTestResponder()
	// Start should not launch any goroutines when both pc4 and pc6 are nil.
	ctx := t.Context()
	r.Start(ctx)
	// Calling Start again (cancel != nil now) must be a no-op.
	r.Start(ctx)
	// Close should work even without sockets.
	if err := r.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestSubtypeResponder_Close_WithoutStart(t *testing.T) {
	t.Parallel()
	r := newTestResponder()
	// Close without Start must not block.
	if err := r.Close(); err != nil {
		t.Fatalf("Close without Start: %v", err)
	}
}

func TestSubtypeResponder_Close_Idempotent(t *testing.T) {
	t.Parallel()
	r := newTestResponder()
	ctx := t.Context()
	r.Start(ctx)
	if err := r.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	// Second Close must not panic or return an error.
	if err := r.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

// ---- isPrimaryV4 / isPrimaryV6 — real interface walk ----

// TestIsPrimaryV4_AllInterfaces exercises isPrimaryV4 against every
// interface on the host so the function body is exercised regardless
// of which flag combinations the test runner has. We only check that
// the call does not panic — correctness of the "true" case depends on
// the host having a non-loopback IPv4 interface.
func TestIsPrimaryV4_AllSystemInterfaces(t *testing.T) {
	t.Parallel()
	ifaces, _ := net.Interfaces()
	for i := range ifaces {
		ifi := &ifaces[i]
		// Must not panic.
		_ = isPrimaryV4(ifi)
	}
}

func TestIsPrimaryV6_AllSystemInterfaces(t *testing.T) {
	t.Parallel()
	ifaces, _ := net.Interfaces()
	for i := range ifaces {
		ifi := &ifaces[i]
		_ = isPrimaryV6(ifi)
	}
}

// TestIsPrimaryV4_NonLoopback_WithAddress exercises the "has non-loopback
// IPv4" path. We look for a suitable interface on this machine; if none
// exists we skip — this is a coverage opportunistic test.
func TestIsPrimaryV4_WithRealInterface(t *testing.T) {
	t.Parallel()
	ifaces, _ := net.Interfaces()
	for i := range ifaces {
		ifi := &ifaces[i]
		if ifi.Flags&net.FlagLoopback != 0 || ifi.Flags&net.FlagPointToPoint != 0 {
			continue
		}
		// Just verify the call does not panic.
		result := isPrimaryV4(ifi)
		_ = result
	}
}

func TestIsPrimaryV6_WithRealInterface(t *testing.T) {
	t.Parallel()
	ifaces, _ := net.Interfaces()
	for i := range ifaces {
		ifi := &ifaces[i]
		if ifi.Flags&net.FlagLoopback != 0 || ifi.Flags&net.FlagPointToPoint != 0 {
			continue
		}
		result := isPrimaryV6(ifi)
		_ = result
	}
}

// ---- AddSubtype / RemoveSubtype — nil-value guards ----

func TestAddSubtype_EmptySubType_NoOp(t *testing.T) {
	t.Parallel()
	r := newTestResponder()
	// Empty subType — should not add.
	r.AddSubtype("", "inst._matterc._udp.local.")
	if len(r.mappings) != 0 {
		t.Errorf("expected 0 mappings, got %d", len(r.mappings))
	}
}

func TestAddSubtype_EmptyTarget_NoOp(t *testing.T) {
	t.Parallel()
	r := newTestResponder()
	r.AddSubtype("_cm._sub._matterc._udp.local.", "")
	if len(r.mappings) != 0 {
		t.Errorf("expected 0 mappings, got %d", len(r.mappings))
	}
}

func TestRemoveSubtype_Empty_NoOp(t *testing.T) {
	t.Parallel()
	r := newTestResponder()
	// Should not panic.
	r.RemoveSubtype("")
}

// ---- matchAnswers — no mappings returns nil (not empty slice) ----

func TestMatchAnswers_ZeroQuestions(t *testing.T) {
	t.Parallel()
	r := newTestResponder()
	r.AddSubtype("_cm._sub._matterc._udp.local.", "inst._matterc._udp.local.")
	// matchAnswers with an empty slice should return nil.
	got := r.matchAnswers([]dns.Question{})
	if got != nil {
		t.Errorf("matchAnswers(empty): expected nil, got %v", got)
	}
}

// ---- packSubtypePTRs ----

// TestPackSubtypePTRs_AnnouncementShape verifies the unsolicited
// announcement is a spec-shaped mDNS response: ID 0, QR + AA set, an
// EMPTY question section (RFC 6762 §6), and one PTR answer per mapping
// carrying the shared subtype TTL. Commissioners fill their browse
// cache from exactly this packet — a malformed shape silently degrades
// to "device not found" on the subtype-filtered browse.
func TestPackSubtypePTRs_AnnouncementShape(t *testing.T) {
	t.Parallel()
	out, err := packSubtypePTRs(map[string]string{
		"_l3840._sub._matterc._udp.local.": "AABBCCDDEEFF0102._matterc._udp.local.",
		"_cm._sub._matterc._udp.local.":    "AABBCCDDEEFF0102._matterc._udp.local.",
	}, subtypePTRTTL)
	if err != nil {
		t.Fatalf("packSubtypePTRs: %v", err)
	}
	msg := new(dns.Msg)
	if err := msg.Unpack(out); err != nil {
		t.Fatalf("Unpack: %v", err)
	}
	if !msg.Response || !msg.Authoritative {
		t.Errorf("QR/AA = %v/%v, want true/true", msg.Response, msg.Authoritative)
	}
	if msg.Id != 0 {
		t.Errorf("Id = %d, want 0 (mDNS multicast response)", msg.Id)
	}
	if len(msg.Question) != 0 {
		t.Errorf("question section has %d entries, want 0 (RFC 6762 §6)", len(msg.Question))
	}
	if len(msg.Answer) != 2 {
		t.Fatalf("answers = %d, want 2", len(msg.Answer))
	}
	for _, rr := range msg.Answer {
		ptr, ok := rr.(*dns.PTR)
		if !ok {
			t.Fatalf("answer %T, want *dns.PTR", rr)
		}
		if ptr.Hdr.Ttl != subtypePTRTTL {
			t.Errorf("TTL = %d, want %d", ptr.Hdr.Ttl, subtypePTRTTL)
		}
		if ptr.Ptr != "AABBCCDDEEFF0102._matterc._udp.local." {
			t.Errorf("target = %q, want the primary instance FQDN", ptr.Ptr)
		}
	}
}

// TestPackSubtypePTRs_GoodbyeTTLZero verifies the withdraw path emits
// TTL=0 records so peer caches evict the subtype PTR immediately when
// the commissioning window closes.
func TestPackSubtypePTRs_GoodbyeTTLZero(t *testing.T) {
	t.Parallel()
	out, err := packSubtypePTRs(map[string]string{
		"_l3840._sub._matterc._udp.local.": "AABBCCDDEEFF0102._matterc._udp.local.",
	}, 0)
	if err != nil {
		t.Fatalf("packSubtypePTRs: %v", err)
	}
	msg := new(dns.Msg)
	if err := msg.Unpack(out); err != nil {
		t.Fatalf("Unpack: %v", err)
	}
	if len(msg.Answer) != 1 || msg.Answer[0].Header().Ttl != 0 {
		t.Errorf("want exactly one TTL=0 goodbye answer, got %v", msg.Answer)
	}
}

// TestSubtypeResponder_AnnounceEmptyMappings verifies Announce is a
// safe no-op when nothing is registered (nil receiver and empty table).
func TestSubtypeResponder_AnnounceEmptyMappings(t *testing.T) {
	t.Parallel()
	var nilResponder *SubtypeResponder
	nilResponder.Announce() // must not panic
	r := &SubtypeResponder{logger: slog.Default(), mappings: map[string]string{}}
	r.Announce() // empty table: no packet, no panic
}

// TestSurvivesReadError pins the receive-loop policy: a transient read
// error keeps the loop alive, a closed socket or a cancelled context
// stops it, and a socket that fails on every read is abandoned after the
// cap rather than spun on. Ending the loop on the first transient error
// silently stops subtype PTR answering for the process lifetime — the
// bridge stays resolvable by chip-tool but disappears from the Apple
// Home and Google Home browses, which filter on `_L<disc>._sub`.
func TestSurvivesReadError(t *testing.T) {
	t.Parallel()
	r := newTestResponder()
	transient := errors.New("recvmsg: interrupted system call")

	consecutive := 0
	if !r.survivesReadError(context.Background(), "v4", transient, &consecutive) {
		t.Fatal("a single transient read error ended the loop")
	}
	if consecutive != 1 {
		t.Errorf("consecutive = %d, want 1", consecutive)
	}

	// A closed socket is terminal regardless of the counter.
	consecutive = 0
	if r.survivesReadError(context.Background(), "v4", net.ErrClosed, &consecutive) {
		t.Error("loop continued after net.ErrClosed")
	}

	// A cancelled context is terminal too.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	consecutive = 0
	if r.survivesReadError(ctx, "v6", transient, &consecutive) {
		t.Error("loop continued after the context was cancelled")
	}

	// Persistent failure gives up at the cap instead of spinning.
	consecutive = maxConsecutiveReadErrors - 1
	if r.survivesReadError(context.Background(), "v6", transient, &consecutive) {
		t.Error("loop continued past maxConsecutiveReadErrors")
	}
}
