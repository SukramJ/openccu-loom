// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Package definitionexport reproduces the Python reference's
// `Device.export_device_definition`: it fetches a device's raw description and
// the descriptions of all its channels plus their non-LINK paramset
// descriptions, anonymises the addresses behind a single random "VCU" id, and
// packs the result into a zip whose two JSON members are byte-for-byte
// identical to the Python reference's output. The archive is consumed verbatim as a
// godevccu device fixture, so the wire member order and the orjson
// number/string formatting must match exactly — see [orderedjson].
package definitionexport

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"strings"

	"github.com/SukramJ/openccu-loom/internal/orderedjson"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// ErrDeviceNotFound reports that the requested device address is not present on
// any configured central. Callers (REST/WS/CLI) map it to a 404.
var ErrDeviceNotFound = errors.New("definitionexport: device not found")

const (
	addressSeparator           = ":"
	deviceDescriptionsZipDir   = "device_descriptions"
	paramsetDescriptionsZipDir = "paramset_descriptions"
)

// OrderedRPC issues description reads that preserve the CCU's wire member
// order. *client.InterfaceClient satisfies it; the export needs nothing else
// from the south-bound stack. The transport (XML-RPC for radio/wired, BIN-RPC
// for CUxD) is already bound to the InterfaceClient, so the export stays
// transport-agnostic.
type OrderedRPC interface {
	CallOrdered(ctx context.Context, method string, params []any, priority hmenum.CommandPriority) (any, error)
}

// RandomIDFunc returns a fresh anonymisation id. Injected so tests can pin it;
// production uses [DefaultRandomID].
type RandomIDFunc func() (string, error)

// Result is the produced archive together with the device model it describes
// (the model doubles as both JSON filenames and the suggested ".zip" name).
type Result struct {
	Model string
	Zip   []byte
}

// Export fetches device + channel descriptions and their non-LINK paramset
// descriptions through rpc, anonymises every address with a single random VCU
// id, and returns the zip archive
//
//	device_descriptions/{model}.json   — JSON list of device descriptions
//	paramset_descriptions/{model}.json — JSON map address→paramset→param→data
//
// Mirrors the Python reference `model/device.py:_DefinitionExporter.export_data`.
func Export(ctx context.Context, rpc OrderedRPC, deviceAddress string, randomID RandomIDFunc) (*Result, error) {
	descriptions, err := fetchDescriptions(ctx, rpc, deviceAddress)
	if err != nil {
		return nil, err
	}
	model, ok := stringField(descriptions[0], "TYPE")
	if !ok || model == "" {
		return nil, fmt.Errorf("definitionexport: device %s has no TYPE", deviceAddress)
	}

	paramsets := fetchParamsets(ctx, rpc, descriptions)

	vcu, err := randomID()
	if err != nil {
		return nil, fmt.Errorf("definitionexport: random id: %w", err)
	}
	anonymiseDescriptions(descriptions, vcu)
	paramsets = anonymiseParamsets(paramsets, vcu)

	deviceList := make(orderedjson.Array, len(descriptions))
	for i, d := range descriptions {
		deviceList[i] = d
	}
	deviceJSON, err := orderedjson.Marshal(deviceList)
	if err != nil {
		return nil, fmt.Errorf("definitionexport: marshal device descriptions: %w", err)
	}
	paramsetJSON, err := orderedjson.Marshal(paramsets)
	if err != nil {
		return nil, fmt.Errorf("definitionexport: marshal paramset descriptions: %w", err)
	}

	zipBytes, err := buildZip(model, deviceJSON, paramsetJSON)
	if err != nil {
		return nil, err
	}
	return &Result{Model: model, Zip: zipBytes}, nil
}

// fetchDescriptions returns the device description first, then each channel
// description in CHILDREN order. Empty CHILDREN entries (observed on
// HmIP-RCV-50) are skipped, matching the Python reference's get_device_with_channels.
func fetchDescriptions(ctx context.Context, rpc OrderedRPC, deviceAddress string) ([]*orderedjson.Object, error) {
	dev, err := callDeviceDescription(ctx, rpc, deviceAddress)
	if err != nil {
		return nil, err
	}
	out := []*orderedjson.Object{dev}
	for _, child := range stringSlice(dev, "CHILDREN") {
		if child == "" {
			continue
		}
		ch, err := callDeviceDescription(ctx, rpc, child)
		if err != nil {
			return nil, err
		}
		out = append(out, ch)
	}
	return out, nil
}

// fetchParamsets fetches every non-LINK paramset description for each
// description. The outer key for an address is always present (even with no
// paramsets); a per-paramset transport error skips that key only, mirroring
// the Python reference's get_paramset_descriptions (which drops a paramset whose fetch
// returns None but keeps an empty {} result).
func fetchParamsets(ctx context.Context, rpc OrderedRPC, descriptions []*orderedjson.Object) *orderedjson.Object {
	out := orderedjson.NewObject(len(descriptions))
	for _, desc := range descriptions {
		address, _ := stringField(desc, "ADDRESS")
		psObj := orderedjson.NewObject(0)
		for _, pkey := range stringSlice(desc, "PARAMSETS") {
			if pkey == string(hmenum.ParamsetKeyLink) {
				continue
			}
			v, err := rpc.CallOrdered(ctx, "getParamsetDescription", []any{address, pkey}, hmenum.CommandPriorityLow)
			if err != nil {
				continue
			}
			if obj, ok := v.(*orderedjson.Object); ok {
				psObj.Set(pkey, obj)
			}
		}
		out.Set(address, psObj)
	}
	return out
}

func callDeviceDescription(ctx context.Context, rpc OrderedRPC, address string) (*orderedjson.Object, error) {
	v, err := rpc.CallOrdered(ctx, "getDeviceDescription", []any{address}, hmenum.CommandPriorityLow)
	if err != nil {
		return nil, fmt.Errorf("definitionexport: getDeviceDescription %s: %w", address, err)
	}
	obj, ok := v.(*orderedjson.Object)
	if !ok {
		return nil, fmt.Errorf("definitionexport: getDeviceDescription %s: unexpected reply %T", address, v)
	}
	return obj, nil
}

// anonymiseDescriptions rewrites ADDRESS on every description, and — exactly as
// the Python reference does — PARENT on channels (to the anonymised device id) or
// CHILDREN on the device (each child anonymised). All addresses share the one
// vcu id, so the cross-references stay internally consistent.
func anonymiseDescriptions(descriptions []*orderedjson.Object, vcu string) {
	for _, desc := range descriptions {
		oldAddr, _ := stringField(desc, "ADDRESS")
		newAddr := anonymiseAddress(oldAddr, vcu)
		desc.Update("ADDRESS", newAddr)

		if parent, ok := stringField(desc, "PARENT"); ok && parent != "" {
			desc.Update("PARENT", deviceOf(newAddr))
			continue
		}
		if children, ok := arrayField(desc, "CHILDREN"); ok && len(children) > 0 {
			anon := make(orderedjson.Array, 0, len(children))
			for _, c := range children {
				cs, _ := c.(string)
				anon = append(anon, anonymiseAddress(cs, vcu))
			}
			desc.Update("CHILDREN", anon)
		}
	}
}

// anonymiseParamsets re-keys the outer address map with anonymised addresses,
// preserving insertion order; the inner paramset content is untouched.
func anonymiseParamsets(paramsets *orderedjson.Object, vcu string) *orderedjson.Object {
	out := orderedjson.NewObject(paramsets.Len())
	for i := range paramsets.Members {
		out.Set(anonymiseAddress(paramsets.Members[i].Key, vcu), paramsets.Members[i].Value)
	}
	return out
}

// anonymiseAddress replaces the device-serial segment (before the first ":")
// with vcu, keeping any channel suffix. "ABC0001234" → "VCU1234567",
// "ABC0001234:3" → "VCU1234567:3".
func anonymiseAddress(address, vcu string) string {
	parts := strings.Split(address, addressSeparator)
	parts[0] = vcu
	return strings.Join(parts, addressSeparator)
}

// deviceOf returns the device portion of a channel address (before ":").
func deviceOf(address string) string {
	if i := strings.Index(address, addressSeparator); i >= 0 {
		return address[:i]
	}
	return address
}

func buildZip(model string, deviceJSON, paramsetJSON []byte) ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, part := range []struct {
		name string
		data []byte
	}{
		{deviceDescriptionsZipDir + "/" + model + ".json", deviceJSON},
		{paramsetDescriptionsZipDir + "/" + model + ".json", paramsetJSON},
	} {
		w, err := zw.Create(part.name)
		if err != nil {
			return nil, fmt.Errorf("definitionexport: zip entry %s: %w", part.name, err)
		}
		if _, err := w.Write(part.data); err != nil {
			return nil, fmt.Errorf("definitionexport: zip write %s: %w", part.name, err)
		}
	}
	if err := zw.Close(); err != nil {
		return nil, fmt.Errorf("definitionexport: zip close: %w", err)
	}
	return buf.Bytes(), nil
}

// DefaultRandomID mirrors the Python reference's `f"VCU{secrets.randbelow(9000000) +
// 1000000}"`: a uniformly random 7-digit id in [1000000, 9999999].
func DefaultRandomID() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(9000000))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("VCU%d", n.Int64()+1000000), nil
}

func stringField(o *orderedjson.Object, key string) (string, bool) {
	v, ok := o.Get(key)
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}

func arrayField(o *orderedjson.Object, key string) (orderedjson.Array, bool) {
	v, ok := o.Get(key)
	if !ok {
		return nil, false
	}
	a, ok := v.(orderedjson.Array)
	return a, ok
}

func stringSlice(o *orderedjson.Object, key string) []string {
	a, ok := arrayField(o, key)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(a))
	for _, e := range a {
		if s, ok := e.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
