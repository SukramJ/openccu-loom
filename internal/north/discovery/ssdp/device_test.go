// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package ssdp

import (
	"testing"
)

// realOpenCCUXML is the XML fixture from a real OpenCCU device-description response.
const realOpenCCUXML = `<?xml version="1.0" encoding="UTF-8"?>
<root xmlns="urn:schemas-upnp-org:device-1-0"><specVersion><major>1</major><minor>0</minor></specVersion><URLBase>http://172.18.4.29</URLBase><device><deviceType>urn:schemas-upnp-org:device:Basic:1</deviceType><friendlyName>OpenCCU - Otto</friendlyName><manufacturer>OpenCCU</manufacturer><manufacturerURL>https://openccu.de</manufacturerURL><modelDescription>OpenCCU 3014F711A0001F5A4993D962</modelDescription><modelName>OpenCCU</modelName><UDN>uuid:upnp-BasicDevice-1_0-3014F711A0001F5A4993D962</UDN></device></root>`

const classicCCUXML = `<?xml version="1.0" encoding="UTF-8"?>
<root xmlns="urn:schemas-upnp-org:device-1-0"><URLBase>http://192.168.1.5</URLBase><device><friendlyName>HomeMatic Central - 0001ABCDEF12</friendlyName><manufacturer>eq-3</manufacturer><modelName>CCU3</modelName><modelDescription>HomeMatic Central 0001ABCDEF12</modelDescription><UDN>uuid:upnp-BasicDevice-1_0-0001ABCDEF12</UDN></device></root>`

const sonosXML = `<?xml version="1.0" encoding="UTF-8"?>
<root xmlns="urn:schemas-upnp-org:device-1-0"><URLBase>http://10.0.0.50</URLBase><device><friendlyName>Sonos Speaker</friendlyName><manufacturer>Sonos</manufacturer><modelName>Play:1</modelName><UDN>uuid:sonos-1234567890</UDN></device></root>`

// TestParseDeviceDescription covers the main XML-parse path.
func TestParseDeviceDescription(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		body        string
		locationURL string
		wantOK      bool
		wantSerial  string
		wantName    string
		wantHost    string
		wantMfr     string
	}{
		{
			name:        "real OpenCCU device",
			body:        realOpenCCUXML,
			locationURL: "http://172.18.4.29/upnp/basic_dev.cgi",
			wantOK:      true,
			wantSerial:  "3014F711A0001F5A4993D962",
			wantName:    "Otto",
			wantHost:    "172.18.4.29",
			wantMfr:     "OpenCCU",
		},
		{
			name:        "classic eQ-3 CCU3",
			body:        classicCCUXML,
			locationURL: "http://192.168.1.5/upnp/basic_dev.cgi",
			wantOK:      true,
			wantSerial:  "0001ABCDEF12",
			wantName:    "0001ABCDEF12",
			wantHost:    "192.168.1.5",
			wantMfr:     "eq-3",
		},
		{
			name:        "Sonos — foreign manufacturer",
			body:        sonosXML,
			locationURL: "http://10.0.0.50/upnp/basic_dev.cgi",
			wantOK:      false,
		},
		{
			name:        "malformed XML",
			body:        "this is not xml <<<",
			locationURL: "http://192.168.1.1/upnp/basic_dev.cgi",
			wantOK:      false,
		},
		{
			name: "UDN without dash — fallback to modelDescription tail",
			body: `<?xml version="1.0" encoding="UTF-8"?>
<root xmlns="urn:schemas-upnp-org:device-1-0"><URLBase>http://10.0.0.1</URLBase><device>
<friendlyName>HomeMatic Central</friendlyName><manufacturer>eq-3</manufacturer>
<modelName>CCU3</modelName><modelDescription>ABCDEFGHIJKLMNOP</modelDescription>
<UDN>uuid:nodashhere</UDN></device></root>`,
			locationURL: "http://10.0.0.1/upnp/basic_dev.cgi",
			wantOK:      true,
			// UDN has no dash → strip "uuid:" prefix → "nodashhere"
			wantSerial: "nodashhere",
			// "HomeMatic " prefix is stripped by centralName → "Central"
			wantName: "Central",
			wantHost: "10.0.0.1",
		},
		{
			name: "empty friendlyName — falls back to manufacturer",
			body: `<?xml version="1.0" encoding="UTF-8"?>
<root xmlns="urn:schemas-upnp-org:device-1-0"><URLBase>http://10.0.0.2</URLBase><device>
<friendlyName></friendlyName><manufacturer>RaspberryMatic</manufacturer>
<modelName>RaspberryMatic</modelName><modelDescription>Serial1234567890</modelDescription>
<UDN>uuid:upnp-Basic-1_0-Serial1234567890</UDN></device></root>`,
			locationURL: "http://10.0.0.2/upnp/basic_dev.cgi",
			wantOK:      true,
			wantSerial:  "Serial1234567890",
			wantName:    "RaspberryMatic",
			wantHost:    "10.0.0.2",
		},
		{
			name: "no URLBase — falls back to locationURL host",
			body: `<?xml version="1.0" encoding="UTF-8"?>
<root xmlns="urn:schemas-upnp-org:device-1-0"><device>
<friendlyName>OpenCCU - Keller</friendlyName><manufacturer>OpenCCU</manufacturer>
<modelName>OpenCCU</modelName><modelDescription>OpenCCU ZZZZ1234567890</modelDescription>
<UDN>uuid:upnp-Basic-1_0-ZZZZ1234567890</UDN></device></root>`,
			locationURL: "http://10.0.0.3/upnp/basic_dev.cgi",
			wantOK:      true,
			wantSerial:  "ZZZZ1234567890",
			wantName:    "Keller",
			wantHost:    "10.0.0.3",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, ok := parseDeviceDescription([]byte(tc.body), tc.locationURL)
			if ok != tc.wantOK {
				t.Fatalf("ok=%v want %v (body snippet: %.60s)", ok, tc.wantOK, tc.body)
			}
			if !tc.wantOK {
				return
			}
			if got.Serial != tc.wantSerial {
				t.Errorf("Serial=%q want %q", got.Serial, tc.wantSerial)
			}
			if got.Name != tc.wantName {
				t.Errorf("Name=%q want %q", got.Name, tc.wantName)
			}
			if got.Host != tc.wantHost {
				t.Errorf("Host=%q want %q", got.Host, tc.wantHost)
			}
			if tc.wantMfr != "" && got.Manufacturer != tc.wantMfr {
				t.Errorf("Manufacturer=%q want %q", got.Manufacturer, tc.wantMfr)
			}
		})
	}
}

// TestCentralName exercises the prefix-stripping logic directly.
func TestCentralName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		friendlyName string
		manufacturer string
		want         string
	}{
		{"OpenCCU - Otto", "OpenCCU", "Otto"},
		{"HomeMatic Central - 0001ABC", "eq-3", "0001ABC"},
		{"OpenCCU - ", "OpenCCU", "-"}, // " - " found but empty tail → strips "OpenCCU " prefix, leaving "-"
		{"RaspberryMatic", "RaspberryMatic", "RaspberryMatic"},
		{"", "OpenCCU", "OpenCCU"},
		{"", "", "CCU"},
		{"OpenCCU Keller", "OpenCCU", "Keller"},
	}

	for _, tc := range tests {
		got := centralName(tc.friendlyName, tc.manufacturer)
		if got != tc.want {
			t.Errorf("centralName(%q, %q) = %q, want %q", tc.friendlyName, tc.manufacturer, got, tc.want)
		}
	}
}

// TestSerialFrom exercises the UDN-parsing and modelDescription-fallback logic.
func TestSerialFrom(t *testing.T) {
	t.Parallel()

	tests := []struct {
		udn       string
		modelDesc string
		want      string
	}{
		{"uuid:upnp-BasicDevice-1_0-3014F711A0001F5A4993D962", "", "3014F711A0001F5A4993D962"},
		{"uuid:upnp-BasicDevice-1_0-0001ABCDEF12", "HomeMatic 0001ABCDEF12", "0001ABCDEF12"},
		// UDN without "-": strip uuid: prefix
		{"uuid:nodashhere", "ignored", "nodashhere"},
		// Empty UDN → last 10 chars of modelDescription
		{"", "ABCDEFGHIJKLMNOPQRST", "KLMNOPQRST"},
		// Empty both
		{"", "", ""},
		// Short modelDescription (≤10) → empty
		{"", "SHORT", ""},
	}

	for _, tc := range tests {
		got := serialFrom(tc.udn, tc.modelDesc)
		if got != tc.want {
			t.Errorf("serialFrom(%q, %q) = %q, want %q", tc.udn, tc.modelDesc, got, tc.want)
		}
	}
}

// TestIsCentralManufacturer checks the manufacturer-filter function.
func TestIsCentralManufacturer(t *testing.T) {
	t.Parallel()

	tests := []struct {
		mfr          string
		modelName    string
		friendlyName string
		want         bool
	}{
		{"OpenCCU", "OpenCCU", "OpenCCU - Otto", true},
		{"eq-3", "CCU3", "HomeMatic Central", true},
		{"EQ3", "CCU3", "", true},
		{"", "", "HomeMatic Central - Test", true},
		{"Sonos", "Play:1", "Sonos Speaker", false},
		{"Philips", "Hue", "Hue Bridge", false},
		{"", "RaspberryMatic", "", true},
	}

	for _, tc := range tests {
		got := isCentralManufacturer(tc.mfr, tc.modelName, tc.friendlyName)
		if got != tc.want {
			t.Errorf("isCentralManufacturer(%q, %q, %q) = %v, want %v",
				tc.mfr, tc.modelName, tc.friendlyName, got, tc.want)
		}
	}
}
