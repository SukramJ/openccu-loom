// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mdns

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/miekg/dns"
	"golang.org/x/net/ipv4"
	"golang.org/x/net/ipv6"
)

// SubtypeResponder is a side-car mDNS responder that serves PTR
// queries for service subtypes (`_<sub>._sub.<service>.local.`) with
// a PTR record pointing at the primary instance — the form Apple Home
// and Google Home rely on when filtering commissionable Matter
// bridges by long discriminator (`_L<long>`), short discriminator
// (`_S<short>`), commissioning mode (`_CM`) or vendor (`_V<vid>`).
//
// `grandcat/zeroconf` lacks a first-class subtype API; emitting each
// subtype as its own service registers PTRs of the form
// `_<sub>._sub.<service> PTR <instance>._<sub>._sub.<service>` instead
// of `_<sub>._sub.<service> PTR <instance>.<service>`. Apple's
// commissioner browse hits the wrong target, sees no SRV with the
// expected name, and gives up with "device not found".
//
// This responder fixes that without forking the upstream library: it
// joins the mDNS multicast groups (224.0.0.251, ff02::fb) on UDP/5353
// alongside zeroconf via `SO_REUSEADDR`-style multicast wildcard bind
// and answers only the PTR queries it has explicit mappings for.
// Anything else is ignored — the upstream library still owns the
// SRV/TXT/A/AAAA chain.
type SubtypeResponder struct {
	logger *slog.Logger

	pc4    *ipv4.PacketConn
	pc6    *ipv6.PacketConn
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu sync.RWMutex
	// mappings is a lower-cased qname (`_l3840._sub._matterc._udp.local.`)
	// → primary instance FQDN
	// (`f9732c2a304348b3._matterc._udp.local.`).
	mappings map[string]string
}

// NewSubtypeResponder binds the responder to the mDNS multicast
// groups on every multicast-capable interface. Returns an error if
// neither IPv4 nor IPv6 could be joined; either alone is sufficient
// for normal operation (Apple Home pairs over both).
//
// The caller owns the responder lifecycle: call [SubtypeResponder.Start]
// to spin up the receive loops and [SubtypeResponder.Close] during
// shutdown.
func NewSubtypeResponder(logger *slog.Logger) (*SubtypeResponder, error) {
	if logger == nil {
		logger = slog.Default()
	}
	r := &SubtypeResponder{
		logger:   logger,
		mappings: make(map[string]string),
	}

	pc4, err4 := joinMcast4()
	if err4 != nil {
		logger.Debug("matter.mdns.subtype.udp4_join_failed", slog.String("err", err4.Error()))
	} else {
		r.pc4 = pc4
	}
	pc6, err6 := joinMcast6()
	if err6 != nil {
		logger.Debug("matter.mdns.subtype.udp6_join_failed", slog.String("err", err6.Error()))
	} else {
		r.pc6 = pc6
	}
	if r.pc4 == nil && r.pc6 == nil {
		return nil, fmt.Errorf("mdns subtype: failed to bind both udp4 and udp6: %w / %w", err4, err6)
	}
	return r, nil
}

// AddSubtype registers a `_<sub>._sub.<service>.local.` →
// `<instance>.<service>.local.` mapping. Idempotent. Lowercases the
// qname for case-insensitive lookup.
func (r *SubtypeResponder) AddSubtype(subType, primaryFQDN string) {
	if r == nil || subType == "" || primaryFQDN == "" {
		return
	}
	q := strings.ToLower(ensureTrailingDot(subType))
	target := ensureTrailingDot(primaryFQDN)
	r.mu.Lock()
	r.mappings[q] = target
	r.mu.Unlock()
}

// RemoveSubtype drops a previously registered mapping.
func (r *SubtypeResponder) RemoveSubtype(subType string) {
	if r == nil || subType == "" {
		return
	}
	q := strings.ToLower(ensureTrailingDot(subType))
	r.mu.Lock()
	delete(r.mappings, q)
	r.mu.Unlock()
}

// Start begins the receive loops. Idempotent — repeated calls are no-ops.
func (r *SubtypeResponder) Start(ctx context.Context) {
	if r == nil || r.cancel != nil {
		return
	}
	cctx, cancel := context.WithCancel(ctx)
	r.cancel = cancel
	if r.pc4 != nil {
		r.wg.Add(1)
		go r.serveV4(cctx)
	}
	if r.pc6 != nil {
		r.wg.Add(1)
		go r.serveV6(cctx)
	}
	r.logger.Info("matter.mdns.subtype.started",
		slog.Bool("ipv4", r.pc4 != nil),
		slog.Bool("ipv6", r.pc6 != nil))
}

// Close cancels the receive loops and releases the multicast sockets.
func (r *SubtypeResponder) Close() error {
	if r == nil {
		return nil
	}
	if r.cancel != nil {
		r.cancel()
		r.cancel = nil
	}
	r.wg.Wait()
	var err error
	if r.pc4 != nil {
		if e := r.pc4.Close(); e != nil {
			err = errors.Join(err, e)
		}
		r.pc4 = nil
	}
	if r.pc6 != nil {
		if e := r.pc6.Close(); e != nil {
			err = errors.Join(err, e)
		}
		r.pc6 = nil
	}
	return err
}

// serveV4 / serveV6 run the receive loops. Each ReadFrom is timed out
// every second so a closed responder can wind down without hanging
// on a multicast socket that may never receive another packet.
//
// The receive ControlMessage is preserved through to the responder so
// we can send the reply back on the same interface — multicast send
// fails with "no route to host" when no outgoing interface is bound,
// which is the default on macOS for sockets opened on `[::]`.
func (r *SubtypeResponder) serveV4(ctx context.Context) {
	defer r.wg.Done()
	buf := make([]byte, 9000) // mDNS messages cap below this; larger frame is overkill but cheap.
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		_ = r.pc4.SetReadDeadline(time.Now().Add(time.Second))
		n, cm, src, err := r.pc4.ReadFrom(buf)
		if err != nil {
			if isTimeout(err) {
				continue
			}
			r.logger.Debug("matter.mdns.subtype.read4_err", slog.String("err", err.Error()))
			return
		}
		r.handleV4(buf[:n], cm, src)
	}
}

func (r *SubtypeResponder) serveV6(ctx context.Context) {
	defer r.wg.Done()
	buf := make([]byte, 9000)
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		_ = r.pc6.SetReadDeadline(time.Now().Add(time.Second))
		n, cm, src, err := r.pc6.ReadFrom(buf)
		if err != nil {
			if isTimeout(err) {
				continue
			}
			r.logger.Debug("matter.mdns.subtype.read6_err", slog.String("err", err.Error()))
			return
		}
		r.handleV6(buf[:n], cm, src)
	}
}

func (r *SubtypeResponder) handleV4(buf []byte, cm *ipv4.ControlMessage, src net.Addr) {
	out, ok := r.buildReply(buf)
	if !ok || r.pc4 == nil {
		return
	}
	dst := &net.UDPAddr{IP: net.IPv4(224, 0, 0, 251), Port: 5353}
	r.writeMulticastV4(out, cm, dst)
	_ = src
}

func (r *SubtypeResponder) handleV6(buf []byte, cm *ipv6.ControlMessage, src net.Addr) {
	out, ok := r.buildReply(buf)
	if !ok || r.pc6 == nil {
		return
	}
	dst := &net.UDPAddr{IP: net.ParseIP("ff02::fb"), Port: 5353}
	r.writeMulticastV6(out, cm, dst)
	_ = src
}

// writeMulticastV4 prefers the incoming-packet interface (so the
// reply lands on the same network the query arrived on) but falls
// back to fan-out across every multicast-capable interface when the
// incoming control message had no usable IfIndex — which happens when
// the kernel did not annotate the packet (`net.UnixListenPacketUnix`
// vs `socket(2) IP_RECVIF`-style behaviour) or when the source was
// loopback. Without a fallback we silently drop replies on macOS
// where unbound multicast sends fail with "network is unreachable".
func (r *SubtypeResponder) writeMulticastV4(out []byte, cm *ipv4.ControlMessage, dst *net.UDPAddr) {
	tried := false
	if cm != nil && cm.IfIndex > 0 {
		ifi, err := net.InterfaceByIndex(cm.IfIndex)
		if err == nil && (ifi.Flags&net.FlagMulticast) != 0 {
			ocm := &ipv4.ControlMessage{IfIndex: cm.IfIndex}
			if _, werr := r.pc4.WriteTo(out, ocm, dst); werr == nil {
				r.logger.Debug("matter.mdns.subtype.write4_ok",
					slog.String("iface", ifi.Name),
					slog.Int("bytes", len(out)))
				return
			}
			tried = true
		}
	}
	for _, ifi := range listMulticastInterfaces() {
		ocm := &ipv4.ControlMessage{IfIndex: ifi.Index}
		if _, werr := r.pc4.WriteTo(out, ocm, dst); werr != nil {
			r.logger.Debug("matter.mdns.subtype.write4_err",
				slog.String("err", werr.Error()),
				slog.String("iface", ifi.Name))
			continue
		}
		r.logger.Debug("matter.mdns.subtype.write4_ok",
			slog.String("iface", ifi.Name),
			slog.Int("bytes", len(out)))
		return
	}
	if !tried {
		r.logger.Debug("matter.mdns.subtype.write4_drop",
			slog.String("reason", "no multicast-capable interface accepted the reply"))
	}
}

func (r *SubtypeResponder) writeMulticastV6(out []byte, cm *ipv6.ControlMessage, dst *net.UDPAddr) {
	tried := false
	if cm != nil && cm.IfIndex > 0 {
		ifi, err := net.InterfaceByIndex(cm.IfIndex)
		if err == nil && (ifi.Flags&net.FlagMulticast) != 0 {
			ocm := &ipv6.ControlMessage{IfIndex: cm.IfIndex}
			if _, werr := r.pc6.WriteTo(out, ocm, dst); werr == nil {
				return
			}
			tried = true
		}
	}
	for _, ifi := range listMulticastInterfaces() {
		ocm := &ipv6.ControlMessage{IfIndex: ifi.Index}
		if _, werr := r.pc6.WriteTo(out, ocm, dst); werr != nil {
			r.logger.Debug("matter.mdns.subtype.write6_err",
				slog.String("err", werr.Error()),
				slog.String("iface", ifi.Name))
			continue
		}
		return
	}
	if !tried {
		r.logger.Debug("matter.mdns.subtype.write6_drop",
			slog.String("reason", "no multicast-capable interface accepted the reply"))
	}
}

// buildReply parses an inbound mDNS packet, extracts PTR questions
// that match a registered subtype mapping, and returns the packed
// response bytes. (false, nil) means "no answer to send" — either the
// packet was malformed, a response, or did not ask for any subtype we
// know about. Centralised so the v4/v6 paths share the parsing logic.
func (r *SubtypeResponder) buildReply(buf []byte) ([]byte, bool) {
	msg := new(dns.Msg)
	if err := msg.Unpack(buf); err != nil {
		return nil, false
	}
	if msg.Response { // only react to queries.
		return nil, false
	}
	answers := r.matchAnswers(msg.Question)
	if len(answers) == 0 {
		return nil, false
	}
	resp := new(dns.Msg)
	resp.SetReply(msg)
	resp.Authoritative = true
	resp.Answer = answers
	out, err := resp.Pack()
	if err != nil {
		return nil, false
	}
	return out, true
}

func (r *SubtypeResponder) matchAnswers(qs []dns.Question) []dns.RR {
	if len(qs) == 0 {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if len(r.mappings) == 0 {
		return nil
	}
	out := make([]dns.RR, 0, len(qs))
	for _, q := range qs {
		if q.Qtype != dns.TypePTR && q.Qtype != dns.TypeANY {
			continue
		}
		target, ok := r.mappings[strings.ToLower(q.Name)]
		if !ok {
			continue
		}
		out = append(out, &dns.PTR{
			Hdr: dns.RR_Header{
				Name:   q.Name,
				Rrtype: dns.TypePTR,
				Class:  dns.ClassINET,
				Ttl:    120,
			},
			Ptr: target,
		})
	}
	return out
}

// joinMcast4 / joinMcast6 mirror grandcat/zeroconf's multicast join
// logic but on a fresh UDP socket so the two responders can coexist.
// Multicast wildcard bind + JoinGroup is the standard approach
// supported by both Linux and macOS.
func joinMcast4() (*ipv4.PacketConn, error) {
	addr := &net.UDPAddr{IP: net.IPv4(224, 0, 0, 0), Port: 5353}
	conn, err := net.ListenUDP("udp4", addr)
	if err != nil {
		return nil, err
	}
	pc := ipv4.NewPacketConn(conn)
	if err := pc.SetControlMessage(ipv4.FlagInterface, true); err != nil {
		_ = conn.Close()
		return nil, err
	}
	group := &net.UDPAddr{IP: net.IPv4(224, 0, 0, 251)}
	joined := 0
	var primaryIface *net.Interface
	for i := range listMulticastInterfaces() {
		ifi := listMulticastInterfaces()[i]
		if jerr := pc.JoinGroup(&ifi, group); jerr == nil {
			joined++
			// First non-loopback, non-tunnel interface with an IPv4
			// address wins. Used as the default outgoing interface
			// for multicast sends — without this, macOS rejects
			// `WriteTo(224.0.0.251:5353)` with `network unreachable`
			// because the wildcard listen socket has no implicit
			// route into a multicast group.
			if primaryIface == nil && isPrimaryV4(&ifi) {
				primaryIface = &ifi
			}
		}
	}
	if joined == 0 {
		_ = pc.Close()
		return nil, fmt.Errorf("no multicast interface accepted IPv4 join")
	}
	if primaryIface != nil {
		_ = pc.SetMulticastInterface(primaryIface)
	}
	return pc, nil
}

func joinMcast6() (*ipv6.PacketConn, error) {
	addr := &net.UDPAddr{IP: net.ParseIP("ff02::"), Port: 5353}
	conn, err := net.ListenUDP("udp6", addr)
	if err != nil {
		return nil, err
	}
	pc := ipv6.NewPacketConn(conn)
	if err := pc.SetControlMessage(ipv6.FlagInterface, true); err != nil {
		_ = conn.Close()
		return nil, err
	}
	group := &net.UDPAddr{IP: net.ParseIP("ff02::fb")}
	joined := 0
	var primaryIface *net.Interface
	for i := range listMulticastInterfaces() {
		ifi := listMulticastInterfaces()[i]
		if jerr := pc.JoinGroup(&ifi, group); jerr == nil {
			joined++
			if primaryIface == nil && isPrimaryV6(&ifi) {
				primaryIface = &ifi
			}
		}
	}
	if joined == 0 {
		_ = pc.Close()
		return nil, fmt.Errorf("no multicast interface accepted IPv6 join")
	}
	if primaryIface != nil {
		_ = pc.SetMulticastInterface(primaryIface)
	}
	return pc, nil
}

// isPrimaryV4 reports whether the interface looks like the host's
// primary IPv4 multicast egress: not a loopback, has at least one
// non-loopback IPv4 address, not a point-to-point tunnel. The first
// one matched becomes the SetMulticastInterface target.
func isPrimaryV4(ifi *net.Interface) bool {
	if ifi.Flags&net.FlagLoopback != 0 || ifi.Flags&net.FlagPointToPoint != 0 {
		return false
	}
	addrs, err := ifi.Addrs()
	if err != nil {
		return false
	}
	for _, a := range addrs {
		ipn, ok := a.(*net.IPNet)
		if !ok || ipn.IP.IsLoopback() {
			continue
		}
		if v4 := ipn.IP.To4(); v4 != nil && !v4.IsLinkLocalUnicast() {
			return true
		}
	}
	return false
}

func isPrimaryV6(ifi *net.Interface) bool {
	if ifi.Flags&net.FlagLoopback != 0 || ifi.Flags&net.FlagPointToPoint != 0 {
		return false
	}
	addrs, err := ifi.Addrs()
	if err != nil {
		return false
	}
	for _, a := range addrs {
		ipn, ok := a.(*net.IPNet)
		if !ok || ipn.IP.IsLoopback() {
			continue
		}
		if ipn.IP.To4() == nil && !ipn.IP.IsUnspecified() {
			return true
		}
	}
	return false
}

// listMulticastInterfaces returns the system interfaces that are up
// and multicast-capable. Mirrors grandcat/zeroconf's helper of the
// same name; duplicated here so the responder does not import an
// internal symbol.
func listMulticastInterfaces() []net.Interface {
	out := make([]net.Interface, 0, 4)
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	for _, ifi := range ifaces {
		if (ifi.Flags & net.FlagUp) == 0 {
			continue
		}
		if (ifi.Flags & net.FlagMulticast) == 0 {
			continue
		}
		out = append(out, ifi)
	}
	return out
}

func ensureTrailingDot(s string) string {
	if !strings.HasSuffix(s, ".") {
		return s + "."
	}
	return s
}

func isTimeout(err error) bool {
	if err == nil {
		return false
	}
	var ne net.Error
	if errors.As(err, &ne) {
		return ne.Timeout()
	}
	return false
}
