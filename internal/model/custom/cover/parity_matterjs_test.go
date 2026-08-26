// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package cover

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	matterparity "github.com/SukramJ/openccu-loom/internal/north/matter/parity"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// TestParityMatterJS_CoverClusterRevisions pins every cluster revision
// implemented by the cover package against matter.js HEAD so a stale
// revision does not pass review unnoticed.

type matterClusterEntry struct {
	ID       uint32 `json:"id"`
	Name     string `json:"name"`
	Revision uint16 `json:"revision"`
}

type matterSchema struct {
	Clusters []matterClusterEntry `json:"clusters"`
}

func loadMatterSchemaT(t *testing.T) *matterSchema {
	t.Helper()

	var s matterSchema
	if err := json.Unmarshal(matterparity.SchemaJSON(), &s); err != nil {
		t.Fatalf("unmarshal embedded matter-schema-snapshot.json: %v", err)
	}

	return &s
}

func clusterByID(s *matterSchema, id uint32) (matterClusterEntry, bool) {
	for _, c := range s.Clusters {
		if c.ID == id {
			return c, true
		}
	}

	return matterClusterEntry{}, false
}

func TestParityMatterJS_CoverClusterRevisions(t *testing.T) {
	t.Parallel()

	schema := loadMatterSchemaT(t)
	cases := []struct {
		id           uint32
		name         string
		codeRevision uint16
	}{
		{matterClusterWindowCovering, "WindowCovering", matterWindowCoveringClusterRevision},
	}

	for _, c := range cases {
		js, ok := clusterByID(schema, c.id)
		if !ok {
			t.Errorf("matter.js schema has no cluster 0x%04X (%s)", c.id, c.name)

			continue
		}

		t.Run(js.Name, func(t *testing.T) {
			t.Parallel()

			if c.codeRevision != js.Revision {
				t.Errorf("code revision %d != matter.js %d for %s (0x%04X)",
					c.codeRevision, js.Revision, js.Name, js.ID)
			}
		})
	}
}

// TestParityMatterJS_WindowCoveringTypeAndEndProductType pins the Type
// (0x0000) and EndProductType (0x000D) values every cover projection
// reports against matter.js HEAD
// packages/model/src/standard/elements/window-covering-cluster.element.ts
// (TypeEnum :152-162, EndProductTypeEnum :166-192). The expected
// literals below are copied from that file — not from the production
// constants — so a drifted constant fails here. The two enums share a
// numeric space with unrelated meanings; reusing a Type code as
// EndProductType reports a different product entirely.
func TestParityMatterJS_WindowCoveringTypeAndEndProductType(t *testing.T) {
	// matter.js TypeEnum (window-covering-cluster.element.ts:152-162).
	const (
		jsTypeRollershade   uint8 = 0x0
		jsTypeDrapery       uint8 = 0x4
		jsTypeAwning        uint8 = 0x5
		jsTypeShutter       uint8 = 0x6
		jsTypeTiltBlindLift uint8 = 0x8
	)
	// matter.js EndProductTypeEnum (window-covering-cluster.element.ts:166-192).
	const (
		jsEndRollerShade        uint8 = 0x0
		jsEndInteriorBlind      uint8 = 0xA
		jsEndCentralCurtain     uint8 = 0x10
		jsEndRollerShutter      uint8 = 0x11
		jsEndAwningTerracePatio uint8 = 0x13
	)

	const (
		attrType           uint32 = 0x0000
		attrEndProductType uint32 = 0x000D
	)

	readAttrU8 := func(t *testing.T, srv interfaces.MatterClusterServer, attrID uint32, name string) uint8 {
		t.Helper()
		v, ok := srv.MatterRead(attrID)
		if !ok {
			t.Fatalf("%s not readable", name)
		}
		u, isU8 := v.(uint8)
		if !isU8 {
			t.Fatalf("%s type = %T, want uint8", name, v)
		}

		return u
	}

	coverCases := []struct {
		name     string
		variant  CoverVariant
		wantType uint8
		wantEnd  uint8
	}{
		{"shutter", VariantShutter, jsTypeShutter, jsEndRollerShutter},
		{"window", VariantWindow, jsTypeShutter, jsEndRollerShutter},
		{"awning", VariantAwning, jsTypeAwning, jsEndAwningTerracePatio},
		{"curtain", VariantCurtain, jsTypeDrapery, jsEndCentralCurtain},
		{"shade", VariantShade, jsTypeRollershade, jsEndRollerShade},
		{"damper", VariantDamper, jsTypeRollershade, jsEndRollerShade},
	}
	for _, tc := range coverCases {
		t.Run("cover/"+tc.name, func(t *testing.T) {
			c, _, _ := newRig(t, "HmIP-BROLL:3", &stubWriter{}, custom.CoverCapabilities{})
			c.Variant = tc.variant
			srv := c.MatterClusterServers()[0]

			if got := readAttrU8(t, srv, attrType, "Type"); got != tc.wantType {
				t.Errorf("Type = %d, want %d (matter.js TypeEnum)", got, tc.wantType)
			}
			if got := readAttrU8(t, srv, attrEndProductType, "EndProductType"); got != tc.wantEnd {
				t.Errorf("EndProductType = %d, want %d (matter.js EndProductTypeEnum)", got, tc.wantEnd)
			}
		})
	}

	t.Run("blind", func(t *testing.T) {
		b := newBlindRig(t, "VCU3560967:1", &putWriter{}, custom.CoverCapabilities{SupportsTilt: true}, BlindKindHM)
		srv := b.MatterClusterServers()[0]

		if got := readAttrU8(t, srv, attrType, "Type"); got != jsTypeTiltBlindLift {
			t.Errorf("Blind Type = %d, want %d (TiltBlindLift)", got, jsTypeTiltBlindLift)
		}
		if got := readAttrU8(t, srv, attrEndProductType, "EndProductType"); got != jsEndInteriorBlind {
			t.Errorf("Blind EndProductType = %d, want %d (InteriorBlind)", got, jsEndInteriorBlind)
		}
	})
}

// TestParityMatterJS_WindowCoveringCommandLists pins the command lists
// every cover projection advertises against matter.js HEAD
// packages/model/src/standard/elements/window-covering-cluster.element.ts:85-105:
// UpOrOpen (0x00), DownOrClose (0x01) and StopMotion (0x02) carry
// conformance "M", GoToLiftPercentage (0x05) "LF & PA_LF" and
// GoToTiltPercentage (0x08) "TL & PA_TL" — the feature pairs each
// projection advertises in its FeatureMap.
//
// The dispatcher answers AcceptedCommandList (0xFFF9) from this
// capability and falls back to an empty list without it, and a
// controller derives its write capability from that attribute: an empty
// list turns a blind into a read-only sensor, because Mode (0x0017) is
// the cluster's only writable attribute.
//
// The second half of each case is the round-trip: a command is
// advertised exactly when MatterInvoke handles it, so the declared
// surface cannot drift away from the implemented one.
func TestParityMatterJS_WindowCoveringCommandLists(t *testing.T) {
	t.Parallel()

	// Command IDs, verbatim from window-covering-cluster.element.ts:85-105.
	const (
		jsCmdUpOrOpen           uint32 = 0x0
		jsCmdDownOrClose        uint32 = 0x1
		jsCmdStopMotion         uint32 = 0x2
		jsCmdGoToLiftPercentage uint32 = 0x5
		jsCmdGoToTiltPercentage uint32 = 0x8
	)
	clusterCommands := []uint32{
		jsCmdUpOrOpen, jsCmdDownOrClose, jsCmdStopMotion,
		jsCmdGoToLiftPercentage, jsCmdGoToTiltPercentage,
	}
	// GoToTiltPercentage is gated on TL & PA_TL, which only the blind
	// projection advertises.
	liftOnly := []uint32{jsCmdUpOrOpen, jsCmdDownOrClose, jsCmdStopMotion, jsCmdGoToLiftPercentage}

	cases := []struct {
		name   string
		server func(t *testing.T) interfaces.MatterClusterServer
		want   []uint32
	}{
		{
			name: "cover",
			server: func(t *testing.T) interfaces.MatterClusterServer {
				t.Helper()
				c, _, _ := newRig(t, "HmIP-BROLL:3", &stubWriter{}, custom.CoverCapabilities{SupportsStop: true})

				return c.MatterClusterServers()[0]
			},
			want: liftOnly,
		},
		{
			name: "blind",
			server: func(t *testing.T) interfaces.MatterClusterServer {
				t.Helper()
				b := newBlindRig(t, "VCU3560967:1", &putWriter{}, custom.CoverCapabilities{SupportsTilt: true, SupportsStop: true}, BlindKindHM)

				return b.MatterClusterServers()[0]
			},
			want: clusterCommands,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			srv := tc.server(t)
			lister, ok := srv.(interfaces.MatterClusterCommandLister)
			if !ok {
				t.Fatalf("%T does not implement MatterClusterCommandLister — AcceptedCommandList would be empty", srv)
			}

			advertised := make(map[uint32]bool, len(lister.MatterAcceptedCommands()))
			for _, id := range lister.MatterAcceptedCommands() {
				advertised[id] = true
			}
			for _, id := range tc.want {
				if !advertised[id] {
					t.Errorf("MatterAcceptedCommands() = %v, missing 0x%02X", lister.MatterAcceptedCommands(), id)
				}
			}
			if len(advertised) != len(tc.want) {
				t.Errorf("MatterAcceptedCommands() = %v, want exactly %v", lister.MatterAcceptedCommands(), tc.want)
			}
			// Every WindowCovering command answers with a plain status,
			// so nothing is generated back (element.ts:85-105).
			if got := lister.MatterGeneratedCommands(); len(got) != 0 {
				t.Errorf("MatterGeneratedCommands() = %v, want empty", got)
			}

			for _, id := range clusterCommands {
				_, err := srv.MatterInvoke(context.Background(), id, uint16(5000), hmenum.CommandPriorityHigh)
				handled := !errors.Is(err, errMatterUnknownCommand)
				if handled != advertised[id] {
					t.Errorf("command 0x%02X: MatterInvoke handles=%v, advertised=%v", id, handled, advertised[id])
				}
			}
		})
	}
}
