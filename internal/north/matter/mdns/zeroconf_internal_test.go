// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// White-box tests for zeroconf.go paths that require internal access.
// We use package mdns so we can call newTestResponder() and primaryHostIPs().

package mdns

import (
	"context"
	"log/slog"
	"net"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/miekg/dns"
)

// ---- primaryHostIPs ----

// TestPrimaryHostIPs_ReturnsNonNilSlice verifies primaryHostIPs does not
// panic and returns either nil (no suitable interface) or at least one
// address. We cannot assert the exact result because the host's network
// configuration varies.
func TestPrimaryHostIPs_ReturnsNonNilSlice(t *testing.T) {
	t.Parallel()
	ips := primaryHostIPs()
	// Must not panic. Result may be nil on a loopback-only environment.
	for _, ip := range ips {
		if ip == "" {
			t.Error("primaryHostIPs returned empty string IP")
		}
	}
}

// ---- filterPrimaryHostIPs ----

// TestFilterPrimaryHostIPs verifies the advertise policy documented on
// filterPrimaryHostIPs: container/virtualisation bridges are dropped by
// name, down/non-multicast/loopback/point-to-point interfaces are dropped
// by flag, IPv4 sorts before IPv6, duplicates are deduplicated, and
// link-local addresses (both families) are excluded while global IPv6
// survives.
func TestFilterPrimaryHostIPs(t *testing.T) {
	t.Parallel()
	ip := net.ParseIP

	tests := []struct {
		name   string
		ifaces []hostIface
		want   []string
	}{
		{
			name: "container bridges dropped, real LAN interface kept",
			ifaces: []hostIface{
				{name: "docker0", up: true, multicast: true, ips: []net.IP{ip("172.17.0.1")}},
				{name: "hassio", up: true, multicast: true, ips: []net.IP{ip("172.30.32.1")}},
				{name: "br-1a2b3c4d", up: true, multicast: true, ips: []net.IP{ip("172.18.0.1")}},
				{name: "eth0", up: true, multicast: true, ips: []net.IP{ip("192.168.1.10")}},
			},
			want: []string{"192.168.1.10"},
		},
		{
			name: "down interface dropped",
			ifaces: []hostIface{
				{name: "eth0", up: false, multicast: true, ips: []net.IP{ip("192.168.1.10")}},
			},
			want: nil,
		},
		{
			name: "non-multicast interface dropped",
			ifaces: []hostIface{
				{name: "eth0", up: true, multicast: false, ips: []net.IP{ip("192.168.1.10")}},
			},
			want: nil,
		},
		{
			name: "loopback interface dropped",
			ifaces: []hostIface{
				{name: "lo", up: true, multicast: true, loopback: true, ips: []net.IP{ip("127.0.0.1")}},
			},
			want: nil,
		},
		{
			name: "point-to-point interface dropped",
			ifaces: []hostIface{
				{name: "tun0", up: true, multicast: true, pointToPoint: true, ips: []net.IP{ip("10.8.0.2")}},
			},
			want: nil,
		},
		{
			name: "IPv4 sorted before IPv6",
			ifaces: []hostIface{
				{name: "eth0", up: true, multicast: true, ips: []net.IP{ip("2001:db8::1"), ip("192.168.1.10")}},
			},
			want: []string{"192.168.1.10", "2001:db8::1"},
		},
		{
			name: "duplicate IPs across interfaces are deduplicated",
			ifaces: []hostIface{
				{name: "eth0", up: true, multicast: true, ips: []net.IP{ip("192.168.1.10")}},
				{name: "eth0:1", up: true, multicast: true, ips: []net.IP{ip("192.168.1.10")}},
			},
			want: []string{"192.168.1.10"},
		},
		{
			name: "IPv4 and IPv6 link-local dropped, global IPv6 kept",
			ifaces: []hostIface{
				{name: "eth0", up: true, multicast: true, ips: []net.IP{
					ip("169.254.1.1"),
					ip("fe80::1"),
					ip("192.168.1.10"),
					ip("2001:db8::1"),
				}},
			},
			want: []string{"192.168.1.10", "2001:db8::1"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := filterPrimaryHostIPs(tc.ifaces)
			if !slices.Equal(got, tc.want) {
				t.Errorf("filterPrimaryHostIPs() = %v, want %v", got, tc.want)
			}
		})
	}
}

// ---- Publish with z.responder != nil ----

// TestZeroconfInternal_Publish_WithResponder verifies the subtype-mapping
// block inside Publish is exercised when a non-nil SubtypeResponder is
// attached. Uses the white-box newTestResponder (no sockets) so the test
// does not require multicast.
func TestZeroconfInternal_Publish_WithResponder(t *testing.T) {
	t.Parallel()
	z := NewZeroconf()
	t.Cleanup(func() { _ = z.Close() })

	// Attach a socket-less responder (white-box).
	r := newTestResponder()
	z.AttachSubtypeResponder(r)

	svc := Service{
		InstanceName: "AABBCCDDEEFF1122",
		ServiceType:  ServiceTypeCommissionable,
		Port:         5540,
		HostName:     "test",
		Subtypes:     []string{"_L3840", "_CM", "_S15"},
	}
	if err := z.Publish(context.Background(), svc); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	// Verify the subtype mappings were registered with the responder.
	// We probe via matchAnswers — a PTR query for any registered subtype should resolve.
	if len(r.mappings) == 0 {
		t.Fatal("responder has no subtype mappings after Publish — subtype block not exercised")
	}
	// Spot-check one subtype lookup.
	var firstKey string
	for k := range r.mappings {
		firstKey = k
		break
	}
	qs := []dns.Question{{Name: firstKey, Qtype: dns.TypePTR}}
	answers := r.matchAnswers(qs)
	if len(answers) == 0 {
		t.Errorf("matchAnswers for %q returned nothing — mapping not stored correctly", firstKey)
	}
}

// TestZeroconfInternal_Publish_WithResponder_EmptySubtype verifies that a
// subtype label that is an empty string is skipped (the `sub == ""` guard).
func TestZeroconfInternal_Publish_WithResponder_EmptySubtype(t *testing.T) {
	t.Parallel()
	z := NewZeroconf()
	t.Cleanup(func() { _ = z.Close() })

	r := newTestResponder()
	z.AttachSubtypeResponder(r)

	svc := Service{
		InstanceName: "DEADBEEF01234567",
		ServiceType:  ServiceTypeCommissionable,
		Port:         5540,
		HostName:     "test",
		// One empty subtype that should be skipped, one valid one.
		Subtypes: []string{"", "_CM"},
	}
	if err := z.Publish(context.Background(), svc); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	// Only "_CM" should be registered; "" must be skipped.
	if len(r.mappings) != 1 {
		t.Errorf("expected 1 mapping (empty subtype skipped), got %d", len(r.mappings))
	}
}

// TestZeroconfInternal_Publish_WithResponder_Domain verifies the subtype
// qname construction when the service has a non-empty Domain.
func TestZeroconfInternal_Publish_WithResponder_Domain(t *testing.T) {
	t.Parallel()
	z := NewZeroconf()
	t.Cleanup(func() { _ = z.Close() })

	r := newTestResponder()
	z.AttachSubtypeResponder(r)

	svc := Service{
		InstanceName: "1234567890ABCDEF",
		ServiceType:  ServiceTypeOperational,
		Port:         5540,
		HostName:     "test",
		Domain:       "local",
		Subtypes:     []string{"_I9C71D38FBE48F2E5"},
	}
	if err := z.Publish(context.Background(), svc); err != nil {
		t.Fatalf("Publish with domain: %v", err)
	}
	if len(r.mappings) != 1 {
		t.Errorf("expected 1 subtype mapping, got %d", len(r.mappings))
	}
}

// TestZeroconfInternal_Publish_UnchangedSkipsReRegister verifies the
// re-announce dedup: publishing the same record set twice keeps the
// SAME underlying zeroconf.Server (no teardown → no TTL-0 goodbye →
// no Apple cache flush), while a changed record set (TXT bump)
// re-registers a fresh server.
func TestZeroconfInternal_Publish_UnchangedSkipsReRegister(t *testing.T) {
	t.Parallel()
	z := NewZeroconf()
	t.Cleanup(func() { _ = z.Close() })

	svc := Service{
		InstanceName: "AABBCCDD00112233",
		ServiceType:  ServiceTypeOperational,
		Port:         5540,
		HostName:     "test",
		TXT:          []TXTRecord{{Key: "SII", Value: "500"}},
	}
	if err := z.Publish(context.Background(), svc); err != nil {
		t.Fatalf("first Publish: %v", err)
	}
	key := noopKey(svc.InstanceName, svc.ServiceType)
	z.mu.RLock()
	first := z.servers[key]
	firstFP := z.published[key]
	z.mu.RUnlock()
	if first == nil {
		t.Fatal("no server registered after first Publish")
	}

	// Identical re-publish → same server instance, same fingerprint.
	if err := z.Publish(context.Background(), svc); err != nil {
		t.Fatalf("identical re-Publish: %v", err)
	}
	z.mu.RLock()
	second := z.servers[key]
	secondFP := z.published[key]
	z.mu.RUnlock()
	if second != first {
		t.Error("identical re-Publish replaced the server (would emit a TTL-0 goodbye + re-register)")
	}
	if secondFP != firstFP {
		t.Errorf("fingerprint changed on identical re-Publish: %q → %q", firstFP, secondFP)
	}

	// Changed TXT → fresh server (re-register is correct here).
	changed := svc
	changed.TXT = []TXTRecord{{Key: "SII", Value: "300"}}
	if err := z.Publish(context.Background(), changed); err != nil {
		t.Fatalf("changed re-Publish: %v", err)
	}
	z.mu.RLock()
	third := z.servers[key]
	thirdFP := z.published[key]
	z.mu.RUnlock()
	if third == first {
		t.Error("changed re-Publish did NOT re-register — the stale record would keep answering")
	}
	if thirdFP == firstFP {
		t.Error("fingerprint unchanged after a TXT change")
	}
}

// TestZeroconfInternal_Close_WithResponder_SubFQDNs verifies that Close
// removes subtype mappings from the attached responder when there are
// registered subFQDNs. This exercises the
// `z.responder != nil` branch inside Close.
func TestZeroconfInternal_Close_WithResponder_SubFQDNs(t *testing.T) {
	t.Parallel()
	z := NewZeroconf()

	r := newTestResponder()
	z.AttachSubtypeResponder(r)

	svc := Service{
		InstanceName: "FFFF000011112222",
		ServiceType:  ServiceTypeCommissionable,
		Port:         5540,
		HostName:     "test",
		Subtypes:     []string{"_CM", "_L3840"},
	}
	if err := z.Publish(context.Background(), svc); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if len(r.mappings) == 0 {
		t.Fatal("responder has no mappings — precondition not met")
	}

	// Close must remove the subtype mappings from the responder.
	if err := z.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if len(r.mappings) != 0 {
		t.Errorf("responder mappings not cleared after Close: %d remaining", len(r.mappings))
	}
}

// TestZeroconfInternal_Withdraw_WithResponder_SubFQDNs verifies that Withdraw
// removes subtype mappings from the attached responder via shutdownByKeyLocked.
func TestZeroconfInternal_Withdraw_WithResponder_SubFQDNs(t *testing.T) {
	t.Parallel()
	z := NewZeroconf()
	t.Cleanup(func() { _ = z.Close() })

	r := newTestResponder()
	z.AttachSubtypeResponder(r)

	svc := Service{
		InstanceName: "AAAA000011113333",
		ServiceType:  ServiceTypeCommissionable,
		Port:         5540,
		HostName:     "test",
		Subtypes:     []string{"_CM"},
	}
	if err := z.Publish(context.Background(), svc); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	before := len(r.mappings)
	if before == 0 {
		t.Fatal("responder has no mappings after Publish — precondition failed")
	}

	if err := z.Withdraw(context.Background(), svc.InstanceName, svc.ServiceType); err != nil {
		t.Fatalf("Withdraw: %v", err)
	}
	if len(r.mappings) != 0 {
		t.Errorf("responder mappings not cleared after Withdraw: %d remaining", len(r.mappings))
	}
}

// TestZeroconfInternal_RepublishAll_WithItems exercises the republishAll path
// that re-Publishes each active item. Specifically the `z.closed == false`
// branch and the snapshot loop.
func TestZeroconfInternal_RepublishAll_WithItems(t *testing.T) {
	t.Parallel()
	z := NewZeroconf()
	t.Cleanup(func() { _ = z.Close() })

	svc := Service{
		InstanceName: "BBBBBBBBCCCCCCCC",
		ServiceType:  ServiceTypeOperational,
		Port:         5540,
		HostName:     "test",
	}
	if err := z.Publish(context.Background(), svc); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	loop, _ := z.StartReannounceLoop(ctx, 30*time.Millisecond)
	defer loop()

	time.Sleep(100 * time.Millisecond)
	cancel()

	if got := z.Active(); len(got) != 1 {
		t.Errorf("Active after reannounce: got %d, want 1", len(got))
	}
}

// TestZeroconfInternal_Publish_HostName_Empty_FallsBack verifies that an
// empty HostName in the Service causes Publish to fall back to os.Hostname()
// rather than crashing. This exercises the `host == ""` branch in Publish.
func TestZeroconfInternal_Publish_HostName_Empty_FallsBack(t *testing.T) {
	t.Parallel()
	z := NewZeroconf()
	t.Cleanup(func() { _ = z.Close() })

	svc := Service{
		InstanceName: "EEEEEEEEFFFFFFFF",
		ServiceType:  ServiceTypeOperational,
		Port:         5540,
		HostName:     "", // triggers os.Hostname() fallback
	}
	// Publish must not panic. It may succeed or fail depending on whether
	// zeroconf.RegisterProxy accepts the OS hostname; we only care that
	// the empty-hostname branch is reached without a nil-dereference.
	_ = z.Publish(context.Background(), svc)
}

// TestZeroconfInternal_NewSubtypeResponder_WithLogger_NoNil exercises the
// NewSubtypeResponder path where logger is non-nil. Also exercises
// the debug-log branches in joinMcast4/joinMcast6 when an interface
// is available.
func TestZeroconfInternal_NewSubtypeResponder_Logger_NonNil(t *testing.T) {
	// Build a custom logger to be explicit.
	logger := slog.Default()
	r, err := NewSubtypeResponder(logger)
	if err != nil {
		// Not a test failure — on a restricted sandbox joinMcast might fail.
		t.Logf("NewSubtypeResponder with non-nil logger failed (acceptable): %v", err)
		return
	}
	if cerr := r.Close(); cerr != nil {
		t.Fatalf("Close: %v", cerr)
	}
}

// TestZeroconfInternal_Publish_RecordsAdvertisedAddresses verifies the
// published record reports the addresses that actually went on the wire.
// Production callers build their service configs without addresses — the
// advertiser resolves them itself — so an address-less stored record makes
// the operator-facing mDNS diagnostics report "no IP address" on a healthy
// bridge and silently disables the container-internal and IPv6 checks that
// only run once an address is present.
func TestZeroconfInternal_Publish_RecordsAdvertisedAddresses(t *testing.T) {
	t.Parallel()
	want := primaryHostIPs()
	if len(want) == 0 {
		t.Skip("host advertises no routable address; nothing to assert")
	}

	z := NewZeroconf()
	t.Cleanup(func() { _ = z.Close() })

	// The shape production builds: BuildOperationalService copies an
	// Addresses field its callers never fill.
	svc := Service{
		InstanceName: "1122334455667788-0000000000001234",
		ServiceType:  ServiceTypeOperational,
		Port:         5540,
		HostName:     "advertised-address-test",
	}
	if err := z.Publish(context.Background(), svc); err != nil {
		t.Skipf("Publish failed (no multicast in this environment): %v", err)
	}

	active := z.Active()
	if len(active) != 1 {
		t.Fatalf("Active() returned %d services, want 1", len(active))
	}
	got := make([]string, 0, len(active[0].Addresses))
	for _, ip := range active[0].Addresses {
		got = append(got, ip.String())
	}
	if !slices.Equal(got, want) {
		t.Errorf("Active() addresses = %v, want the advertised set %v", got, want)
	}

	for _, f := range Diagnose(active) {
		if f.Code == "no_addresses" {
			t.Errorf("diagnostics report %q for a record that advertises %v", f.Code, want)
		}
	}
}

// TestZeroconfInternal_RepublishAllDoesNotResurrectWithdrawn pins that a
// Withdraw racing the periodic re-announce stays withdrawn. republishAll
// used to snapshot the record set and then re-Publish from that copy, so
// a Withdraw landing in the gap was undone — the record stayed on the
// wire for the process lifetime and commissioners kept resolving a dead
// identity. The gap is reproduced deterministically here by driving the
// snapshot and the re-announce as two steps. The invariant checked is
// structural: every registered server must still have an entry in
// z.items.
func TestZeroconfInternal_RepublishAllDoesNotResurrectWithdrawn(t *testing.T) {
	z := NewZeroconf()
	t.Cleanup(func() { _ = z.Close() })
	r := newTestResponder()
	z.AttachSubtypeResponder(r)

	ctx := context.Background()
	keep := Service{
		InstanceName: "CCCC000011112222",
		ServiceType:  ServiceTypeOperational,
		Port:         5540,
		HostName:     "test",
	}
	doomed := Service{
		InstanceName: "DDDD000011112222",
		ServiceType:  ServiceTypeCommissionable,
		Port:         5540,
		HostName:     "test",
		Subtypes:     []string{"_CM"},
	}
	for _, svc := range []Service{keep, doomed} {
		if err := z.Publish(ctx, svc); err != nil {
			t.Fatalf("Publish %s: %v", svc.InstanceName, err)
		}
	}

	// Land the Withdraw exactly in the gap between the snapshot and the
	// re-announce — the one interleaving that reproduces the defect.
	// Racing a goroutine against Withdraw instead makes the test a coin
	// flip: a Withdraw that lands after both re-publishes leaves the
	// record withdrawn even with the defect present.
	withdrawn := false
	z.afterSnapshot = func() {
		if withdrawn {
			return
		}
		withdrawn = true
		if err := z.Withdraw(ctx, doomed.InstanceName, doomed.ServiceType); err != nil {
			t.Errorf("Withdraw: %v", err)
		}
	}
	z.republishAll(ctx)
	if !withdrawn {
		t.Fatal("the re-announce never reached its snapshot gap; the test did not exercise the race it names")
	}

	z.mu.RLock()
	defer z.mu.RUnlock()
	doomedKey := noopKey(doomed.InstanceName, doomed.ServiceType)
	if _, ok := z.items[doomedKey]; ok {
		t.Error("withdrawn service is back in items after republishAll")
	}
	for key := range z.servers {
		if _, ok := z.items[key]; !ok {
			t.Errorf("server %q survives with no item — the record was resurrected by republishAll", key)
		}
	}
}

// TestZeroconfInternal_CloseClosesAttachedResponder pins that attaching a
// side-car responder hands its shutdown to the advertiser: without it the
// responder's receive goroutines and its two multicast :5353 sockets
// outlive every Matter teardown.
func TestZeroconfInternal_CloseClosesAttachedResponder(t *testing.T) {
	t.Parallel()
	z := NewZeroconf()
	r := newTestResponder()
	z.AttachSubtypeResponder(r)
	r.Start(context.Background())

	if err := z.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if r.cancel != nil {
		t.Error("responder still running after Zeroconf.Close — its goroutines outlive the advertiser")
	}
	z.mu.RLock()
	defer z.mu.RUnlock()
	if z.responder != nil {
		t.Error("responder reference retained after Close")
	}
}

// TestZeroconfInternal_ResponderCloseIsIdempotentUnderTwoOwners pins that
// closing an attached responder twice, concurrently, is safe. The daemon
// keeps its own close func for the responder it constructed while the
// advertiser closes the same responder on teardown, so the second Close
// is normal operation rather than a bug — but only if it neither races
// the first on the cancel func and packet conns nor reports an error.
func TestZeroconfInternal_ResponderCloseIsIdempotentUnderTwoOwners(t *testing.T) {
	t.Parallel()
	z := NewZeroconf()
	r := newTestResponder()
	z.AttachSubtypeResponder(r)
	r.Start(context.Background())

	var wg sync.WaitGroup
	errs := make([]error, 2)
	wg.Add(2)
	go func() { defer wg.Done(); errs[0] = z.Close() }()
	go func() { defer wg.Done(); errs[1] = r.Close() }()
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("close %d returned %v, want nil — a second owner closing the responder must be a no-op", i, err)
		}
	}
	if err := r.Close(); err != nil {
		t.Errorf("third Close returned %v, want nil", err)
	}
	// The shutdown must have run exactly once. This package cannot run
	// under -race (see TestMain), so the serialisation itself is pinned
	// structurally: `closed` is the flag that turns every later Close
	// into a no-op instead of a second, unsynchronised teardown.
	r.lifecycleMu.Lock()
	defer r.lifecycleMu.Unlock()
	if !r.closed {
		t.Error("responder not marked closed; a later Close would tear the sockets down a second time")
	}
	if r.cancel != nil {
		t.Error("responder still running after Close — its goroutines outlive both owners")
	}
}
