// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Package ssdp implements active SSDP (UPnP) discovery of Homematic /
// OpenCCU central units on the local network. It periodically multicasts an
// M-SEARCH, follows each responder's device-description URL
// (`/upnp/basic_dev.cgi`), and surfaces the matching CCUs so the operator can
// adopt or ignore them.
//
// The field-extraction rules (manufacturer filter, friendlyName → name,
// modelDescription → serial) follow the established CCU discovery conventions;
// provenance and the scope decision live in docs/adr/0046-ssdp-ccu-discovery.md.
package ssdp

import (
	"encoding/xml"
	"net/url"
	"strings"
	"time"

	"github.com/SukramJ/openccu-loom/internal/routingkey"
)

// DiscoveredCCU is one central unit found on the network. Serial is the stable
// dedup / ignore key; Host is the address an operator would configure.
type DiscoveredCCU struct {
	// Serial is the stable unique id, derived from the UDN (preferred) or the
	// modelDescription tail. Used to dedupe responses and to key the ignore list.
	Serial string `json:"serial"`
	// Name is the human-friendly central name (friendlyName with the
	// manufacturer prefix stripped, e.g. "OpenCCU - Hausanlage" → "Hausanlage").
	Name string `json:"name"`
	// Host is the CCU's IP / hostname, taken from the device-description URL.
	Host string `json:"host"`
	// Manufacturer / Model are surfaced for display and let the UI distinguish
	// an OpenCCU from a classic eQ-3 CCU.
	Manufacturer string `json:"manufacturer"`
	Model        string `json:"model"`
	// Location is the full device-description URL the response pointed at.
	Location string `json:"location,omitempty"`
	// LastSeen is when the most recent M-SEARCH response for this CCU arrived.
	LastSeen time.Time `json:"last_seen"`
}

// upnpRoot is the subset of the UPnP device-description XML
// (`/upnp/basic_dev.cgi`) we read. encoding/xml matches local element names
// regardless of the `urn:schemas-upnp-org:device-1-0` default namespace.
type upnpRoot struct {
	XMLName xml.Name   `xml:"root"`
	URLBase string     `xml:"URLBase"`
	Device  upnpDevice `xml:"device"`
}

type upnpDevice struct {
	DeviceType       string `xml:"deviceType"`
	FriendlyName     string `xml:"friendlyName"`
	Manufacturer     string `xml:"manufacturer"`
	ManufacturerURL  string `xml:"manufacturerURL"`
	ModelName        string `xml:"modelName"`
	ModelDescription string `xml:"modelDescription"`
	UDN              string `xml:"UDN"`
	PresentationURL  string `xml:"presentationURL"`
}

// parseDeviceDescription parses a `basic_dev.cgi` body and, when it describes a
// Homematic-family central, returns the corresponding DiscoveredCCU. locationURL
// is the URL the body was fetched from and is the sole source of the adopted
// address. ok is false when the body is not a CCU we care about (wrong
// manufacturer / unparseable), so the caller can drop it.
func parseDeviceDescription(body []byte, locationURL string) (DiscoveredCCU, bool) {
	var root upnpRoot
	if err := xml.Unmarshal(body, &root); err != nil {
		return DiscoveredCCU{}, false
	}
	d := root.Device
	if !isCentralManufacturer(d.Manufacturer, d.ModelName, d.FriendlyName) {
		return DiscoveredCCU{}, false
	}
	// The address comes from the URL we fetched, never from the body. SSDP is
	// unauthenticated: any LAN device can answer the M-SEARCH with a
	// description that claims an eQ-3 manufacturer, and a body-supplied
	// URLBase / presentationURL would let it name a *third* host — the one an
	// operator then adopts and submits the CCU credentials to (ADR 0046 puts
	// the location host on the CCU address for the same reason).
	host := hostFromURL(locationURL)
	if host == "" {
		return DiscoveredCCU{}, false
	}
	// Reduce to the canonical per-CCU serial (last 10, case preserved) — the
	// same form the ReGa system.GetSerial reader produces — so a discovered CCU
	// and a configured central identify by an identical string. CanonicalSerial
	// is empty-in/empty-out, so the host fallback below still triggers when no
	// serial could be derived (a host must NOT be truncated).
	serial := routingkey.CanonicalSerial(serialFrom(d.UDN, d.ModelDescription))
	if serial == "" {
		// Without a stable id we cannot dedupe or persist an ignore decision;
		// fall back to the host so the entry is at least usable.
		serial = host
	}
	return DiscoveredCCU{
		Serial:       serial,
		Name:         centralName(d.FriendlyName, d.Manufacturer),
		Host:         host,
		Manufacturer: strings.TrimSpace(d.Manufacturer),
		Model:        strings.TrimSpace(d.ModelName),
		Location:     locationURL,
	}, true
}

// isCentralManufacturer reports whether a device-description belongs to a
// Homematic-family central. The reference discovery filters on manufacturer
// ("OpenCCU"); we also accept classic eQ-3 CCUs so a real CCU2/CCU3 is found,
// matching this project's "OpenCCU + classic" discovery scope (ADR 0046).
//
// "openccu" alone covers a current central: OpenCCU patches the UPnP resource
// to MANUFACTURER = MODEL_NAME = "OpenCCU"
// (buildroot-external/patches/occu/0001-OpenCCU.patch). The unpatched OCCU
// base sets a "HomeMatic Central …" title instead, so the "homematic" needle
// is what still finds an installation predating that patch.
func isCentralManufacturer(manufacturer, modelName, friendlyName string) bool {
	hay := strings.ToLower(manufacturer + " " + modelName + " " + friendlyName)
	for _, needle := range []string{"openccu", "eq-3", "eq3", "homematic"} {
		if strings.Contains(hay, needle) {
			return true
		}
	}
	return false
}

// centralName strips the manufacturer prefix from the friendlyName, as the
// reference instance-name handling does. "OpenCCU - Hausanlage" → "Hausanlage";
// "HomeMatic Central - 0001ABC" keeps its (less friendly) tail. Falls back to
// the manufacturer, then a generic "CCU".
func centralName(friendlyName, manufacturer string) string {
	name := strings.TrimSpace(friendlyName)
	if i := strings.Index(name, " - "); i >= 0 {
		if tail := strings.TrimSpace(name[i+3:]); tail != "" {
			return tail
		}
	}
	for _, pfx := range []string{manufacturer + " ", "OpenCCU ", "HomeMatic "} {
		if pfx != " " && strings.HasPrefix(name, pfx) {
			if tail := strings.TrimSpace(strings.TrimPrefix(name, pfx)); tail != "" {
				return tail
			}
		}
	}
	if name != "" {
		return name
	}
	if m := strings.TrimSpace(manufacturer); m != "" {
		return m
	}
	return "CCU"
}

// serialFrom derives the stable unique id. The UDN
// (`uuid:upnp-BasicDevice-1_0-<SERIAL>`) carries the full hardware serial and
// is preferred; otherwise it falls back to the last 10 characters of the
// modelDescription, the established discovery convention.
func serialFrom(udn, modelDescription string) string {
	if udn != "" {
		s := strings.TrimSpace(udn)
		if i := strings.LastIndex(s, "-"); i >= 0 && i+1 < len(s) {
			if tail := strings.TrimSpace(s[i+1:]); tail != "" {
				return tail
			}
		}
		// A UDN without a "-" separator: drop a leading "uuid:" and use the rest.
		s = strings.TrimSpace(strings.TrimPrefix(s, "uuid:"))
		if s != "" {
			return s
		}
	}
	md := strings.TrimSpace(modelDescription)
	if len(md) > 10 {
		return md[len(md)-10:]
	}
	return ""
}

// hostFromURL returns the host of raw, or "" when it carries none.
func hostFromURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Hostname()
}
