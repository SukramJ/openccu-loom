// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package cover

import (
	"encoding/json"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	matterparity "github.com/SukramJ/openccu-loom/internal/north/matter/parity"
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

	t.Run("garage", func(t *testing.T) {
		g, _, _ := newGarageRig(t, "HmIP-MOD-HO:1", &stubWriter{})
		srv := g.MatterClusterServers()[0]

		// Neither enum carries a garage value; RollerShade (0) is the
		// neutral default on both attributes (Unknown=255 is avoided —
		// some ecosystems' routine pickers drop Unknown devices).
		if got := readAttrU8(t, srv, attrType, "Type"); got != jsTypeRollershade {
			t.Errorf("Garage Type = %d, want %d (Rollershade)", got, jsTypeRollershade)
		}
		if got := readAttrU8(t, srv, attrEndProductType, "EndProductType"); got != jsEndRollerShade {
			t.Errorf("Garage EndProductType = %d, want %d (RollerShade)", got, jsEndRollerShade)
		}
	})
}
