// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package occulited

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// The metadata API, /api/meta/v1: names, enum trees (rooms, functions,
// favourites) and per-namespace metadata of devices and channels,
// addressed by refs "<interface>.<address>". Reads need meta:read,
// writes meta:write. Every changing write answers {"revision": N} with
// ETag N; a write that changes nothing answers 304 with an empty body
// and the revision only in ETag.

// Snapshot is GET /api/meta/v1/snapshot.
type Snapshot struct {
	Format   int               `json:"format"`
	Revision int               `json:"revision"`
	Objects  map[string]Object `json:"objects"`
	Enums    map[string]Enum   `json:"enums"`
}

// Object is the metadata of one device or channel. Enums are full node
// paths including the enum id ("room/eg/wohnzimmer"). Orphaned is set by
// the box when no interface lists the address any more.
type Object struct {
	Name     string                     `json:"name"`
	Enums    []string                   `json:"enums"`
	Meta     map[string]json.RawMessage `json:"meta"`
	Orphaned bool                       `json:"orphaned,omitempty"`
}

// Enum is one enum: a localised name and its node tree.
type Enum struct {
	Name map[string]string `json:"name"`
	Tree []Node            `json:"tree"`
}

// Node is one enum tree node.
type Node struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Icon     string `json:"icon,omitempty"`
	Children []Node `json:"children,omitempty"`
}

// ObjectsQuery filters GET /objects: Enum is a node path (a subtree
// query), Orphaned restricts to orphaned or non-orphaned objects.
type ObjectsQuery struct {
	Enum     string
	Orphaned *bool
}

// ObjectsPage is GET /objects.
type ObjectsPage struct {
	Revision int               `json:"revision"`
	Objects  map[string]Object `json:"objects"`
}

// ObjectAnswer is GET /objects/{ref}.
type ObjectAnswer struct {
	Revision int    `json:"revision"`
	Ref      string `json:"ref"`
	Object   Object `json:"object"`
}

// EnumsAnswer is GET /enums.
type EnumsAnswer struct {
	Revision int             `json:"revision"`
	Enums    map[string]Enum `json:"enums"`
}

// TreeAnswer is GET /enums/{enum}/tree.
type TreeAnswer struct {
	Revision int               `json:"revision"`
	Enum     string            `json:"enum"`
	Name     map[string]string `json:"name"`
	Tree     []Node            `json:"tree"`
}

// Mutation is the outcome of a metadata write. Changed is false for the
// box's 304 answer (nothing changed); Revision is the store revision
// either way.
type Mutation struct {
	Revision int
	Changed  bool
}

// WriteOptions guards a write: IfMatch sends If-Match with the revision
// the write is based on; the box answers 409 revision-conflict when the
// store moved on.
type WriteOptions struct {
	IfMatch *int
}

// ObjectWrite is the PUT body: it replaces the object; missing optional
// members reset.
type ObjectWrite struct {
	Name  string                     `json:"name"`
	Enums []string                   `json:"enums,omitempty"`
	Meta  map[string]json.RawMessage `json:"meta,omitempty"`
}

// ObjectPatch is the PATCH body: every set member changes, Enums
// replaces the whole list, Meta merges per namespace (a namespace set to
// the JSON null is deleted). A PATCH with a name creates a missing
// object.
type ObjectPatch struct {
	Name  *string                    `json:"name,omitempty"`
	Enums *[]string                  `json:"enums,omitempty"`
	Meta  map[string]json.RawMessage `json:"meta,omitempty"`
}

// BulkRequest is POST /objects:bulk, applied under one revision.
type BulkRequest struct {
	Set    map[string]ObjectPatch `json:"set,omitempty"`
	Delete []string               `json:"delete,omitempty"`
}

// NodeCreate is POST /enums/{enum}/nodes. Parent is a full path, or nil
// for the root.
type NodeCreate struct {
	Parent   *string `json:"parent"`
	ID       string  `json:"id"`
	Name     string  `json:"name"`
	Icon     string  `json:"icon,omitempty"`
	Position *int    `json:"position,omitempty"`
}

// NodePatch is PATCH /enums/{enum}/nodes/{path}. Move with Parent moves
// the node: Parent "" moves it to the root (sent as null).
type NodePatch struct {
	Name     *string
	Icon     *string
	Move     bool
	Parent   string
	Position *int
}

// MarshalJSON renders the parent as absent, null or a path.
func (p NodePatch) MarshalJSON() ([]byte, error) {
	type wire struct {
		Name     *string          `json:"name,omitempty"`
		Icon     *string          `json:"icon,omitempty"`
		Parent   *json.RawMessage `json:"parent,omitempty"`
		Position *int             `json:"position,omitempty"`
	}
	w := wire{Name: p.Name, Icon: p.Icon, Position: p.Position}
	if p.Move {
		parent := json.RawMessage("null")
		if p.Parent != "" {
			b, err := json.Marshal(p.Parent)
			if err != nil {
				return nil, err
			}
			parent = b
		}
		w.Parent = &parent
	}
	return json.Marshal(w)
}

// EscapeRef escapes a ref for a URL path; the box wants ":" encoded too
// ("BidCos-RF.JEQ0230153%3A1").
func EscapeRef(ref string) string {
	return strings.ReplaceAll(url.PathEscape(ref), ":", "%3A")
}

// escapeNodePath escapes every segment of a node path.
func escapeNodePath(p string) string {
	segs := strings.Split(p, "/")
	for i, s := range segs {
		segs[i] = url.PathEscape(s)
	}
	return strings.Join(segs, "/")
}

const metaBase = "/api/meta/v1"

// Snapshot reads the whole store.
func (c *Client) Snapshot(ctx context.Context) (Snapshot, error) {
	return get[Snapshot](ctx, c, metaBase+"/snapshot", nil)
}

// Objects reads GET /objects.
func (c *Client) Objects(ctx context.Context, oq ObjectsQuery) (ObjectsPage, error) {
	q := url.Values{}
	if oq.Enum != "" {
		q.Set("enum", oq.Enum)
	}
	if oq.Orphaned != nil {
		q.Set("orphaned", strconv.FormatBool(*oq.Orphaned))
	}
	return get[ObjectsPage](ctx, c, metaBase+"/objects", q)
}

// Object reads one object; an unknown ref is an *APIError with code
// unknown-object (404).
func (c *Client) Object(ctx context.Context, ref string) (ObjectAnswer, error) {
	return get[ObjectAnswer](ctx, c, metaBase+"/objects/"+EscapeRef(ref), nil)
}

// Enums reads every enum.
func (c *Client) Enums(ctx context.Context) (EnumsAnswer, error) {
	return get[EnumsAnswer](ctx, c, metaBase+"/enums", nil)
}

// EnumTree reads one enum's tree.
func (c *Client) EnumTree(ctx context.Context, enum string) (TreeAnswer, error) {
	return get[TreeAnswer](ctx, c, metaBase+"/enums/"+url.PathEscape(enum)+"/tree", nil)
}

// PutObject replaces an object.
func (c *Client) PutObject(ctx context.Context, ref string, w ObjectWrite, o WriteOptions) (Mutation, error) {
	return mutate(ctx, c, http.MethodPut, metaBase+"/objects/"+EscapeRef(ref), nil, w, o)
}

// PatchObject changes an object (creating it when the patch has a name).
func (c *Client) PatchObject(ctx context.Context, ref string, p ObjectPatch, o WriteOptions) (Mutation, error) {
	return mutate(ctx, c, http.MethodPatch, metaBase+"/objects/"+EscapeRef(ref), nil, p, o)
}

// DeleteObject removes an object.
func (c *Client) DeleteObject(ctx context.Context, ref string, o WriteOptions) (Mutation, error) {
	return mutate(ctx, c, http.MethodDelete, metaBase+"/objects/"+EscapeRef(ref), nil, emptyBody{}, o)
}

// Bulk applies sets and deletes under one revision.
func (c *Client) Bulk(ctx context.Context, b BulkRequest, o WriteOptions) (Mutation, error) {
	return mutate(ctx, c, http.MethodPost, metaBase+"/objects:bulk", nil, b, o)
}

// enumCreate is the POST /enums body.
type enumCreate struct {
	ID   string            `json:"id"`
	Name map[string]string `json:"name"`
}

// enumPatch is the PATCH /enums/{enum} body.
type enumPatch struct {
	Name map[string]string `json:"name"`
}

// CreateEnum adds an enum.
func (c *Client) CreateEnum(ctx context.Context, id string, name map[string]string, o WriteOptions) (Mutation, error) {
	return mutate(ctx, c, http.MethodPost, metaBase+"/enums", nil, enumCreate{ID: id, Name: name}, o)
}

// PatchEnum renames an enum.
func (c *Client) PatchEnum(ctx context.Context, id string, name map[string]string, o WriteOptions) (Mutation, error) {
	return mutate(ctx, c, http.MethodPatch, metaBase+"/enums/"+url.PathEscape(id), nil, enumPatch{Name: name}, o)
}

// DeleteEnum removes an enum; detach strips its paths from member
// objects instead of refusing with has-members.
func (c *Client) DeleteEnum(ctx context.Context, id string, detach bool, o WriteOptions) (Mutation, error) {
	return mutate(ctx, c, http.MethodDelete, metaBase+"/enums/"+url.PathEscape(id), detachQuery(detach), emptyBody{}, o)
}

// CreateNode adds a node to an enum.
func (c *Client) CreateNode(ctx context.Context, enum string, n NodeCreate, o WriteOptions) (Mutation, error) {
	return mutate(ctx, c, http.MethodPost, metaBase+"/enums/"+url.PathEscape(enum)+"/nodes", nil, n, o)
}

// PatchNode changes or moves the node at path (relative to the enum).
func (c *Client) PatchNode(ctx context.Context, enum, path string, p NodePatch, o WriteOptions) (Mutation, error) {
	return mutate(ctx, c, http.MethodPatch, metaBase+"/enums/"+url.PathEscape(enum)+"/nodes/"+escapeNodePath(path), nil, p, o)
}

// DeleteNode removes the node at path and its subtree; detach strips
// the subtree's paths from member objects instead of refusing with
// has-members.
func (c *Client) DeleteNode(ctx context.Context, enum, path string, detach bool, o WriteOptions) (Mutation, error) {
	return mutate(ctx, c, http.MethodDelete, metaBase+"/enums/"+url.PathEscape(enum)+"/nodes/"+escapeNodePath(path),
		detachQuery(detach), emptyBody{}, o)
}

func detachQuery(detach bool) url.Values {
	if !detach {
		return nil
	}
	return url.Values{"members": {"detach"}}
}

// emptyBody renders "{}", the body of a write that sends nothing.
type emptyBody struct{}

type revisionBody struct {
	Revision *int `json:"revision"`
}

// mutate sends a metadata write and reads the revision from the answer:
// the body for 200/201, the ETag for 304. B is the request body type.
func mutate[B any](ctx context.Context, c *Client, method, path string, q url.Values, body B, o WriteOptions) (Mutation, error) {
	raw, err := marshal(body)
	if err != nil {
		return Mutation{}, err
	}
	r := request{method: method, path: path, query: q, body: raw, allow304: true, header: http.Header{}}
	if o.IfMatch != nil {
		r.header.Set("If-Match", strconv.Itoa(*o.IfMatch))
	}
	resp, err := c.send(ctx, c.calls, r)
	if err != nil {
		return Mutation{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotModified {
		rev, err := etagRevision(resp.Header.Get("ETag"))
		if err != nil {
			return Mutation{}, fmt.Errorf("occulited: %s %s: 304: %w", method, path, err)
		}
		return Mutation{Revision: rev, Changed: false}, nil
	}
	ans, err := decodeBody[revisionBody](resp, r)
	if err != nil {
		return Mutation{}, err
	}
	if ans.Revision == nil {
		return Mutation{}, fmt.Errorf("occulited: %s %s: %w: answer without revision", method, path, ErrProtocol)
	}
	return Mutation{Revision: *ans.Revision, Changed: true}, nil
}

// etagRevision reads a revision from an ETag, tolerating quotes and a
// weak marker.
func etagRevision(etag string) (int, error) {
	v := strings.Trim(strings.TrimPrefix(strings.TrimSpace(etag), "W/"), `"`)
	if v == "" {
		return 0, fmt.Errorf("%w: no ETag", ErrProtocol)
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("%w: ETag %q is not a revision", ErrProtocol, etag)
	}
	return n, nil
}
