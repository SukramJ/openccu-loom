// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package handlers

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/model/hub"
	"github.com/SukramJ/openccu-loom/internal/model/taxonomy"
	"github.com/SukramJ/openccu-loom/internal/north/rest/problem"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
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

// TaxonomyNodeAdmin edits taxonomy nodes. *adapter.TaxonomyAdmin
// satisfies it.
type TaxonomyNodeAdmin interface {
	CreateNode(ctx context.Context, central, enum, parent, name string) (string, error)
	UpdateNode(ctx context.Context, central, enum, path string, name, parent *string, position *int) error
	DeleteNode(ctx context.Context, central, enum, path string) error
}

// TaxonomyNodeCreateRequest is the body of POST /taxonomy/{central}/{enum}/nodes.
type TaxonomyNodeCreateRequest struct {
	// ParentPath is the parent node's path inside the enum; absent or
	// empty creates a root node.
	ParentPath string `json:"parent_path,omitempty"`
	Name       string `json:"name"`
}

// TaxonomyNodeCreated is the answer to a node create.
type TaxonomyNodeCreated struct {
	Path string `json:"path"`
}

// TaxonomyNodeUpdateRequest is the body of PATCH
// /taxonomy/{central}/{enum}/nodes?path=<node path>: a new name, and/or a new
// parent (empty string: the root) and position among the siblings.
type TaxonomyNodeUpdateRequest struct {
	Name       *string `json:"name,omitempty"`
	ParentPath *string `json:"parent_path,omitempty"`
	Position   *int    `json:"position,omitempty"`
}

// CreateTaxonomyNode adds a node.
func CreateTaxonomyNode(admin TaxonomyNodeAdmin, rec audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if admin == nil {
			problem.Write(w, http.StatusServiceUnavailable, problem.New(problem.TypeServiceUnready, r, "Taxonomy admin unavailable", ""))
			return
		}
		var req TaxonomyNodeCreateRequest
		if err := DecodeJSON(r, &req); err != nil {
			problem.Write(w, DecodeJSONStatus(err), problem.New(problem.TypeBadRequest, r, "Invalid JSON", err.Error()))
			return
		}
		if strings.TrimSpace(req.Name) == "" {
			problem.Write(w, http.StatusUnprocessableEntity, problem.New(problem.TypeValidation, r, "Name required", ""))
			return
		}
		central, enum := chi.URLParam(r, "central"), chi.URLParam(r, "enum")
		path, err := admin.CreateNode(r.Context(), central, enum, req.ParentPath, req.Name)
		if err != nil {
			writeTaxonomyError(w, r, err)
			return
		}
		groupAudit(rec, r, "taxonomy.create "+central+" "+enum+"/"+path)
		JSON(w, http.StatusCreated, TaxonomyNodeCreated{Path: path})
	}
}

// UpdateTaxonomyNode renames and/or moves a node.
func UpdateTaxonomyNode(admin TaxonomyNodeAdmin, rec audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if admin == nil {
			problem.Write(w, http.StatusServiceUnavailable, problem.New(problem.TypeServiceUnready, r, "Taxonomy admin unavailable", ""))
			return
		}
		var req TaxonomyNodeUpdateRequest
		if err := DecodeJSON(r, &req); err != nil {
			problem.Write(w, DecodeJSONStatus(err), problem.New(problem.TypeBadRequest, r, "Invalid JSON", err.Error()))
			return
		}
		if req.Name == nil && req.ParentPath == nil && req.Position == nil {
			problem.Write(w, http.StatusUnprocessableEntity, problem.New(problem.TypeValidation, r, "No patchable field supplied", ""))
			return
		}
		if req.Name != nil && strings.TrimSpace(*req.Name) == "" {
			problem.Write(w, http.StatusUnprocessableEntity, problem.New(problem.TypeValidation, r, "Name must not be empty", ""))
			return
		}
		central, enum, path := chi.URLParam(r, "central"), chi.URLParam(r, "enum"), r.URL.Query().Get("path")
		if path == "" {
			problem.Write(w, http.StatusUnprocessableEntity, problem.New(problem.TypeValidation, r, "Node path required", "the path query parameter names the node"))
			return
		}
		if err := admin.UpdateNode(r.Context(), central, enum, path, req.Name, req.ParentPath, req.Position); err != nil {
			writeTaxonomyError(w, r, err)
			return
		}
		groupAudit(rec, r, "taxonomy.update "+central+" "+enum+"/"+path)
		w.WriteHeader(http.StatusNoContent)
	}
}

// DeleteTaxonomyNode removes a node and its subtree; the addresses
// assigned to them lose the assignment and keep existing.
func DeleteTaxonomyNode(admin TaxonomyNodeAdmin, rec audit.Recorder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if admin == nil {
			problem.Write(w, http.StatusServiceUnavailable, problem.New(problem.TypeServiceUnready, r, "Taxonomy admin unavailable", ""))
			return
		}
		central, enum, path := chi.URLParam(r, "central"), chi.URLParam(r, "enum"), r.URL.Query().Get("path")
		if path == "" {
			problem.Write(w, http.StatusUnprocessableEntity, problem.New(problem.TypeValidation, r, "Node path required", "the path query parameter names the node"))
			return
		}
		if err := admin.DeleteNode(r.Context(), central, enum, path); err != nil {
			writeTaxonomyError(w, r, err)
			return
		}
		groupAudit(rec, r, "taxonomy.delete "+central+" "+enum+"/"+path)
		w.WriteHeader(http.StatusNoContent)
	}
}

// writeTaxonomyError maps a node edit failure: an unknown central or node
// is 404, a request the system cannot take (a nested node on a flat
// system, an unknown enum) is 422; a feature the central lacks is the
// feature_unavailable answer.
func writeTaxonomyError(w http.ResponseWriter, r *http.Request, err error) {
	if problem.WriteFeatureUnavailable(w, r, err) {
		return
	}
	switch {
	case errors.Is(err, hub.ErrCentralNotFound), errors.Is(err, hub.ErrRoomNotFound), errors.Is(err, hub.ErrFunctionNotFound):
		problem.Write(w, http.StatusNotFound, problem.New(problem.TypeNotFound, r, "Not found", err.Error()))
	case errors.Is(err, hub.ErrCentralAmbiguous):
		problem.Write(w, http.StatusBadRequest, problem.New(problem.TypeValidation, r, "Central name required", err.Error()))
	case errors.Is(err, hmerr.ErrValidation):
		problem.Write(w, http.StatusUnprocessableEntity, problem.New(problem.TypeValidation, r, "Invalid node change", err.Error()))
	default:
		writeServerError(w, r, http.StatusBadGateway, problem.TypeUpstreamUnavailable, "Taxonomy change failed", err)
	}
}
