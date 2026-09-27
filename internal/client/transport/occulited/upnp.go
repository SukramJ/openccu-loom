// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package occulited

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"

	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

// upnpLimit caps the UPnP description read.
const upnpLimit = 64 << 10

// UPnPDevice is the device element of the box's open UPnP description at
// /upnp/basic_dev.cgi: manufacturer and model name are "openccu-lite",
// SerialNumber is the board serial (or the host name when the board has
// none), FriendlyName the host name.
type UPnPDevice struct {
	DeviceType       string `xml:"deviceType"`
	FriendlyName     string `xml:"friendlyName"`
	Manufacturer     string `xml:"manufacturer"`
	ModelName        string `xml:"modelName"`
	ModelDescription string `xml:"modelDescription"`
	SerialNumber     string `xml:"serialNumber"`
	UDN              string `xml:"UDN"`
}

type upnpRoot struct {
	Device UPnPDevice `xml:"device"`
}

// ParseUPnP decodes a UPnP device description. The namespace is not
// checked; a document without a serial number is an [ErrProtocol] error.
func ParseUPnP(r io.Reader) (UPnPDevice, error) {
	var root upnpRoot
	if err := xml.NewDecoder(io.LimitReader(r, upnpLimit)).Decode(&root); err != nil {
		return UPnPDevice{}, fmt.Errorf("%w: UPnP description: %w", ErrProtocol, err)
	}
	if root.Device.SerialNumber == "" {
		return UPnPDevice{}, fmt.Errorf("%w: UPnP description without serialNumber", ErrProtocol)
	}
	return root.Device, nil
}

// UPnP reads and parses GET /upnp/basic_dev.cgi (open).
func (c *Client) UPnP(ctx context.Context) (UPnPDevice, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpoint("/upnp/basic_dev.cgi", nil), http.NoBody)
	if err != nil {
		return UPnPDevice{}, fmt.Errorf("occulited: build request: %w", err)
	}
	resp, err := c.calls.Do(req)
	if err != nil {
		return UPnPDevice{}, fmt.Errorf("occulited: GET /upnp/basic_dev.cgi: %w: %w", hmerr.ErrNoConnection, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, errorBodyLimit))
		return UPnPDevice{}, parseAPIError(resp, req, raw)
	}
	return ParseUPnP(resp.Body)
}
