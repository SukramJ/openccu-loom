// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package litefake

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unicode"
)

// Metadata API.
//
// The box keeps names, enum trees (rooms, functions, favourites) and
// free-form per-namespace metadata for devices and channels, addressed
// by "<interface>.<address>" refs. Every mutation bumps one store-wide
// revision and emits change events carrying it; a mutation that
// changes nothing answers 304 and bumps nothing.

// Validation limits of the metadata store.
const (
	metaNameMaxBytes = 255
	metaIDMaxLen     = 32
	metaMaxDepth     = 8
	metaMaxMetaBytes = 16 << 10
	metaBodyLimit    = 1 << 20
)

// Document is a whole metadata store in the snapshot shape. It seeds a
// fake ([Options.Meta], [MetaHandle.Seed]) and is what
// [MetaHandle.Snapshot] returns.
type Document struct {
	Format   int               `json:"format"`
	Revision int               `json:"revision"`
	Objects  map[string]Object `json:"objects"`
	Enums    map[string]Enum   `json:"enums"`
}

// Object is the metadata of one device or channel. Enums holds full
// node paths ("room/eg/wohnzimmer").
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

// MetaEvent is one change-stream event. One mutation may emit several
// events that share a revision.
type MetaEvent struct {
	Revision int             `json:"revision"`
	Kind     string          `json:"kind"`
	Ref      string          `json:"ref,omitempty"`
	Enum     string          `json:"enum,omitempty"`
	Path     string          `json:"path,omitempty"`
	From     string          `json:"from,omitempty"`
	To       string          `json:"to,omitempty"`
	Value    json.RawMessage `json:"value,omitempty"`
}

// MetaError is a refused metadata mutation or read: the HTTP status and
// the stable error code the box answers with.
type MetaError struct {
	Status  int
	Code    string
	Message string
	Refs    []string
}

func (e *MetaError) Error() string { return e.Code + ": " + e.Message }

// metaErrf builds a MetaError; args are the fmt arguments of the
// message, hence the any.
func metaErrf(status int, code, format string, args ...any) *MetaError {
	return &MetaError{Status: status, Code: code, Message: fmt.Sprintf(format, args...)}
}

// mnode is the mutable tree node.
type mnode struct {
	id       string
	name     string
	icon     string
	children []*mnode
}

// menum is the mutable enum.
type menum struct {
	name map[string]string
	tree []*mnode
}

// metaState is the store content a mutation works on.
type metaState struct {
	objects map[string]*Object
	enums   map[string]*menum
}

// metaSub is one open change stream.
type metaSub struct {
	queue    chan MetaEvent
	kill     chan struct{}
	killOnce sync.Once
}

func (s *metaSub) stop() { s.killOnce.Do(func() { close(s.kill) }) }

// metaStore is the metadata store plus its retained event log and open
// change streams, all under one lock so a stream that attaches sees
// every event exactly once.
type metaStore struct {
	mu             sync.Mutex
	revision       int
	state          metaState
	log            []MetaEvent
	logMax         int
	droppedThrough int
	queue          int
	subs           map[*metaSub]struct{}
}

func newMetaStore(doc *Document, logMax, queue int) *metaStore {
	s := &metaStore{
		state:  metaState{objects: map[string]*Object{}, enums: map[string]*menum{}},
		logMax: logMax,
		queue:  queue,
		subs:   map[*metaSub]struct{}{},
	}
	if doc != nil {
		s.state = stateFromDocument(*doc)
		s.revision = doc.Revision
		s.droppedThrough = doc.Revision
	}
	return s
}

// stateFromDocument builds a mutable state from a document.
func stateFromDocument(doc Document) metaState {
	st := metaState{objects: map[string]*Object{}, enums: map[string]*menum{}}
	for ref, o := range doc.Objects {
		st.objects[ref] = cloneObject(o)
	}
	for id, e := range doc.Enums {
		st.enums[id] = &menum{name: cloneNames(e.Name), tree: nodesFrom(e.Tree)}
	}
	return st
}

func nodesFrom(nodes []Node) []*mnode {
	out := make([]*mnode, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, &mnode{id: n.ID, name: n.Name, icon: n.Icon, children: nodesFrom(n.Children)})
	}
	return out
}

func nodesTo(nodes []*mnode) []Node {
	out := make([]Node, 0, len(nodes))
	for _, n := range nodes {
		node := Node{ID: n.id, Name: n.name, Icon: n.icon}
		if len(n.children) > 0 {
			node.Children = nodesTo(n.children)
		}
		out = append(out, node)
	}
	return out
}

func cloneNames(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func cloneObject(o Object) *Object {
	c := Object{Name: o.Name, Orphaned: o.Orphaned, Enums: append([]string{}, o.Enums...), Meta: map[string]json.RawMessage{}}
	for k, v := range o.Meta {
		c.Meta[k] = append(json.RawMessage(nil), v...)
	}
	return &c
}

func cloneTree(nodes []*mnode) []*mnode {
	out := make([]*mnode, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, &mnode{id: n.id, name: n.name, icon: n.icon, children: cloneTree(n.children)})
	}
	return out
}

func (st metaState) clone() metaState {
	c := metaState{objects: make(map[string]*Object, len(st.objects)), enums: make(map[string]*menum, len(st.enums))}
	for ref, o := range st.objects {
		c.objects[ref] = cloneObject(*o)
	}
	for id, e := range st.enums {
		c.enums[id] = &menum{name: cloneNames(e.name), tree: cloneTree(e.tree)}
	}
	return c
}

// document renders the state as a snapshot document.
func (st metaState) document(revision int) Document {
	doc := Document{Format: 1, Revision: revision, Objects: map[string]Object{}, Enums: map[string]Enum{}}
	for ref, o := range st.objects {
		doc.Objects[ref] = *cloneObject(*o)
	}
	for id, e := range st.enums {
		doc.Enums[id] = Enum{Name: cloneNames(e.name), Tree: nodesTo(e.tree)}
	}
	return doc
}

// metaTx is one mutation in progress: a private copy of the state plus
// the enum and node events the operations emitted. Object events are
// derived at commit by comparing every object with its prior value.
type metaTx struct {
	st     metaState
	events []MetaEvent
}

func (tx *metaTx) emit(ev MetaEvent) { tx.events = append(tx.events, ev) }

// mutate runs fn on a copy of the state. ifMatch, when set, must equal
// the current revision. A mutation whose result equals the prior state
// is reported unchanged and emits nothing.
func (s *metaStore) mutate(ifMatch *int, fn func(tx *metaTx) error) (revision int, changed bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ifMatch != nil && *ifMatch != s.revision {
		return s.revision, false, metaErrf(http.StatusConflict, "revision-conflict",
			"the store is at revision %d, not %d", s.revision, *ifMatch)
	}
	tx := &metaTx{st: s.state.clone()}
	if err := fn(tx); err != nil {
		return s.revision, false, err
	}
	before := mustJSON(s.state.document(0))
	after := mustJSON(tx.st.document(0))
	if bytes.Equal(before, after) {
		return s.revision, false, nil
	}
	tx.events = append(tx.events, objectEvents(s.state, tx.st)...)
	events := tx.events
	s.revision++
	s.state = tx.st
	for i := range events {
		events[i].Revision = s.revision
	}
	s.publishLocked(events)
	return s.revision, true, nil
}

// objectEvents lists object.updated / object.deleted for every object
// that differs between two states, in ref order.
func objectEvents(before, after metaState) []MetaEvent {
	refs := map[string]struct{}{}
	for r := range before.objects {
		refs[r] = struct{}{}
	}
	for r := range after.objects {
		refs[r] = struct{}{}
	}
	sorted := make([]string, 0, len(refs))
	for r := range refs {
		sorted = append(sorted, r)
	}
	sort.Strings(sorted)
	var out []MetaEvent
	for _, r := range sorted {
		b, hadB := before.objects[r]
		a, hasA := after.objects[r]
		switch {
		case !hasA:
			out = append(out, MetaEvent{Kind: "object.deleted", Ref: r})
		case !hadB || !bytes.Equal(mustJSON(*b), mustJSON(*a)):
			out = append(out, MetaEvent{Kind: "object.updated", Ref: r, Value: mustJSON(*a)})
		}
	}
	return out
}

// publishLocked appends events to the retained log and fans them out.
// A subscriber whose queue is full loses the event silently; it can
// only notice by the revision gap.
func (s *metaStore) publishLocked(events []MetaEvent) {
	for i := range events {
		ev := &events[i]
		s.log = append(s.log, *ev)
		for sub := range s.subs {
			select {
			case sub.queue <- *ev:
			default:
			}
		}
	}
	if over := len(s.log) - s.logMax; over > 0 {
		for i := range s.log[:over] {
			s.droppedThrough = max(s.droppedThrough, s.log[i].Revision)
		}
		s.log = append([]MetaEvent(nil), s.log[over:]...)
	}
}

// seed replaces the whole store and emits one import event.
func (s *metaStore) seed(doc Document) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state = stateFromDocument(doc)
	s.revision++
	s.publishLocked([]MetaEvent{{Revision: s.revision, Kind: "import"}})
	return s.revision
}

// restart models an occulited restart: the store survives (it lives on
// disk) but the event log is memory only, so any resume below the
// current revision resyncs; open streams end.
func (s *metaStore) restart() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.log = nil
	s.droppedThrough = s.revision
	for sub := range s.subs {
		sub.stop()
	}
}

func (s *metaStore) dropStreams() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for sub := range s.subs {
		sub.stop()
	}
}

// read runs fn under the lock with the current revision.
func (s *metaStore) read(fn func(revision int, st metaState)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn(s.revision, s.state)
}

// idPattern is the shape of enum, node and namespace ids.
var idPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

func validID(id string) error {
	if len(id) > metaIDMaxLen || !idPattern.MatchString(id) {
		return metaErrf(http.StatusUnprocessableEntity, "invalid-id", "invalid id %q", id)
	}
	return nil
}

// cleanName trims and validates a name.
func cleanName(name string) (string, error) {
	n := strings.TrimSpace(name)
	if n == "" || len(n) > metaNameMaxBytes {
		return "", metaErrf(http.StatusUnprocessableEntity, "invalid-name", "a name must be 1-255 bytes")
	}
	for _, r := range n {
		if unicode.IsControl(r) {
			return "", metaErrf(http.StatusUnprocessableEntity, "invalid-name", "a name must not contain control characters")
		}
	}
	return n, nil
}

// splitRef validates "<interface>.<address>", split at the first dot.
func splitRef(ref string) error {
	iface, addr, ok := strings.Cut(ref, ".")
	if !ok || iface == "" || addr == "" {
		return metaErrf(http.StatusNotFound, "unknown-object", "no such object: %s", ref)
	}
	return nil
}

// findNode resolves the node ids below an enum's root; it returns the
// sibling list holding the node and its index.
func findNode(tree []*mnode, ids []string) (siblings []*mnode, index int, ok bool) {
	level := tree
	for depth, id := range ids {
		found := -1
		for i, n := range level {
			if n.id == id {
				found = i
				break
			}
		}
		if found < 0 {
			return nil, 0, false
		}
		if depth == len(ids)-1 {
			return level, found, true
		}
		level = level[found].children
	}
	return nil, 0, false
}

// hasPath reports whether a full path "<enum>/<id>/…" names a node.
func (st metaState) hasPath(path string) bool {
	parts := strings.Split(path, "/")
	e, ok := st.enums[parts[0]]
	if !ok || len(parts) < 2 {
		return false
	}
	_, _, ok = findNode(e.tree, parts[1:])
	return ok
}

func subtreeDepth(n *mnode) int {
	d := 0
	for _, c := range n.children {
		d = max(d, subtreeDepth(c))
	}
	return d + 1
}

// objectsUnder lists the refs of objects holding path or a path below.
func (st metaState) objectsUnder(path string) []string {
	var refs []string
	for ref, o := range st.objects {
		for _, p := range o.Enums {
			if p == path || strings.HasPrefix(p, path+"/") {
				refs = append(refs, ref)
				break
			}
		}
	}
	sort.Strings(refs)
	return refs
}

// rewritePaths replaces the prefix from with to in every object path;
// to == "" removes the matching paths.
func (st metaState) rewritePaths(from, to string) {
	for _, o := range st.objects {
		out := o.Enums[:0:0]
		for _, p := range o.Enums {
			switch {
			case p == from || strings.HasPrefix(p, from+"/"):
				if to != "" {
					out = append(out, to+strings.TrimPrefix(p, from))
				}
			default:
				out = append(out, p)
			}
		}
		o.Enums = out
	}
}

// objectPatch is a parsed object write. For a PUT, replace is set and
// absent optionals reset.
type objectPatch struct {
	replace bool
	name    *string
	enums   *[]string
	meta    map[string]json.RawMessage
	hasMeta bool
}

// patchObject applies an object write. A write to a missing object
// creates it when it carries a name.
func (tx *metaTx) patchObject(ref string, p objectPatch) error {
	if err := splitRef(ref); err != nil {
		return err
	}
	o, exists := tx.st.objects[ref]
	if !exists {
		if p.name == nil {
			return metaErrf(http.StatusUnprocessableEntity, "invalid-name", "creating %s needs a name", ref)
		}
		o = &Object{Enums: []string{}, Meta: map[string]json.RawMessage{}}
	}
	next := cloneObject(*o)
	if p.replace {
		next.Enums = []string{}
		next.Meta = map[string]json.RawMessage{}
	}
	if p.name != nil {
		n, err := cleanName(*p.name)
		if err != nil {
			return err
		}
		next.Name = n
	}
	if p.enums != nil {
		for _, path := range *p.enums {
			if !tx.st.hasPath(path) {
				return metaErrf(http.StatusUnprocessableEntity, "unknown-path", "no such enum path: %s", path)
			}
		}
		next.Enums = append([]string{}, *p.enums...)
	}
	if p.hasMeta {
		for ns, v := range p.meta {
			if err := validID(ns); err != nil {
				return err
			}
			if string(v) == "null" {
				delete(next.Meta, ns)
				continue
			}
			next.Meta[ns] = v
		}
		if len(mustJSON(next.Meta)) > metaMaxMetaBytes {
			return metaErrf(http.StatusUnprocessableEntity, "invalid-body", "the metadata of one object exceeds 16 KiB")
		}
	}
	tx.st.objects[ref] = next
	return nil
}

func (tx *metaTx) deleteObject(ref string) error {
	if _, ok := tx.st.objects[ref]; !ok {
		return metaErrf(http.StatusNotFound, "unknown-object", "no such object: %s", ref)
	}
	delete(tx.st.objects, ref)
	return nil
}

// setOrphaned flips the flag only the box itself maintains.
func (tx *metaTx) setOrphaned(ref string, orphaned bool) error {
	o, ok := tx.st.objects[ref]
	if !ok {
		return metaErrf(http.StatusNotFound, "unknown-object", "no such object: %s", ref)
	}
	o.Orphaned = orphaned
	return nil
}

func validEnumName(name map[string]string) error {
	if len(name) == 0 {
		return metaErrf(http.StatusUnprocessableEntity, "invalid-name", "an enum needs a name")
	}
	for _, v := range name {
		if _, err := cleanName(v); err != nil {
			return err
		}
	}
	return nil
}

func enumValue(e *menum) json.RawMessage {
	return mustJSON(Enum{Name: cloneNames(e.name), Tree: nodesTo(e.tree)})
}

func (tx *metaTx) createEnum(id string, name map[string]string) error {
	if err := validID(id); err != nil {
		return err
	}
	if err := validEnumName(name); err != nil {
		return err
	}
	if _, ok := tx.st.enums[id]; ok {
		return metaErrf(http.StatusConflict, "exists", "enum %s exists", id)
	}
	e := &menum{name: cloneNames(name), tree: []*mnode{}}
	tx.st.enums[id] = e
	tx.emit(MetaEvent{Kind: "enum.created", Enum: id, Value: enumValue(e)})
	return nil
}

func (tx *metaTx) enum(id string) (*menum, error) {
	e, ok := tx.st.enums[id]
	if !ok {
		return nil, metaErrf(http.StatusNotFound, "unknown-enum", "no such enum: %s", id)
	}
	return e, nil
}

func (tx *metaTx) patchEnum(id string, name map[string]string) error {
	e, err := tx.enum(id)
	if err != nil {
		return err
	}
	if err := validEnumName(name); err != nil {
		return err
	}
	if bytes.Equal(mustJSON(e.name), mustJSON(name)) {
		return nil
	}
	e.name = cloneNames(name)
	tx.emit(MetaEvent{Kind: "enum.updated", Enum: id, Value: enumValue(e)})
	return nil
}

// membersCheck refuses removing path while objects hold it, unless
// detach is set, which strips the paths from every object instead.
func (tx *metaTx) membersCheck(path string, detach bool) error {
	refs := tx.st.objectsUnder(path)
	if len(refs) == 0 {
		return nil
	}
	if !detach {
		e := metaErrf(http.StatusConflict, "has-members", "%s still has members", path)
		e.Refs = refs
		return e
	}
	tx.st.rewritePaths(path, "")
	return nil
}

func (tx *metaTx) deleteEnum(id string, detach bool) error {
	if _, err := tx.enum(id); err != nil {
		return err
	}
	if err := tx.membersCheck(id, detach); err != nil {
		return err
	}
	delete(tx.st.enums, id)
	tx.emit(MetaEvent{Kind: "enum.deleted", Enum: id})
	return nil
}

// parentIDs resolves a node parent given as a full path; nil, "" or the
// enum id mean the root. A non-root parent must start with "<enum>/".
func (tx *metaTx) parentIDs(enum string, parent *string) ([]string, error) {
	if parent == nil || *parent == "" || *parent == enum {
		return nil, nil
	}
	if !strings.HasPrefix(*parent, enum+"/") {
		return nil, metaErrf(http.StatusUnprocessableEntity, "unknown-path", "parent %s is not in enum %s", *parent, enum)
	}
	if !tx.st.hasPath(*parent) {
		return nil, metaErrf(http.StatusUnprocessableEntity, "unknown-path", "no such parent: %s", *parent)
	}
	return strings.Split(strings.TrimPrefix(*parent, enum+"/"), "/"), nil
}

// childList returns a pointer to the child slice below parent ids.
func childList(e *menum, parent []string) *[]*mnode {
	if len(parent) == 0 {
		return &e.tree
	}
	sib, i, _ := findNode(e.tree, parent)
	return &sib[i].children
}

func insertAt(list []*mnode, n *mnode, position *int) []*mnode {
	pos := len(list)
	if position != nil {
		pos = min(max(*position, 0), len(list))
	}
	list = append(list, nil)
	copy(list[pos+1:], list[pos:])
	list[pos] = n
	return list
}

func nodeValue(n *mnode) json.RawMessage {
	return mustJSON(Node{ID: n.id, Name: n.name, Icon: n.icon})
}

func (tx *metaTx) createNode(enum string, parent *string, id, name, icon string, position *int) error {
	e, err := tx.enum(enum)
	if err != nil {
		return err
	}
	pids, err := tx.parentIDs(enum, parent)
	if err != nil {
		return err
	}
	if err := validID(id); err != nil {
		return err
	}
	n, err := cleanName(name)
	if err != nil {
		return err
	}
	if len(pids)+1 > metaMaxDepth {
		return metaErrf(http.StatusUnprocessableEntity, "invalid-body", "enum trees are at most %d levels deep", metaMaxDepth)
	}
	list := childList(e, pids)
	for _, c := range *list {
		if c.id == id {
			return metaErrf(http.StatusConflict, "exists", "node %s exists", id)
		}
	}
	node := &mnode{id: id, name: n, icon: icon}
	*list = insertAt(*list, node, position)
	path := strings.Join(append(append([]string{enum}, pids...), id), "/")
	tx.emit(MetaEvent{Kind: "node.created", Enum: enum, Path: path, Value: nodeValue(node)})
	return nil
}

// nodePatch is a parsed node update; move is set when the body carried
// a parent member (null meaning the root).
type nodePatch struct {
	name     *string
	icon     *string
	move     bool
	parent   *string
	position *int
}

// relIDs splits a node path relative to its enum.
func (tx *metaTx) relIDs(enum, rel string) (*menum, []string, error) {
	e, err := tx.enum(enum)
	if err != nil {
		return nil, nil, err
	}
	ids := strings.Split(strings.Trim(rel, "/"), "/")
	if _, _, ok := findNode(e.tree, ids); rel == "" || !ok {
		return nil, nil, metaErrf(http.StatusUnprocessableEntity, "unknown-path", "no such node: %s/%s", enum, rel)
	}
	return e, ids, nil
}

func (tx *metaTx) patchNode(enum, rel string, p nodePatch) error {
	e, ids, err := tx.relIDs(enum, rel)
	if err != nil {
		return err
	}
	sib, i, _ := findNode(e.tree, ids)
	node := sib[i]
	fromPath := enum + "/" + strings.Join(ids, "/")
	updated := false
	if p.name != nil {
		n, err := cleanName(*p.name)
		if err != nil {
			return err
		}
		updated = updated || n != node.name
		node.name = n
	}
	if p.icon != nil {
		updated = updated || *p.icon != node.icon
		node.icon = *p.icon
	}
	parentIDs := ids[:len(ids)-1]
	if p.move {
		pids, err := tx.parentIDs(enum, p.parent)
		if err != nil {
			return err
		}
		parentIDs = pids
	}
	target := enum + "/" + strings.Join(append(append([]string{}, parentIDs...), node.id), "/")
	if len(parentIDs) == 0 {
		target = enum + "/" + node.id
	}
	moved := target != fromPath
	if moved && strings.HasPrefix(target, fromPath+"/") {
		return metaErrf(http.StatusUnprocessableEntity, "invalid-body", "a node cannot move below itself")
	}
	if moved && len(parentIDs)+subtreeDepth(node) > metaMaxDepth {
		return metaErrf(http.StatusUnprocessableEntity, "invalid-body", "enum trees are at most %d levels deep", metaMaxDepth)
	}
	if moved || p.position != nil {
		// Detach, then insert under the (possibly unchanged) parent.
		oldList := childList(e, ids[:len(ids)-1])
		*oldList = append((*oldList)[:i:i], (*oldList)[i+1:]...)
		newList := childList(e, parentIDs)
		for _, c := range *newList {
			if c.id == node.id {
				return metaErrf(http.StatusConflict, "exists", "node %s exists under the new parent", node.id)
			}
		}
		pos := p.position
		if pos == nil {
			pos = &i
		}
		*newList = insertAt(*newList, node, pos)
		if !moved && *p.position != i {
			updated = true
		}
	}
	if moved {
		tx.st.rewritePaths(fromPath, target)
		tx.emit(MetaEvent{Kind: "node.moved", Enum: enum, From: fromPath, To: target})
	}
	if updated {
		tx.emit(MetaEvent{Kind: "node.updated", Enum: enum, Path: target, Value: nodeValue(node)})
	}
	return nil
}

func (tx *metaTx) deleteNode(enum, rel string, detach bool) error {
	e, ids, err := tx.relIDs(enum, rel)
	if err != nil {
		return err
	}
	path := enum + "/" + strings.Join(ids, "/")
	if err := tx.membersCheck(path, detach); err != nil {
		return err
	}
	list := childList(e, ids[:len(ids)-1])
	_, i, _ := findNode(e.tree, ids)
	*list = append((*list)[:i:i], (*list)[i+1:]...)
	tx.emit(MetaEvent{Kind: "node.deleted", Enum: enum, Path: path})
	return nil
}

// ----------------------------------------------------------------------
// HTTP
// ----------------------------------------------------------------------

// metaRoutes registers the /api/meta/v1 surface other than /version.
func (f *Fake) metaRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/meta/v1/snapshot", f.handleMetaSnapshot)
	mux.HandleFunc("GET /api/meta/v1/objects", f.handleMetaObjects)
	mux.HandleFunc("POST /api/meta/v1/objects:bulk", f.handleMetaBulk)
	mux.HandleFunc("GET /api/meta/v1/objects/{ref}", f.handleMetaObject)
	mux.HandleFunc("PUT /api/meta/v1/objects/{ref}", f.handleMetaObjectWrite)
	mux.HandleFunc("PATCH /api/meta/v1/objects/{ref}", f.handleMetaObjectWrite)
	mux.HandleFunc("DELETE /api/meta/v1/objects/{ref}", f.handleMetaObjectDelete)
	mux.HandleFunc("GET /api/meta/v1/enums", f.handleMetaEnums)
	mux.HandleFunc("POST /api/meta/v1/enums", f.handleMetaEnumCreate)
	mux.HandleFunc("PATCH /api/meta/v1/enums/{enum}", f.handleMetaEnumPatch)
	mux.HandleFunc("DELETE /api/meta/v1/enums/{enum}", f.handleMetaEnumDelete)
	mux.HandleFunc("GET /api/meta/v1/enums/{enum}/tree", f.handleMetaEnumTree)
	mux.HandleFunc("POST /api/meta/v1/enums/{enum}/nodes", f.handleMetaNodeCreate)
	mux.HandleFunc("PATCH /api/meta/v1/enums/{enum}/nodes/{path...}", f.handleMetaNodePatch)
	mux.HandleFunc("DELETE /api/meta/v1/enums/{enum}/nodes/{path...}", f.handleMetaNodeDelete)
	mux.HandleFunc("GET /api/meta/v1/events/sse", f.handleMetaStream)
}

// metaErrorBody is a metadata error answer; has-members names the refs.
type metaErrorBody struct {
	Error   string         `json:"error"`
	Message string         `json:"message"`
	Detail  *metaErrorRefs `json:"detail,omitempty"`
}

type metaErrorRefs struct {
	Refs []string `json:"refs"`
}

func writeMetaError(w http.ResponseWriter, err error) {
	var me *MetaError
	if !errors.As(err, &me) {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	body := metaErrorBody{Error: me.Code, Message: me.Message}
	if me.Refs != nil {
		body.Detail = &metaErrorRefs{Refs: me.Refs}
	}
	writeJSON(w, me.Status, body)
}

// revisionAnswer is the body of a changing mutation.
type revisionAnswer struct {
	Revision int `json:"revision"`
}

// writeMutation answers a mutation: the revision in the body and in
// ETag, or 304 with an empty body and the revision only in ETag when
// nothing changed. [DeviateMeta304WithoutETag] drops the ETag of a 304.
func (f *Fake) writeMutation(w http.ResponseWriter, status, revision int, changed bool, err error) {
	if err != nil {
		writeMetaError(w, err)
		return
	}
	if changed || !f.deviates(DeviateMeta304WithoutETag) {
		w.Header().Set("ETag", strconv.Itoa(revision))
	}
	if !changed {
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusNotModified)
		return
	}
	writeJSON(w, status, revisionAnswer{Revision: revision})
}

// ifMatch reads the If-Match revision. A value that is not a revision
// can never match.
func ifMatch(r *http.Request) *int {
	v := r.Header.Get("If-Match")
	if v == "" {
		return nil
	}
	v = strings.Trim(strings.TrimPrefix(strings.TrimSpace(v), "W/"), `"`)
	n, err := strconv.Atoi(v)
	if err != nil {
		n = -1
	}
	return &n
}

// readBody decodes a JSON object body into its members, so presence
// and null can be told apart and unknown members refused.
func readBody(r *http.Request) (map[string]json.RawMessage, error) {
	raw, err := io.ReadAll(io.LimitReader(r.Body, metaBodyLimit+1))
	if err != nil || len(raw) > metaBodyLimit {
		return nil, metaErrf(http.StatusUnprocessableEntity, "invalid-body", "unreadable body")
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return map[string]json.RawMessage{}, nil
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil || m == nil {
		return nil, metaErrf(http.StatusUnprocessableEntity, "invalid-body", "the body must be a JSON object")
	}
	return m, nil
}

func onlyMembers(m map[string]json.RawMessage, allowed ...string) error {
	for k := range m {
		ok := false
		for _, a := range allowed {
			ok = ok || k == a
		}
		if !ok {
			return metaErrf(http.StatusUnprocessableEntity, "invalid-body", "unknown field %q", k)
		}
	}
	return nil
}

func decodeMember[T any](m map[string]json.RawMessage, key string, into *T) error { // T: the member's declared type.
	raw, ok := m[key]
	if !ok {
		return nil
	}
	if err := json.Unmarshal(raw, into); err != nil {
		return metaErrf(http.StatusUnprocessableEntity, "invalid-body", "field %q has the wrong type", key)
	}
	return nil
}

// parseObjectPatch reads an object write body. orphaned is set only by
// the box and refused with 403; unknown members are refused with 422.
func parseObjectPatch(m map[string]json.RawMessage, replace bool) (objectPatch, error) {
	p := objectPatch{replace: replace}
	if _, ok := m["orphaned"]; ok {
		return p, metaErrf(http.StatusForbidden, "forbidden", "orphaned is maintained by the box")
	}
	if err := onlyMembers(m, "name", "enums", "meta"); err != nil {
		return p, err
	}
	if raw, ok := m["name"]; ok {
		var n string
		if err := json.Unmarshal(raw, &n); err != nil {
			return p, metaErrf(http.StatusUnprocessableEntity, "invalid-name", "name must be a string")
		}
		p.name = &n
	}
	if _, ok := m["enums"]; ok {
		var e []string
		if err := decodeMember(m, "enums", &e); err != nil {
			return p, err
		}
		if e == nil {
			e = []string{}
		}
		p.enums = &e
	}
	if raw, ok := m["meta"]; ok && string(raw) != "null" {
		p.hasMeta = true
		if err := decodeMember(m, "meta", &p.meta); err != nil {
			return p, err
		}
	}
	return p, nil
}

func (f *Fake) handleMetaSnapshot(w http.ResponseWriter, r *http.Request) {
	if _, _, ok := f.authorize(w, r, "meta:read", false); !ok {
		return
	}
	var doc Document
	f.meta.read(func(rev int, st metaState) { doc = st.document(rev) })
	writeJSON(w, http.StatusOK, doc)
}

type objectsAnswer struct {
	Revision int               `json:"revision"`
	Objects  map[string]Object `json:"objects"`
}

// handleMetaObjects answers GET /objects with the optional enum= and
// orphaned= filters. enum= is a subtree query: an object matches when
// it holds the path itself or any path below it.
func (f *Fake) handleMetaObjects(w http.ResponseWriter, r *http.Request) {
	if _, _, ok := f.authorize(w, r, "meta:read", false); !ok {
		return
	}
	q := r.URL.Query()
	enumPath, orphaned := q.Get("enum"), q.Get("orphaned")
	var ans objectsAnswer
	var err error
	f.meta.read(func(rev int, st metaState) {
		if enumPath != "" {
			if !st.hasPath(enumPath) {
				err = metaErrf(http.StatusUnprocessableEntity, "unknown-path", "no such enum path: %s", enumPath)
				return
			}
		}
		ans = objectsAnswer{Revision: rev, Objects: map[string]Object{}}
		for ref, o := range st.objects {
			if enumPath != "" && !holdsSubtree(o.Enums, enumPath) {
				continue
			}
			if orphaned != "" && strconv.FormatBool(o.Orphaned) != orphaned {
				continue
			}
			ans.Objects[ref] = *cloneObject(*o)
		}
	})
	if err != nil {
		writeMetaError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, ans)
}

// holdsSubtree reports whether any path is root or lies below it.
func holdsSubtree(paths []string, root string) bool {
	for _, p := range paths {
		if p == root || strings.HasPrefix(p, root+"/") {
			return true
		}
	}
	return false
}

type objectAnswer struct {
	Revision int    `json:"revision"`
	Ref      string `json:"ref"`
	Object   Object `json:"object"`
}

func (f *Fake) handleMetaObject(w http.ResponseWriter, r *http.Request) {
	if _, _, ok := f.authorize(w, r, "meta:read", false); !ok {
		return
	}
	ref := r.PathValue("ref")
	var ans *objectAnswer
	f.meta.read(func(rev int, st metaState) {
		if o, ok := st.objects[ref]; ok {
			ans = &objectAnswer{Revision: rev, Ref: ref, Object: *cloneObject(*o)}
		}
	})
	if ans == nil {
		writeMetaError(w, metaErrf(http.StatusNotFound, "unknown-object", "no such object: %s", ref))
		return
	}
	writeJSON(w, http.StatusOK, ans)
}

func (f *Fake) handleMetaObjectWrite(w http.ResponseWriter, r *http.Request) {
	if _, _, ok := f.authorize(w, r, "meta:write", false); !ok {
		return
	}
	m, err := readBody(r)
	if err != nil {
		writeMetaError(w, err)
		return
	}
	p, err := parseObjectPatch(m, r.Method == http.MethodPut)
	if err != nil {
		writeMetaError(w, err)
		return
	}
	if p.replace && p.name == nil {
		writeMetaError(w, metaErrf(http.StatusUnprocessableEntity, "invalid-name", "PUT needs a name"))
		return
	}
	ref := r.PathValue("ref")
	rev, changed, err := f.meta.mutate(ifMatch(r), func(tx *metaTx) error { return tx.patchObject(ref, p) })
	f.writeMutation(w, http.StatusOK, rev, changed, err)
}

func (f *Fake) handleMetaObjectDelete(w http.ResponseWriter, r *http.Request) {
	if _, _, ok := f.authorize(w, r, "meta:write", false); !ok {
		return
	}
	ref := r.PathValue("ref")
	rev, changed, err := f.meta.mutate(ifMatch(r), func(tx *metaTx) error { return tx.deleteObject(ref) })
	f.writeMutation(w, http.StatusOK, rev, changed, err)
}

// handleMetaBulk applies {set: {ref: patch}, delete: [ref]} atomically
// under one revision.
func (f *Fake) handleMetaBulk(w http.ResponseWriter, r *http.Request) {
	if _, _, ok := f.authorize(w, r, "meta:write", false); !ok {
		return
	}
	m, err := readBody(r)
	if err == nil {
		err = onlyMembers(m, "set", "delete")
	}
	var set map[string]map[string]json.RawMessage
	var del []string
	if err == nil {
		err = decodeMember(m, "set", &set)
	}
	if err == nil {
		err = decodeMember(m, "delete", &del)
	}
	if err != nil {
		writeMetaError(w, err)
		return
	}
	refs := make([]string, 0, len(set))
	patches := map[string]objectPatch{}
	for ref, body := range set {
		p, err := parseObjectPatch(body, false)
		if err != nil {
			writeMetaError(w, err)
			return
		}
		refs = append(refs, ref)
		patches[ref] = p
	}
	sort.Strings(refs)
	rev, changed, err := f.meta.mutate(ifMatch(r), func(tx *metaTx) error {
		for _, ref := range refs {
			if err := tx.patchObject(ref, patches[ref]); err != nil {
				return err
			}
		}
		for _, ref := range del {
			if err := tx.deleteObject(ref); err != nil {
				return err
			}
		}
		return nil
	})
	f.writeMutation(w, http.StatusOK, rev, changed, err)
}

type enumsAnswer struct {
	Revision int             `json:"revision"`
	Enums    map[string]Enum `json:"enums"`
}

func (f *Fake) handleMetaEnums(w http.ResponseWriter, r *http.Request) {
	if _, _, ok := f.authorize(w, r, "meta:read", false); !ok {
		return
	}
	var ans enumsAnswer
	f.meta.read(func(rev int, st metaState) {
		ans = enumsAnswer{Revision: rev, Enums: st.document(rev).Enums}
	})
	writeJSON(w, http.StatusOK, ans)
}

type treeAnswer struct {
	Revision int               `json:"revision"`
	Enum     string            `json:"enum"`
	Name     map[string]string `json:"name"`
	Tree     []Node            `json:"tree"`
}

func (f *Fake) handleMetaEnumTree(w http.ResponseWriter, r *http.Request) {
	if _, _, ok := f.authorize(w, r, "meta:read", false); !ok {
		return
	}
	id := r.PathValue("enum")
	var ans *treeAnswer
	f.meta.read(func(rev int, st metaState) {
		if e, ok := st.enums[id]; ok {
			ans = &treeAnswer{Revision: rev, Enum: id, Name: cloneNames(e.name), Tree: nodesTo(e.tree)}
		}
	})
	if ans == nil {
		writeMetaError(w, metaErrf(http.StatusNotFound, "unknown-enum", "no such enum: %s", id))
		return
	}
	writeJSON(w, http.StatusOK, ans)
}

func (f *Fake) handleMetaEnumCreate(w http.ResponseWriter, r *http.Request) {
	if _, _, ok := f.authorize(w, r, "meta:write", false); !ok {
		return
	}
	m, err := readBody(r)
	var id string
	var name map[string]string
	if err == nil {
		err = onlyMembers(m, "id", "name")
	}
	if err == nil {
		err = decodeMember(m, "id", &id)
	}
	if err == nil {
		err = decodeMember(m, "name", &name)
	}
	if err != nil {
		writeMetaError(w, err)
		return
	}
	rev, changed, err := f.meta.mutate(ifMatch(r), func(tx *metaTx) error { return tx.createEnum(id, name) })
	f.writeMutation(w, http.StatusCreated, rev, changed, err)
}

func (f *Fake) handleMetaEnumPatch(w http.ResponseWriter, r *http.Request) {
	if _, _, ok := f.authorize(w, r, "meta:write", false); !ok {
		return
	}
	m, err := readBody(r)
	var name map[string]string
	if err == nil {
		err = onlyMembers(m, "name")
	}
	if err == nil {
		err = decodeMember(m, "name", &name)
	}
	if err != nil {
		writeMetaError(w, err)
		return
	}
	id := r.PathValue("enum")
	rev, changed, err := f.meta.mutate(ifMatch(r), func(tx *metaTx) error { return tx.patchEnum(id, name) })
	f.writeMutation(w, http.StatusOK, rev, changed, err)
}

func (f *Fake) handleMetaEnumDelete(w http.ResponseWriter, r *http.Request) {
	if _, _, ok := f.authorize(w, r, "meta:write", false); !ok {
		return
	}
	id := r.PathValue("enum")
	detach := r.URL.Query().Get("members") == "detach"
	rev, changed, err := f.meta.mutate(ifMatch(r), func(tx *metaTx) error { return tx.deleteEnum(id, detach) })
	f.writeMutation(w, http.StatusOK, rev, changed, err)
}

// optionalString decodes a member that may be absent, null or a string.
func optionalString(m map[string]json.RawMessage, key string) (present bool, value *string, err error) {
	raw, ok := m[key]
	if !ok {
		return false, nil, nil
	}
	if string(raw) == "null" {
		return true, nil, nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return true, nil, metaErrf(http.StatusUnprocessableEntity, "invalid-body", "field %q must be a string", key)
	}
	return true, &s, nil
}

func (f *Fake) handleMetaNodeCreate(w http.ResponseWriter, r *http.Request) {
	if _, _, ok := f.authorize(w, r, "meta:write", false); !ok {
		return
	}
	m, err := readBody(r)
	var id, name, icon string
	var position *int
	var parent *string
	if err == nil {
		err = onlyMembers(m, "parent", "id", "name", "icon", "position")
	}
	if err == nil {
		_, parent, err = optionalString(m, "parent")
	}
	if err == nil {
		err = decodeMember(m, "id", &id)
	}
	if err == nil {
		err = decodeMember(m, "name", &name)
	}
	if err == nil {
		err = decodeMember(m, "icon", &icon)
	}
	if err == nil {
		err = decodeMember(m, "position", &position)
	}
	if err != nil {
		writeMetaError(w, err)
		return
	}
	enum := r.PathValue("enum")
	rev, changed, err := f.meta.mutate(ifMatch(r), func(tx *metaTx) error {
		return tx.createNode(enum, parent, id, name, icon, position)
	})
	f.writeMutation(w, http.StatusCreated, rev, changed, err)
}

// handleMetaNodePatch updates a node addressed by its path relative to
// the enum in the URL; a parent member moves it (null: to the root).
func (f *Fake) handleMetaNodePatch(w http.ResponseWriter, r *http.Request) {
	if _, _, ok := f.authorize(w, r, "meta:write", false); !ok {
		return
	}
	m, err := readBody(r)
	var p nodePatch
	if err == nil {
		err = onlyMembers(m, "name", "icon", "parent", "position")
	}
	if err == nil {
		_, p.name, err = optionalString(m, "name")
	}
	if err == nil {
		var present bool
		present, p.icon, err = optionalString(m, "icon")
		if present && p.icon == nil {
			empty := ""
			p.icon = &empty
		}
	}
	if err == nil {
		p.move, p.parent, err = optionalString(m, "parent")
	}
	if err == nil {
		err = decodeMember(m, "position", &p.position)
	}
	if err != nil {
		writeMetaError(w, err)
		return
	}
	enum, rel := r.PathValue("enum"), r.PathValue("path")
	rev, changed, err := f.meta.mutate(ifMatch(r), func(tx *metaTx) error { return tx.patchNode(enum, rel, p) })
	f.writeMutation(w, http.StatusOK, rev, changed, err)
}

func (f *Fake) handleMetaNodeDelete(w http.ResponseWriter, r *http.Request) {
	if _, _, ok := f.authorize(w, r, "meta:write", false); !ok {
		return
	}
	enum, rel := r.PathValue("enum"), r.PathValue("path")
	detach := r.URL.Query().Get("members") == "detach"
	rev, changed, err := f.meta.mutate(ifMatch(r), func(tx *metaTx) error { return tx.deleteNode(enum, rel, detach) })
	f.writeMutation(w, http.StatusOK, rev, changed, err)
}

// ----------------------------------------------------------------------
// Test handle
// ----------------------------------------------------------------------

// MetaHandle seeds and inspects the metadata store from a test. Every
// mutation goes through the same code as the HTTP API and emits the
// same change events, so it stands for "someone edited the box".
type MetaHandle struct{ s *metaStore }

// Meta returns the handle to the fake's metadata store.
func (f *Fake) Meta() *MetaHandle { return &MetaHandle{s: f.meta} }

// Seed replaces the whole store with doc (its revision is ignored) and
// emits one import event; it returns the new revision.
func (h *MetaHandle) Seed(doc Document) int { return h.s.seed(doc) }

// Revision returns the current store revision.
func (h *MetaHandle) Revision() int {
	var rev int
	h.s.read(func(r int, _ metaState) { rev = r })
	return rev
}

// Snapshot returns a copy of the whole store.
func (h *MetaHandle) Snapshot() Document {
	var doc Document
	h.s.read(func(r int, st metaState) { doc = st.document(r) })
	return doc
}

// Events returns the retained change events.
func (h *MetaHandle) Events() []MetaEvent {
	h.s.mu.Lock()
	defer h.s.mu.Unlock()
	return append([]MetaEvent(nil), h.s.log...)
}

func (h *MetaHandle) run(fn func(tx *metaTx) error) error {
	_, _, err := h.s.mutate(nil, fn)
	return err
}

// Rename sets an object's name, creating the object when it is missing.
func (h *MetaHandle) Rename(ref, name string) error {
	return h.run(func(tx *metaTx) error { return tx.patchObject(ref, objectPatch{name: &name}) })
}

// Assign replaces an object's enum paths.
func (h *MetaHandle) Assign(ref string, paths ...string) error {
	list := append([]string{}, paths...)
	return h.run(func(tx *metaTx) error { return tx.patchObject(ref, objectPatch{enums: &list}) })
}

// SetMeta sets (or, with value "null", deletes) one metadata namespace.
func (h *MetaHandle) SetMeta(ref, namespace string, value json.RawMessage) error {
	return h.run(func(tx *metaTx) error {
		return tx.patchObject(ref, objectPatch{hasMeta: true, meta: map[string]json.RawMessage{namespace: value}})
	})
}

// Delete removes an object.
func (h *MetaHandle) Delete(ref string) error {
	return h.run(func(tx *metaTx) error { return tx.deleteObject(ref) })
}

// SetOrphaned sets the flag the box maintains for objects whose device
// no interface lists any more.
func (h *MetaHandle) SetOrphaned(ref string, orphaned bool) error {
	return h.run(func(tx *metaTx) error { return tx.setOrphaned(ref, orphaned) })
}

// CreateNode adds a node; parent is a full path, or "" for the root.
func (h *MetaHandle) CreateNode(enum, parent, id, name string) error {
	return h.run(func(tx *metaTx) error { return tx.createNode(enum, &parent, id, name, "", nil) })
}

// RenameNode renames the node at path (relative to the enum).
func (h *MetaHandle) RenameNode(enum, path, name string) error {
	return h.run(func(tx *metaTx) error { return tx.patchNode(enum, path, nodePatch{name: &name}) })
}

// MoveNode moves the node at path (relative to the enum) below parent,
// a full path or "" for the root.
func (h *MetaHandle) MoveNode(enum, path, parent string) error {
	return h.run(func(tx *metaTx) error { return tx.patchNode(enum, path, nodePatch{move: true, parent: &parent}) })
}

// DeleteNode removes the node at path (relative to the enum) and its
// subtree; detach strips member paths instead of refusing.
func (h *MetaHandle) DeleteNode(enum, path string, detach bool) error {
	return h.run(func(tx *metaTx) error { return tx.deleteNode(enum, path, detach) })
}
