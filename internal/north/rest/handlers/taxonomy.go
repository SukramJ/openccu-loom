// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package handlers

import (
	"net/http"
	"sort"

	"github.com/SukramJ/openccu-loom/internal/model/taxonomy"
	"github.com/SukramJ/openccu-loom/internal/north/rest/problem"
)

// CentralTaxonomy is one central's taxonomy as the source holds it, with
// whether the central lets a client edit its nodes and nest them.
type CentralTaxonomy struct {
	Central  string
	Taxonomy *taxonomy.Taxonomy
	Writable bool
	Tree     bool
}

// TaxonomySource yields every central's taxonomy.
type TaxonomySource interface {
	Taxonomies() []CentralTaxonomy
}

// TaxonomyResponse is `GET /api/v1/taxonomy`.
type TaxonomyResponse struct {
	Centrals []TaxonomyCentral `json:"centrals"`
}

// TaxonomyCentral is one central's enums.
type TaxonomyCentral struct {
	Central string `json:"central"`
	// Revision is the source's revision counter; 0 for a system without
	// one (a CCU).
	Revision uint64 `json:"revision"`
	// Writable says whether nodes can be created, renamed and deleted.
	Writable bool `json:"writable"`
	// Tree says whether nodes can nest; a CCU's rooms and functions are
	// flat.
	Tree  bool           `json:"tree"`
	Enums []TaxonomyEnum `json:"enums"`
}

// TaxonomyEnum is one enum with its node tree.
type TaxonomyEnum struct {
	ID    string            `json:"id"`
	Names map[string]string `json:"names,omitempty"`
	Nodes []TaxonomyNode    `json:"nodes"`
}

// TaxonomyNode is one node; Path is its path inside the enum.
type TaxonomyNode struct {
	ID       string         `json:"id"`
	Path     string         `json:"path"`
	Name     string         `json:"name"`
	Icon     string         `json:"icon,omitempty"`
	Children []TaxonomyNode `json:"children,omitempty"`
}

// GetTaxonomy serves every central's taxonomy, or one central's with
// `?central=`. It is the only surface that shows nodes nothing is
// assigned to, so a tree picker reads it rather than the device list.
func GetTaxonomy(src TaxonomySource) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		want := r.URL.Query().Get("central")
		out, ok := BuildTaxonomyResponse(src, want)
		if !ok {
			problem.Write(w, http.StatusNotFound, problem.New(problem.TypeNotFound, r, "Unknown central", want))
			return
		}
		JSON(w, http.StatusOK, out)
	}
}

// BuildTaxonomyResponse projects every central's taxonomy, or only
// central's when it is set; ok is false for a named central the source
// does not know.
func BuildTaxonomyResponse(src TaxonomySource, central string) (TaxonomyResponse, bool) {
	out := TaxonomyResponse{Centrals: []TaxonomyCentral{}}
	if src != nil {
		for _, ct := range src.Taxonomies() {
			if central != "" && ct.Central != central {
				continue
			}
			out.Centrals = append(out.Centrals, toTaxonomyCentral(ct))
		}
	}
	return out, central == "" || len(out.Centrals) > 0
}

func toTaxonomyCentral(ct CentralTaxonomy) TaxonomyCentral {
	c := TaxonomyCentral{Central: ct.Central, Writable: ct.Writable, Tree: ct.Tree, Enums: []TaxonomyEnum{}}
	if ct.Taxonomy == nil {
		return c
	}
	c.Revision = ct.Taxonomy.Revision
	ids := make([]string, 0, len(ct.Taxonomy.Enums))
	for id := range ct.Taxonomy.Enums {
		ids = append(ids, string(id))
	}
	sort.Strings(ids)
	for _, id := range ids {
		e := ct.Taxonomy.Enums[taxonomy.EnumID(id)]
		c.Enums = append(c.Enums, TaxonomyEnum{ID: id, Names: e.Names, Nodes: toTaxonomyNodes(e.Roots, "")})
	}
	return c
}

func toTaxonomyNodes(in []*taxonomy.Node, prefix string) []TaxonomyNode {
	out := make([]TaxonomyNode, 0, len(in))
	for _, n := range in {
		path := n.ID
		if prefix != "" {
			path = prefix + "/" + n.ID
		}
		node := TaxonomyNode{ID: n.ID, Path: path, Name: n.Name, Icon: n.Icon}
		if len(n.Children) > 0 {
			node.Children = toTaxonomyNodes(n.Children, path)
		}
		out = append(out, node)
	}
	return out
}
