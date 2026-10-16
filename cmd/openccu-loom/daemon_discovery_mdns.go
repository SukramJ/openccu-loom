// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package main

import (
	"context"
	"log/slog"
	"sort"
	"strconv"
	"strings"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/deployment"
	discoverymdns "github.com/SukramJ/openccu-loom/internal/north/discovery/mdns"
	"github.com/SukramJ/openccu-loom/internal/north/rest/handlers"
)

// Daemon discovery over mDNS: the service that lets a client find this
// daemon's REST surface on the local network. It is unrelated to the Matter
// bridge's own DNS-SD records, which go-fabric's node facade publishes.

// startMDNSAdvertiser parses the REST listen address, builds the
// daemon-discovery TXT bundle, and starts a multicast advertiser.
// Returns (nil, nil) when the listen address has no usable port
// (Unix socket etc.) so the caller can skip without an error path.
// Returns (nil, err) when the port is malformed or zeroconf fails to
// register; the caller is expected to log and continue (mDNS is a
// convenience, not a hard dependency).
func startMDNSAdvertiser(ctx context.Context, cfg *config.Config, self mdnsSelf, reg *central.Registry, logger *slog.Logger) (discoverymdns.Advertiser, error) {
	svc, ok := mdnsServiceFor(cfg, self, len(reg.Names()), mdnsCCUSerials(reg))
	if !ok {
		return nil, nil
	}
	adv := discoverymdns.NewMulticast(svc)
	if err := adv.Start(ctx); err != nil {
		return nil, err
	}
	logger.Info("discovery.mdns.started",
		slog.String("service_type", discoverymdns.ServiceType),
		slog.Int("port", svc.Port))
	return adv, nil
}

// mdnsServiceFor builds the mDNS service advertised for client
// auto-discovery from the daemon config. Returns ok=false when the
// REST listen address carries no usable port (mDNS is then skipped).
//
// The TXT bundle gives a discovering client everything it needs to
// reach and label the daemon before authenticating: the API mount
// path, the wire-contract version (mirrors GET /info), whether the
// daemon's own listener is TLS (always 0 — TLS is terminated upstream
// by a reverse proxy / HA ingress), the friendly instance label, a
// pre-auth hint of how many CCUs this daemon serves, and — per
// ADR 0058 — the resolved CCU serial suffixes (`ccus=`). Serials
// resolve during the readiness-gated bring-up and change on live
// adopt, so the record is re-announced at runtime via
// [discoverymdns.Advertiser.UpdateTXT]; `GET /api/v1/system/ccu`
// stays the authoritative post-auth source.
func mdnsServiceFor(cfg *config.Config, self mdnsSelf, centralCount int, ccuSerials []string) (discoverymdns.Service, bool) {
	port, ok := splitListenPort(cfg.North.REST.Listen)
	if !ok {
		return discoverymdns.Service{}, false
	}
	return discoverymdns.Service{
		InstanceName: cfg.North.Discovery.MDNS.InstanceName,
		Port:         port,
		TXT:          mdnsTXT(cfg, self, centralCount, ccuSerials),
	}, true
}

// mdnsSelf is the daemon's self-description as the mDNS record carries
// it: the short form of what `/info` says (ADR 0081). It is built from
// the values `/info` serves — the resolved deployment and the capability
// detector's login paths — so the record cannot say something else.
type mdnsSelf struct {
	kind       deployment.Kind
	tls        bool
	loginPaths []string
}

// mdnsTXTVersion versions the record's key set (RFC 6763 section 6.7).
const mdnsTXTVersion = "1"

// mdnsTXT assembles the TXT bundle. Shared by the initial Register and
// every runtime re-announce so both paths stay identical.
func mdnsTXT(cfg *config.Config, self mdnsSelf, centralCount int, ccuSerials []string) []string {
	tls := "0"
	if self.tls {
		tls = "1"
	}
	txt := []string{
		"txtvers=" + mdnsTXTVersion,
		"path=/api/v1",
		"api_version=" + handlers.APIVersion,
		"tls=" + tls,
		"instance=" + cfg.North.Discovery.MDNS.ResolveInstanceName(),
		"centrals=" + strconv.Itoa(centralCount),
		"deploy=" + string(self.kind),
	}
	if p := self.kind.IngressPath(); p != "" {
		txt = append(txt, "ingress="+p)
	}
	if len(self.loginPaths) > 0 {
		txt = append(txt, "auth="+strings.Join(self.loginPaths, ","))
	}
	if v := mdnsCCUsValue(ccuSerials); v != "" {
		txt = append(txt, "ccus="+v)
	}
	return txt
}

// mdnsCCUsValue joins the resolved serial suffixes sorted and
// comma-separated, dropping whole entries once the TXT string would
// exceed the DNS 255-byte-per-string limit (an invalid record helps
// nobody; the list is best-effort by contract, ADR 0058).
func mdnsCCUsValue(serials []string) string {
	const maxLen = 255 - len("ccus=")
	sorted := make([]string, 0, len(serials))
	for _, sn := range serials {
		if sn != "" {
			sorted = append(sorted, sn)
		}
	}
	sort.Strings(sorted)
	out := ""
	for _, sn := range sorted {
		next := sn
		if out != "" {
			next = out + "," + sn
		}
		if len(next) > maxLen {
			break
		}
		out = next
	}
	return out
}

// mdnsCCUSerials collects the canonical (last-10, case-preserved) serial of
// every registered central; unresolved centrals are skipped and appear on a
// later re-announce.
//
// The value must be the exact string GET /system/ccu reports: the Home
// Assistant integration de-dupes discovery by matching each serial in the
// mDNS ccus= TXT against the config entry's unique_id (the /system/ccu
// serial) with a case-sensitive string compare. The lower-cased routing form
// ([Registry.SerialSuffix]) never matches an upper-case-hex CCU serial, so HA
// re-discovers an already-integrated daemon on every restart.
func mdnsCCUSerials(reg *central.Registry) []string {
	if reg == nil {
		return nil
	}
	names := reg.Names()
	out := make([]string, 0, len(names))
	for _, name := range names {
		if sn := reg.CanonicalSerial(name); sn != "" {
			out = append(out, sn)
		}
	}
	return out
}
