// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

//go:build chiptool

package harness

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"testing"
)

// exposureItem mirrors the subset of the REST GET
// /api/v1/matter/exposable row shape (MatterExposureResponse in
// internal/north/rest/handlers/matter_exposures.go) the send/receive
// suite needs. Duplicated here rather than imported — the harness
// talks to the daemon-under-test only through its REST/chip-tool
// surface, never by importing daemon-internal packages.
type exposureItem struct {
	DeviceAddress string   `json:"device_address"`
	ChannelNo     int      `json:"channel_no"`
	DPKind        string   `json:"dp_kind"`
	DPKey         string   `json:"dp_key"`
	Enabled       bool     `json:"enabled"`
	Mappable      string   `json:"mappable"`
	Clusters      []uint32 `json:"clusters,omitempty"`
}

// fetchExposable GETs the current exposable list. Returns an error
// when the daemon does not answer 200 so callers can decide whether
// to Fatal or degrade to a skip.
func (b *Bridge) fetchExposable(t *testing.T) ([]exposureItem, error) {
	t.Helper()
	var list struct {
		Items []exposureItem `json:"items"`
	}
	status := b.RESTGet(t, "/api/v1/matter/exposable", &list)
	if status != http.StatusOK {
		return nil, fmt.Errorf("GET /matter/exposable: status=%d", status)
	}
	return list.Items, nil
}

// ResolveCCUAddress resolves a bridged Matter endpoint back to its
// CCU-side ground truth. It reads
// BridgedDeviceBasicInformation.SerialNumber off the wire — the
// bridge stamps SerialNumber with the source device's address (see
// internal/north/matter/endpoint/materialize.go) — then
// cross-references GET /api/v1/matter/exposable for the enabled row
// whose device_address matches, returning that row's dp_key.
//
// When more than one enabled exposure shares the same device_address
// (a multi-channel device with several mapped DPs), the first match
// wins. Callers that need to disambiguate by cluster should go
// through [Bridge.CCUAddressForCluster] instead, which narrows to a
// specific cluster before ever reaching this function.
func (b *Bridge) ResolveCCUAddress(ctx context.Context, t *testing.T, endpointID uint16) (address, dpKey string, ok bool) {
	t.Helper()
	ctl := b.SharedCtl
	if ctl == nil {
		return "", "", false
	}
	out, err := ctl.ReadAttr(ctx, t, "bridgeddevicebasicinformation", "serial-number", endpointID)
	if err != nil {
		return "", "", false
	}
	addr, found := FindAttrString(out, "SerialNumber")
	if !found || addr == "" {
		return "", "", false
	}
	items, err := b.fetchExposable(t)
	if err != nil {
		return "", "", false
	}
	for _, it := range items {
		if it.Enabled && it.DeviceAddress == addr {
			// Return the CHANNEL address (ADDRESS:CHANNEL), not the device
			// root: the VALUES paramset a SEND assertion reads and a RECEIVE
			// injection writes lives on the channel, so godevccu's
			// GetValue/SetValue/SimulateDeviceEvent reject the bare device
			// address with `paramset "VALUES" not found on "VCU…"`.
			return fmt.Sprintf("%s:%d", it.DeviceAddress, it.ChannelNo), it.DPKey, true
		}
	}
	return "", "", false
}

// CCUAddressForCluster discovers a bridged endpoint whose Descriptor
// ServerList advertises clusterID, then resolves it to a CCU
// (address, dp_key) pair via [Bridge.ResolveCCUAddress]. This is the
// entry point send/receive table rows use to go from "I want to test
// the OnOff cluster" to "here is the CCU-side channel to inject/
// assert against" without hand-picking a fixture device.
//
// Endpoint discovery mirrors clusters_test.go's
// discoverEndpointsWith: one wildcard Descriptor.ServerList read
// across every endpoint (EP 0xFFFF) rather than N per-endpoint reads
// — sequential per-EP reads accumulate CASE sessions on the daemon
// and start timing out past ~25 endpoints. The logic is duplicated
// here (instead of imported) because that helper lives in the
// _test.go-only `chiptool` package, which this harness package
// cannot import.
func (b *Bridge) CCUAddressForCluster(ctx context.Context, t *testing.T, clusterID uint16) (endpointID uint16, address, dpKey string, ok bool) {
	t.Helper()
	ctl := b.SharedCtl
	if ctl == nil {
		return 0, "", "", false
	}
	out, err := ctl.ReadAttr(ctx, t, "descriptor", "server-list", 0xFFFF)
	if err != nil {
		return 0, "", "", false
	}
	perEP := ServerListIDsPerEndpoint(out)
	eps := make([]uint16, 0, len(perEP))
	for ep := range perEP {
		eps = append(eps, ep)
	}
	sort.Slice(eps, func(i, j int) bool { return eps[i] < eps[j] })

	cluster32 := uint32(clusterID)
	for _, ep := range eps {
		if !HasCluster(perEP[ep], cluster32) {
			continue
		}
		addr, dp, resolved := b.ResolveCCUAddress(ctx, t, ep)
		if resolved {
			return ep, addr, dp, true
		}
	}
	return 0, "", "", false
}
