// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package matterendpoint_test

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"

	matterendpoint "github.com/SukramJ/openccu-loom/internal/store/matterendpoint"
)

// TestSourceKeyRendering pins the rendered form of the source key
// literally, byte for byte.
//
// The rendering is not a debug string. The bridge module hashes it into
// every bridged endpoint's BridgedDeviceBasicInformation.UniqueID
// (Matter §9.13.5.20), and a commissioner caches that value per
// accessory. Change the separator, the field order, the channel's
// formatting or the dp-kind spelling and every already-paired controller
// keeps showing the old accessories: the operator has to remove and
// re-add each bridged device by hand — the same class of breakage the
// Down comment of internal/store/sqlite/migrations/007_matter_endpoints.sql
// describes for reassigned endpoint ids.
//
// A test that reproduced the format with the same Sprintf would pass
// whatever the format became, so the expectations here are literals.
func TestSourceKeyRendering(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		key  matterendpoint.SourceKey
		want string
	}{
		"custom dp": {
			key: matterendpoint.SourceKey{
				CentralName:   "ccu-golden",
				DeviceAddress: "0001PSM01",
				ChannelNo:     3,
				DPKind:        matterendpoint.DPKindCustom,
				DPKey:         "STATE",
			},
			want: "ccu-golden|0001PSM01|3|custom|STATE",
		},
		"empty dp key": {
			key: matterendpoint.SourceKey{
				CentralName:   "GoOtto",
				DeviceAddress: "000C9709AC8CAF",
				ChannelNo:     1,
				DPKind:        matterendpoint.DPKindGeneric,
			},
			want: "GoOtto|000C9709AC8CAF|1|generic|",
		},
		"channel zero is not padded or omitted": {
			key: matterendpoint.SourceKey{
				CentralName:   "ccu1",
				DeviceAddress: "0004SWDO4",
				ChannelNo:     0,
				DPKind:        matterendpoint.DPKindMeasurement,
				DPKey:         "ILLUMINATION",
			},
			want: "ccu1|0004SWDO4|0|measurement|ILLUMINATION",
		},
		"zero value": {
			key:  matterendpoint.SourceKey{},
			want: "||0||",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if got := tc.key.String(); got != tc.want {
				t.Errorf("String() = %q, want %q\n"+
					"This string is hashed into every bridged endpoint's Matter UniqueID. "+
					"Changing it re-fingerprints the whole fleet and every commissioned "+
					"controller has to be re-linked device by device.", got, tc.want)
			}
		})
	}
}

// TestSourceKeyFingerprint pins the UniqueID a rendered key produces,
// end to end, against a hash computed here from the literal string.
//
// TestSourceKeyRendering pins the rendering; this pins that the
// rendering is what the fingerprint is actually taken over, with the
// all-zero boot salt the module mixes in when rotation is off. Together
// they say: this exact byte sequence, and no other, decides what a
// paired controller recognises.
func TestSourceKeyFingerprint(t *testing.T) {
	t.Parallel()
	key := matterendpoint.SourceKey{
		CentralName:   "ccu-golden",
		DeviceAddress: "0001PSM01",
		ChannelNo:     3,
		DPKind:        matterendpoint.DPKindCustom,
		DPKey:         "STATE",
	}
	// Mirrors endpoint.uniqueIDFor: sha256(salt || '|' || rendered key),
	// first 16 bytes as hex, with the 16 zero bytes of an unrotated boot
	// salt. The expected value is the UniqueID of endpoint 2 in
	// internal/north/matteradapter/testdata/topology.golden.json.
	var salt [16]byte
	sum := sha256.Sum256(append(append(salt[:], '|'), []byte(key.String())...))
	const wantFromGolden = "d4a675fb368b2162381d7dbe94eeba74"
	if got := hex.EncodeToString(sum[:16]); got != wantFromGolden {
		t.Errorf("fingerprint of %q = %s, want %s (endpoint 2 of the topology golden)",
			key, got, wantFromGolden)
	}
}

// TestSourceKeyIsComparable pins that the key can be a map key. The
// bridge module keys its per-endpoint state registry and its
// vanished-source set on it, so a field that made the struct
// non-comparable (a slice, a map) would panic at run time during
// assembly rather than fail to compile here.
func TestSourceKeyIsComparable(t *testing.T) {
	t.Parallel()
	seen := map[matterendpoint.SourceKey]int{}
	a := matterendpoint.SourceKey{CentralName: "c", DeviceAddress: "D", ChannelNo: 1, DPKind: matterendpoint.DPKindCustom, DPKey: "K"}
	b := a
	seen[a]++
	seen[b]++
	if seen[a] != 2 {
		t.Errorf("equal keys hashed to different buckets: %v", seen)
	}
	b.ChannelNo = 2
	seen[b]++
	if len(seen) != 2 {
		t.Errorf("distinct keys collapsed into %d entries, want 2", len(seen))
	}
}
