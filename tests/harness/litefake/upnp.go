// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package litefake

import (
	"encoding/xml"
	"net/http"
	"path"
	"strings"
)

// upnpDevice is the device element of the UPnP Basic:1 description.
type upnpDevice struct {
	DeviceType       string `xml:"deviceType"`
	FriendlyName     string `xml:"friendlyName"`
	Manufacturer     string `xml:"manufacturer"`
	ModelDescription string `xml:"modelDescription"`
	ModelName        string `xml:"modelName"`
	SerialNumber     string `xml:"serialNumber"`
	UDN              string `xml:"UDN"`
}

type upnpSpec struct {
	Major int `xml:"major"`
	Minor int `xml:"minor"`
}

type upnpRoot struct {
	XMLName     xml.Name   `xml:"urn:schemas-upnp-org:device-1-0 root"`
	SpecVersion upnpSpec   `xml:"specVersion"`
	Device      upnpDevice `xml:"device"`
}

// handleUPnP answers the open GET /upnp/basic_dev.cgi with the box's
// UPnP description: manufacturer and model name are both
// "openccu-lite", the UDN embeds the serial, the friendly name is the
// host name.
func (f *Fake) handleUPnP(w http.ResponseWriter, _ *http.Request) {
	serial := f.opts.Serial
	doc := upnpRoot{
		SpecVersion: upnpSpec{Major: 1, Minor: 0},
		Device: upnpDevice{
			DeviceType:       "urn:schemas-upnp-org:device:Basic:1",
			FriendlyName:     f.opts.Hostname,
			Manufacturer:     "openccu-lite",
			ModelDescription: "openccu-lite " + serial,
			ModelName:        "openccu-lite",
			SerialNumber:     serial,
			UDN:              "uuid:upnp-BasicDevice-1_0-" + serial,
		},
	}
	raw, err := xml.MarshalIndent(doc, "", "  ")
	if err != nil {
		http.Error(w, "encode failed", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/xml; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(xml.Header))
	_, _ = w.Write(raw)
}

// shellHTML stands in for occulited's single-page web shell.
const shellHTML = `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>openccu-lite</title></head>
<body><div id="app"></div></body></html>
`

// staticExtension reports whether p ends in one of the extensions the
// web shell treats as a static file (answered 404 when missing) rather
// than a client-side route.
func staticExtension(p string) bool {
	switch strings.ToLower(path.Ext(p)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".svg", ".ico", ".css", ".js",
		".mjs", ".map", ".json", ".xml", ".txt", ".woff", ".woff2", ".ttf", ".webmanifest":
		return true
	default:
		return false
	}
}

// handleShell is the catch-all for non-API paths: 200 with the HTML
// shell for anything that is not a static file, so a CCU probe such as
// /ise/checkrega.cgi gets HTML, never "OK".
func (f *Fake) handleShell(w http.ResponseWriter, r *http.Request) {
	if staticExtension(r.URL.Path) {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(shellHTML))
}
