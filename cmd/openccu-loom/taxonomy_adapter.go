// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package main

import (
	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/north/rest/handlers"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// registryTaxonomy serves GET /taxonomy from every registered central's
// device-details mirror, with the central's edit and nesting features.
type registryTaxonomy struct {
	reg *central.Registry
}

// Taxonomies implements [handlers.TaxonomySource].
func (t registryTaxonomy) Taxonomies() []handlers.CentralTaxonomy {
	if t.reg == nil {
		return nil
	}
	units := t.reg.List()
	out := make([]handlers.CentralTaxonomy, 0, len(units))
	for _, u := range units {
		if u == nil {
			continue
		}
		ct := handlers.CentralTaxonomy{Central: u.Name()}
		if u.DeviceDetails != nil {
			ct.Taxonomy = u.DeviceDetails.Taxonomy()
		}
		f := u.Features()
		ct.Writable = f.Available(hmenum.FeatureTaxonomyEdit)
		ct.Tree = f.Available(hmenum.FeatureTaxonomyTree)
		out = append(out, ct)
	}
	return out
}
