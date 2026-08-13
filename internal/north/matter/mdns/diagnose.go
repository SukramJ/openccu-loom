// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mdns

import (
	"net"
	"sort"
	"strings"
)

// Severity ranks a finding by what it costs an operator.
// loom:reachable:reason="the severity of every Finding that Diagnose returns; the REST layer copies it into the diagnostics payload on each GET /api/v1/matter/mdns. A string alias without methods, which the analyzer's type heuristic cannot see used"
type Severity string

// Severity values.
const (
	// SeverityError marks a condition under which commissioning or
	// operational discovery cannot work at all.
	SeverityError Severity = "error"
	// SeverityWarning marks a condition that works for some ecosystems
	// and not others, or that depends on the network the daemon runs on.
	SeverityWarning Severity = "warning"
)

// Finding is one diagnosed condition of the advertised records.
// loom:reachable:reason="returned by Diagnose, which the daemon's matterMdnsReporter calls on each GET /api/v1/matter/mdns; a data struct whose fields the REST layer copies out, invisible to the analyzer's method-based type heuristic"
type Finding struct {
	Severity Severity
	// Code is a stable machine-readable identifier; the message is
	// prose for an operator and may be reworded.
	Code    string
	Message string
	// Service names the advertised record the finding applies to, empty
	// when it concerns the advertisement as a whole.
	Service string
}

// Diagnose inspects the records the bridge currently advertises and
// reports what would keep a controller from finding or commissioning it.
//
// It exists because every one of these conditions is silent: the daemon
// advertises successfully, the log says so, and the controller simply
// never shows the bridge. Each check below corresponds to a failure that
// has actually happened rather than a rule read off the spec.
func Diagnose(services []Service) []Finding {
	var findings []Finding

	if len(services) == 0 {
		return []Finding{{
			Severity: SeverityError,
			Code:     "no_services",
			Message: "The bridge advertises no mDNS records, so no controller can discover it. " +
				"Either advertising is set to `noop` or publishing failed at start-up.",
		}}
	}

	var hasCommissionable bool
	for i := range services {
		svc := &services[i]
		name := svc.ServiceType
		if svc.InstanceName != "" {
			name = svc.InstanceName + "." + svc.ServiceType
		}
		if svc.ServiceType == ServiceTypeCommissionable {
			hasCommissionable = true
			findings = append(findings, diagnoseCommissionable(svc, name)...)
		}
		findings = append(findings, diagnoseAddresses(svc, name)...)
	}

	if !hasCommissionable {
		// Operational-only advertising is the correct state for a bridge
		// that is already commissioned everywhere it needs to be, so this
		// is not an error — but it is the first thing to check when
		// "adding the bridge" finds nothing.
		findings = append(findings, Finding{
			Severity: SeverityWarning,
			Code:     "no_commissionable_service",
			Message: "No commissionable (`_matterc._udp`) record is advertised, so a controller cannot " +
				"add this bridge right now. Open a commissioning window to advertise one.",
		})
	}

	sort.SliceStable(findings, func(i, j int) bool {
		if findings[i].Severity != findings[j].Severity {
			return findings[i].Severity == SeverityError
		}
		return findings[i].Code < findings[j].Code
	})
	return findings
}

// matterDefaultCommissioningPort is the UDP port Matter assigns to
// operational and commissionable discovery.
const matterDefaultCommissioningPort = 5540

// diagnoseCommissionable checks the record a controller browses for when
// adding the bridge.
func diagnoseCommissionable(svc *Service, name string) []Finding {
	var out []Finding

	// Apple Home and Google Home browse the subtype PTRs rather than the
	// bare service type, so a record without them is discoverable by
	// chip-tool and invisible to the two ecosystems most users have.
	if len(svc.Subtypes) == 0 {
		out = append(out, Finding{
			Severity: SeverityError,
			Code:     "no_subtypes",
			Service:  name,
			Message: "The commissionable record announces no service subtypes (`_L<discriminator>`, " +
				"`_S<short>`, `_CM`). Apple Home and Google Home filter their discovery browse through " +
				"these, so both will not offer the bridge even though it is advertising.",
		})
	}

	// Alexa only ever contacts UDP 5540. A configured port is legitimate
	// for the other ecosystems, which is why this is a warning attached
	// to the record rather than a refusal to advertise.
	if svc.Port != 0 && svc.Port != matterDefaultCommissioningPort {
		out = append(out, Finding{
			Severity: SeverityWarning,
			Code:     "non_default_commissioning_port",
			Service:  name,
			Message: "Commissioning is advertised on a port other than 5540. Amazon Alexa commissions " +
				"only on 5540 and will not find the bridge; Apple, Google and chip-tool honour the " +
				"advertised port.",
		})
	}
	return out
}

// diagnoseAddresses checks what the record tells a controller to connect
// to.
func diagnoseAddresses(svc *Service, name string) []Finding {
	var (
		out         []Finding
		hasIPv6     bool
		unroutable  []string
		hasRoutable bool
	)
	for _, ip := range svc.Addresses {
		if ip == nil {
			continue
		}
		if ip.To4() == nil {
			hasIPv6 = true
		}
		if isContainerInternal(ip) {
			unroutable = append(unroutable, ip.String())
			continue
		}
		hasRoutable = true
	}

	if len(svc.Addresses) == 0 {
		out = append(out, Finding{
			Severity: SeverityError,
			Code:     "no_addresses",
			Service:  name,
			Message:  "The record announces no IP address, so a controller has nothing to connect to.",
		})
		return out
	}

	// A container-internal address in the announcement is the classic
	// host-network add-on failure: the daemon sees the bridge network's
	// own addresses, announces them, and the controller cannot route to
	// them. It looks like a working advertisement from inside.
	if len(unroutable) > 0 {
		severity := SeverityWarning
		if !hasRoutable {
			severity = SeverityError
		}
		out = append(out, Finding{
			Severity: severity,
			Code:     "container_internal_address",
			Service:  name,
			Message: "The record announces container-internal addresses (" + strings.Join(unroutable, ", ") +
				") that a controller on the LAN cannot reach. This is what a container with its own " +
				"bridge network announces when it is not restricted to the host's LAN addresses.",
		})
	}

	// Matter operational discovery is IPv6-first, and Apple's controllers
	// rely on it. An IPv4-only announcement pairs with chip-tool and then
	// fails against a HomePod.
	if !hasIPv6 {
		out = append(out, Finding{
			Severity: SeverityWarning,
			Code:     "no_ipv6_address",
			Service:  name,
			Message: "The record announces no IPv6 address. Matter discovery is IPv6-first and Apple's " +
				"controllers depend on it, so pairing may work with chip-tool and fail with a HomePod.",
		})
	}
	return out
}

// isContainerInternal reports whether ip is in a range a container
// runtime hands out for its own bridge networks. It deliberately does
// not treat every RFC1918 address as suspect: a LAN is private too, and
// flagging it would make the check useless in exactly the deployments
// where it matters.
func isContainerInternal(ip net.IP) bool {
	v4 := ip.To4()
	if v4 == nil {
		return false
	}
	switch {
	// Docker's default bridge and its default address pool.
	case v4[0] == 172 && v4[1] >= 17 && v4[1] <= 31:
		return true
	// Docker's default `docker0` subnet when configured from the
	// documented default pool.
	case v4[0] == 172 && v4[1] == 16:
		return true
	default:
		return false
	}
}
