// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mdns

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/grandcat/zeroconf"

	"github.com/SukramJ/openccu-loom/internal/netutil"
)

// Zeroconf is an [Advertiser] backed by github.com/grandcat/zeroconf.
// It binds the bridge's `_matter._tcp` (operational) and
// `_matterc._udp` (commissionable) records to the host's multicast
// interfaces so chip-tool's `pairing onnetwork-*` discovery can find
// the bridge by service-type instead of needing an explicit
// `<ip> <port>` argument.
//
// Subtype records (e.g. `_L<discriminator>._sub._matterc._udp`) are
// emitted as additional zeroconf services under the
// `<subtype>._sub.<service-type>` synthesised name. The upstream
// library lacks a first-class subtype API, but registering
// independent servers per subtype produces the PTR records chip-tool
// queries during `pairing onnetwork-long`. The duplication wastes
// a small amount of bandwidth on Probe / Announce but is functionally
// equivalent — chip-tool resolves the instance via SRV/TXT against
// the primary registration regardless of which subtype matched.
//
// One zeroconf.Server is held per published (instance, service-type)
// pair plus one per subtype so Withdraw and Close can dismantle them
// independently. The library handles re-probing on link changes; no
// manual interface tracking required in v1.1.
type Zeroconf struct {
	mu      sync.RWMutex
	servers map[string]*zeroconf.Server
	items   map[string]Service
	// subFQDNs maps a primary key (instance|service-type) to the list
	// of `<sub>._sub.<service>` qnames currently registered with
	// [SubtypeResponder]. Withdraw / Close use this to remove the
	// matching mappings from the responder when the primary record
	// goes away.
	subFQDNs map[string][]string
	// responder is the side-car that answers PTR queries for service
	// subtypes (`_<sub>._sub.<service>.local.`). May be nil — when no
	// responder is wired the [Zeroconf] keeps publishing primaries
	// without subtype support, matching pre-1.1 behaviour.
	responder *SubtypeResponder
	// published maps a primary key to the fingerprint of the record
	// set last handed to the zeroconf library (TXT + port + host +
	// address list + subtypes). Publish skips the teardown + re-register
	// cycle when the fingerprint is unchanged: the library's Shutdown
	// emits TTL-0 goodbye packets, so an unconditional re-register on
	// the periodic re-announce made Apple flush and re-learn the whole
	// record set every interval. matter.js re-broadcasts records
	// without expiring them and sends goodbyes only on real unpublish
	// (MdnsAdvertisement.ts broadcast() vs expire()).
	published map[string]string
	closed    bool
	// afterSnapshot, when non-nil, runs between the snapshot of the
	// active keys and the re-announce in [Zeroconf.republishAll]. It is
	// the seam a test uses to land a Withdraw exactly in that gap —
	// the interleaving that decides whether a re-announce can resurrect
	// a record. Nil in production; set before any concurrent use.
	afterSnapshot func()
}

// NewZeroconf returns a multicast advertiser backed by zeroconf. The
// caller is responsible for calling [Zeroconf.Close] (typically
// during the daemon's shutdown sequence).
func NewZeroconf() *Zeroconf {
	return &Zeroconf{
		servers:   make(map[string]*zeroconf.Server),
		items:     make(map[string]Service),
		subFQDNs:  make(map[string][]string),
		published: make(map[string]string),
	}
}

// AttachSubtypeResponder wires a side-car responder that handles PTR
// queries for service subtypes — the form Apple Home and Google Home
// use to filter commissionable Matter bridges by long discriminator,
// short discriminator, vendor, and commissioning mode. Must be called
// before [Zeroconf.Publish] for the responder to receive the
// subtype-mappings of subsequent publishes.
//
// Attaching transfers the responder's shutdown to this advertiser:
// [Zeroconf.Close] closes it, releasing both receive goroutines and both
// multicast sockets.
func (z *Zeroconf) AttachSubtypeResponder(r *SubtypeResponder) {
	z.mu.Lock()
	z.responder = r
	z.mu.Unlock()
}

// hostIface is the testable shape of one network interface: its name,
// the flags the filter consults, and its unicast IPs. Decoupled from
// net.Interface so filterPrimaryHostIPs can be unit-tested without
// real interfaces.
type hostIface struct {
	name         string
	up           bool
	multicast    bool
	loopback     bool
	pointToPoint bool
	ips          []net.IP
}

// primaryHostIPs returns the curated host-IP list the Matter mDNS
// records should publish (see filterPrimaryHostIPs for the policy).
func primaryHostIPs() []string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	infos := make([]hostIface, 0, len(ifaces))
	for _, ifi := range ifaces {
		info := hostIface{
			name:         ifi.Name,
			up:           ifi.Flags&net.FlagUp != 0,
			multicast:    ifi.Flags&net.FlagMulticast != 0,
			loopback:     ifi.Flags&net.FlagLoopback != 0,
			pointToPoint: ifi.Flags&net.FlagPointToPoint != 0,
		}
		addrs, aerr := ifi.Addrs()
		if aerr != nil {
			continue
		}
		for _, a := range addrs {
			if ipn, ok := a.(*net.IPNet); ok {
				info.ips = append(info.ips, ipn.IP)
			}
		}
		infos = append(infos, info)
	}
	return filterPrimaryHostIPs(infos)
}

// parseHostIPs turns the advertised address strings back into net.IP so a
// published [Service] reports the same addresses the A/AAAA records carry.
func parseHostIPs(ips []string) []net.IP {
	if len(ips) == 0 {
		return nil
	}
	out := make([]net.IP, 0, len(ips))
	for _, s := range ips {
		if ip := net.ParseIP(s); ip != nil {
			out = append(out, ip)
		}
	}
	return out
}

// filterPrimaryHostIPs applies the advertise policy: the routable IPv4 +
// globally-routable IPv6 addresses from every non-loopback, non-tunnel,
// multicast-capable interface that is currently UP — excluding container /
// virtualisation bridges by interface name. Apple iOS Matter daemon
// iterates the published address list during resolve and aborts on
// unreachable addresses before trying the next — grandcat/zeroconf
// defaulted to emitting every IP on every interface (loopback, link-local
// duplicates, tunnels), which made pairing silently time out. Container
// bridges (docker0, hassio, br-<hex>, veth*, …) pass the flag checks —
// they are UP and multicast-capable — but their addresses are unroutable
// from LAN peers, so they are dropped by [netutil.IsVirtualInterfaceName],
// the same filter the client-discovery advertiser applies.
//
// Collecting from all valid NICs (multi-homed hosts have multiple
// physical or VLAN interfaces) ensures the bridge is reachable from
// any network segment. Duplicates are deduplicated by string value.
//
// Returns string form (RegisterProxy expects strings). IPv4 addresses
// are listed before IPv6 so commissioners that prefer IPv4 don't pay
// the cost of an IPv6 timeout first.
func filterPrimaryHostIPs(ifaces []hostIface) []string {
	seen := make(map[string]struct{})
	var v4s, v6s []string
	for _, ifi := range ifaces {
		if !ifi.up || !ifi.multicast || ifi.loopback || ifi.pointToPoint {
			continue
		}
		if netutil.IsVirtualInterfaceName(ifi.name) {
			continue
		}
		for _, ip := range ifi.ips {
			if ip == nil || ip.IsLoopback() {
				continue
			}
			s := ip.String()
			if _, dup := seen[s]; dup {
				continue
			}
			if four := ip.To4(); four != nil {
				if !four.IsLinkLocalUnicast() {
					seen[s] = struct{}{}
					v4s = append(v4s, four.String())
				}
				continue
			}
			if !ip.IsLinkLocalUnicast() && !ip.IsUnspecified() {
				seen[s] = struct{}{}
				v6s = append(v6s, s)
			}
		}
	}
	return append(v4s, v6s...)
}

// Publish implements [Advertiser]. Each publish call registers a
// fresh zeroconf.Server for the primary type plus one per
// [Service.Subtypes] entry; if the same instance/service-type pair is
// already published the prior server bundle is shut down first so
// re-publish produces a clean record (for example after a TXT-record
// bump). On any subtype-registration failure the primary is also
// torn down and the error is returned, so a partial publish never
// leaks half-registered records.
func (z *Zeroconf) Publish(_ context.Context, svc Service) error {
	if err := svc.Validate(); err != nil {
		return err
	}

	z.mu.Lock()
	defer z.mu.Unlock()
	return z.publishLocked(svc)
}

// publishLocked is [Zeroconf.Publish] minus validation and locking.
// Caller must hold z.mu for writing. Split out so [Zeroconf.republishAll]
// can re-check that a record is still wanted and re-register it in the
// same critical section — a Withdraw landing between the check and the
// register would otherwise be undone and the record would stay on the
// wire for the process lifetime.
func (z *Zeroconf) publishLocked(svc Service) error {
	if z.closed {
		return errors.New("mdns: zeroconf advertiser is closed")
	}
	key := noopKey(svc.InstanceName, svc.ServiceType)

	domain := svc.Domain
	if domain == "" {
		domain = "local."
	}
	txt := svc.MarshalTXT()
	// Default `zeroconf.Register` publishes A/AAAA records for every
	// IP on every interface — including loopback (`fe80::1`),
	// duplicates, and link-locals from disconnected interfaces. Apple
	// iOS Matter daemon iterates the address list and times out on
	// the unreachable ones before reaching the routable IPv4, so the
	// pairing handshake silently aborts in the resolve phase. Switch
	// to `RegisterProxy` with a curated address list — the primary
	// multicast interface only — so commissioners see a single
	// reachable IPv4 + (optionally) one global IPv6.
	// Hostname: a SRV target whose A/AAAA we publish ourselves on the
	// multicast wire. Defaulting to a bridge-specific name like
	// `openccu-loom-matter` only works if grandcat/zeroconf actually
	// emits A/AAAA for it; in practice macOS' mDNSResponder owns the
	// host's `<LocalHostName>.local` and our duplicate publish is
	// drowned out — Apple Home resolves the SRV target after
	// CommissioningComplete, finds nothing, and tears the fabric down
	// with RemoveFabric ~10s later. Falling back to `os.Hostname()`
	// reuses the OS-pinned name, which is guaranteed to have A/AAAA on
	// the wire for the lifetime of the host.
	host := svc.HostName
	if host == "" {
		if h, err := os.Hostname(); err == nil && h != "" {
			host = strings.TrimSuffix(h, ".local")
		} else {
			host = "openccu-loom-matter"
		}
	}
	ips := primaryHostIPs()
	// Stamp the effective address set onto the copy we keep: the A/AAAA
	// records carry primaryHostIPs(), never whatever the caller left in
	// Service.Addresses, and [Zeroconf.Active] is what the operator-facing
	// mDNS diagnostics inspect. Without the write-back a perfectly healthy
	// bridge reports "the record announces no IP address", and the
	// container-internal-address and missing-IPv6 checks — which exist to
	// catch a containerised daemon announcing unroutable addresses — can
	// never run at all.
	svc.Addresses = parseHostIPs(ips)
	// Skip the teardown + re-register cycle when nothing changed: the
	// library's Shutdown broadcasts TTL-0 goodbyes, so re-registering
	// an identical record set on every periodic re-announce made
	// Apple's cache flush + re-learn the bridge every interval. A real
	// change (TXT bump, port, host rename, address change, subtype
	// set) still re-registers — the goodbye is then correct. Mirrors
	// matter.js MdnsAdvertisement.ts: broadcast() re-announces without
	// expiring; expire() (goodbye) fires only on unpublish.
	fp := fmt.Sprintf("%s|%d|%s|%s|%s",
		strings.Join(txt, "\x1f"), svc.Port, host,
		strings.Join(ips, ","), strings.Join(svc.Subtypes, ","))
	if _, alive := z.servers[key]; alive && z.published[key] == fp {
		z.items[key] = svc
		// The record set is unchanged, but this is the periodic
		// re-announce path: refresh the subtype PTRs in peer caches.
		// A bare unsolicited announcement never flaps caches the way
		// the goodbye + re-register cycle (avoided above) would.
		z.announceSubtypes()
		return nil
	}
	z.shutdownByKeyLocked(key)
	server, err := zeroconf.RegisterProxy(
		svc.InstanceName,
		svc.ServiceType,
		domain,
		int(svc.Port),
		host,
		ips,
		txt,
		nil, // nil → all multicast-capable interfaces
	)
	if err != nil {
		return fmt.Errorf("mdns: zeroconf register %s/%s: %w", svc.InstanceName, svc.ServiceType, err)
	}
	z.servers[key] = server
	z.items[key] = svc
	z.published[key] = fp

	// Subtypes are emitted as `_<sub>._sub.<service>.local.` PTR
	// records pointing at the primary instance FQDN. Apple Home and
	// Google Home commissioners filter the browse via this exact form;
	// `grandcat/zeroconf` v1.0.0 has no first-class subtype API and
	// registering each subtype as a stand-alone service produces a
	// PTR with the wrong target. The side-car [SubtypeResponder]
	// handles these records directly so the upstream library keeps
	// owning SRV/TXT/A/AAAA for the primary.
	subFQDNs := make([]string, 0, len(svc.Subtypes))
	if z.responder != nil {
		primaryFQDN := svc.InstanceName + "." + svc.ServiceType
		if !strings.HasSuffix(primaryFQDN, ".") {
			primaryFQDN += "." + strings.TrimSuffix(domain, ".") + "."
		}
		for _, sub := range svc.Subtypes {
			if sub == "" {
				continue
			}
			subQName := sub + "._sub." + svc.ServiceType
			if !strings.HasSuffix(subQName, ".") {
				subQName += "." + strings.TrimSuffix(domain, ".") + "."
			}
			z.responder.AddSubtype(subQName, primaryFQDN)
			subFQDNs = append(subFQDNs, subQName)
		}
	}
	z.subFQDNs[key] = subFQDNs
	z.announceSubtypes()
	return nil
}

// announceSubtypes multicasts the registered subtype PTRs as an
// unsolicited response, twice with a one-second gap per RFC 6762 §8.3.
// Commissioners resolve the browse-by-discriminator filter
// (`_L<disc>._sub._matterc._udp`) from their mDNS cache, which only the
// announcement fills — grandcat/zeroconf announces the primary record
// set but knows nothing about the side-car's subtype PTRs, so without
// this the peer cache holds a resolvable instance that no subtype
// browse ever surfaces ("device not found" on a valid QR code). The
// second transmission runs detached so Publish stays non-blocking;
// Announce snapshots the mapping table per call, so a subtype withdrawn
// in the gap is simply absent from the repeat.
func (z *Zeroconf) announceSubtypes() {
	r := z.responder
	if r == nil {
		return
	}
	r.Announce()
	time.AfterFunc(time.Second, r.Announce)
}

// shutdownByKeyLocked tears down the primary server identified by
// key plus every subtype mapping registered alongside it. Caller
// must hold z.mu.
func (z *Zeroconf) shutdownByKeyLocked(key string) {
	if prev, ok := z.servers[key]; ok {
		prev.Shutdown()
		delete(z.servers, key)
	}
	if z.responder != nil {
		for _, qname := range z.subFQDNs[key] {
			z.responder.RemoveSubtype(qname)
		}
	}
	delete(z.subFQDNs, key)
	delete(z.published, key)
}

// Withdraw implements [Advertiser]. The library emits goodbye packets
// (TTL=0) on Shutdown so peers learn the service vanished without
// waiting for the TTL to elapse. Subtypes registered alongside the
// primary are also withdrawn.
func (z *Zeroconf) Withdraw(_ context.Context, instanceName, serviceType string) error {
	z.mu.Lock()
	defer z.mu.Unlock()
	key := noopKey(instanceName, serviceType)
	if _, ok := z.servers[key]; !ok {
		return fmt.Errorf("%w: %s/%s", ErrServiceNotFound, instanceName, serviceType)
	}
	z.shutdownByKeyLocked(key)
	delete(z.items, key)
	return nil
}

// Active implements [Advertiser].
func (z *Zeroconf) Active() []Service {
	z.mu.RLock()
	defer z.mu.RUnlock()
	out := make([]Service, 0, len(z.items))
	for k := range z.items {
		out = append(out, z.items[k])
	}
	return out
}

// TriggerReannounce performs an immediate re-publish of every active
// service outside the periodic tick interval. Intended for use on
// network-change events (interface up/down, address assigned) where
// waiting for the next periodic tick would leave the bridge invisible
// to commissioners for up to the full interval. Safe to call from any
// goroutine; the underlying republishAll is concurrency-safe.
func (z *Zeroconf) TriggerReannounce(ctx context.Context) {
	z.republishAll(ctx)
}

// StartReannounceLoop spawns a background goroutine that re-publishes
// every active service every `interval` and also responds to
// TriggerReannounce calls on the returned trigger channel. mDNS
// responder libraries like grandcat/zeroconf only Probe + Announce on
// initial Register; commissioners that miss the announce window
// (transient network loss, late join, mDNS cache eviction past TTL)
// never see the bridge until it re-announces.
//
// The returned trigger channel accepts a single value; sending to it
// causes an immediate re-publish before the next periodic tick. The
// channel is buffered (capacity 1) so a burst of events collapses into
// one re-announce. Callers that do not use event-driven triggering may
// ignore the channel — the periodic cadence still applies.
//
// Returns a (cancel func, trigger chan). Idempotent — calling twice
// silently replaces the prior loop.
func (z *Zeroconf) StartReannounceLoop(ctx context.Context, interval time.Duration) (cancel func(), trigger chan<- struct{}) {
	triggerCh := make(chan struct{}, 1)
	stopCh := make(chan struct{})
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-stopCh:
				return
			case <-ctx.Done():
				return
			case <-triggerCh:
				z.republishAll(ctx)
			case <-ticker.C:
				z.republishAll(ctx)
			}
		}
	}()
	return func() { close(stopCh) }, triggerCh
}

// republishAll snapshots the current Service set and re-Publishes
// each entry. Republish replaces the prior server bundle (see
// Publish docstring), so the wire effect is a Probe + Announce burst
// for every active service.
func (z *Zeroconf) republishAll(_ context.Context) {
	keys := z.snapshotKeys()
	if z.afterSnapshot != nil {
		z.afterSnapshot()
	}
	z.republishKeys(keys)
}

// snapshotKeys returns the keys of the currently active services, or
// nil once the advertiser is closed.
func (z *Zeroconf) snapshotKeys() []string {
	z.mu.RLock()
	defer z.mu.RUnlock()
	if z.closed {
		return nil
	}
	keys := make([]string, 0, len(z.items))
	for k := range z.items {
		keys = append(keys, k)
	}
	return keys
}

// republishKeys re-announces the named services. The Service value is
// re-read from z.items under the write lock and republished in the same
// critical section, so a key that was withdrawn between the snapshot and
// its turn here is skipped.
//
// Republishing from a snapshot of Service VALUES instead resurrects a
// record that was withdrawn in the meantime — Publish re-registers it and
// nothing withdraws it a second time, so a removed fabric would keep
// answering commissioner browses until the daemon restarts. Splitting the
// snapshot from the republish keeps that gap explicit (and testable)
// rather than implicit in one long function.
func (z *Zeroconf) republishKeys(keys []string) {
	for _, k := range keys {
		z.mu.Lock()
		svc, ok := z.items[k]
		if !ok || z.closed {
			z.mu.Unlock()
			continue
		}
		_ = z.publishLocked(svc)
		z.mu.Unlock()
	}
}

// Close implements [Advertiser]. Idempotent — repeated calls return nil.
func (z *Zeroconf) Close() error {
	z.mu.Lock()
	defer z.mu.Unlock()
	if z.closed {
		return nil
	}
	for k, server := range z.servers {
		server.Shutdown()
		delete(z.servers, k)
	}
	for k := range z.items {
		delete(z.items, k)
	}
	for k, qnames := range z.subFQDNs {
		if z.responder != nil {
			for _, q := range qnames {
				z.responder.RemoveSubtype(q)
			}
		}
		delete(z.subFQDNs, k)
	}
	// The side-car responder's receive goroutines and its two
	// multicast :5353 sockets are bound to the advertiser's lifetime —
	// attaching one hands its shutdown to us. Without this the loops and
	// the group memberships outlive every Matter teardown.
	var err error
	if z.responder != nil {
		err = z.responder.Close()
		z.responder = nil
	}
	z.closed = true
	return err
}
