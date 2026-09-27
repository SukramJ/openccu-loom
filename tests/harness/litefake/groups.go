// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package litefake

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Heating groups (/api/system/v1/groups): each group is a virtual device
// on the VirtualDevices interface ("INT000000N").

// Group is one group as the list and detail answers show it.
type Group struct {
	ID                    string   `json:"id"`
	Name                  string   `json:"name"`
	Type                  string   `json:"type"`
	TypeLabel             string   `json:"type_label"`
	Device                string   `json:"device"`
	Ref                   string   `json:"ref"`
	Members               []string `json:"-"`
	ForbidSingleOperation bool     `json:"-"`
}

// groupStore holds the groups.
type groupStore struct {
	mu     sync.Mutex
	next   int
	groups map[string]*Group
}

func newGroupStore() *groupStore { return &groupStore{groups: map[string]*Group{}} }

// groupTypes are the group types the fake offers.
func groupTypes() map[string]string {
	return map[string]string{
		"HomeMatic.heating":  "Heating group (HomeMatic)",
		"hmip.heating.group": "Heating group (HomeMatic IP)",
	}
}

func (f *Fake) groupRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/system/v1/groups", f.sysRead(f.handleGroups))
	mux.HandleFunc("GET /api/system/v1/groups/types", f.sysRead(f.handleGroupTypes))
	mux.HandleFunc("GET /api/system/v1/groups/{id}", f.sysRead(f.handleGroup))
	mux.HandleFunc("POST /api/system/v1/groups", f.sysScope(scopeSystemWrite, f.handleGroupCreate))
	mux.HandleFunc("PUT /api/system/v1/groups/{id}", f.sysScope(scopeSystemWrite, f.handleGroupUpdate))
	mux.HandleFunc("DELETE /api/system/v1/groups/{id}", f.sysScope(scopeSystemWrite, f.handleGroupDelete))
}

// deviceToConfigure is a member device that still needs configuring.
type deviceToConfigure struct {
	ID     string `json:"id"`
	Serial string `json:"serial"`
	Type   string `json:"type"`
}

type groupsAnswer struct {
	Groups             []Group             `json:"groups"`
	DevicesToConfigure []deviceToConfigure `json:"devices_to_configure"`
}

func (s *groupStore) list() []Group {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Group, 0, len(s.groups))
	for _, g := range s.groups {
		out = append(out, *g)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Device < out[j].Device })
	return out
}

func (f *Fake) handleGroups(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, groupsAnswer{Groups: f.system.groups.list(), DevicesToConfigure: []deviceToConfigure{}})
}

type groupType struct {
	ID         string   `json:"id"`
	Label      string   `json:"label"`
	Assignable []string `json:"assignable"`
	Leftover   []string `json:"leftover"`
}

func (f *Fake) handleGroupTypes(w http.ResponseWriter, _ *http.Request) {
	types := groupTypes()
	ids := make([]string, 0, len(types))
	for id := range types {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]groupType, 0, len(ids))
	for _, id := range ids {
		out = append(out, groupType{ID: id, Label: types[id], Assignable: []string{}, Leftover: []string{}})
	}
	writeJSON(w, http.StatusOK, struct {
		Types []groupType `json:"types"`
	}{Types: out})
}

// groupDetail is the GET /groups/{id} answer.
type groupDetail struct {
	Group
	DeviceName            string   `json:"device_name"`
	ForbidSingleOperation bool     `json:"forbid_single_operation"`
	Members               []string `json:"members"`
	Assignable            []string `json:"assignable"`
	Leftover              []string `json:"leftover"`
	Types                 []string `json:"types"`
}

func detailOf(g Group) groupDetail {
	return groupDetail{
		Group:                 g,
		DeviceName:            g.Name,
		ForbidSingleOperation: g.ForbidSingleOperation,
		Members:               append([]string{}, g.Members...),
		Assignable:            []string{},
		Leftover:              []string{},
		Types:                 []string{g.Type},
	}
}

func (s *groupStore) get(id string) (Group, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g, ok := s.groups[id]
	if !ok {
		return Group{}, false
	}
	return *g, true
}

func (f *Fake) handleGroup(w http.ResponseWriter, r *http.Request) {
	g, ok := f.system.groups.get(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "unknown-group", "no such group")
		return
	}
	writeJSON(w, http.StatusOK, detailOf(g))
}

// invalidField is the 422 answer naming the offending field.
type invalidField struct {
	Error   string `json:"error"`
	Message string `json:"message"`
	Field   string `json:"field"`
}

func writeInvalid(w http.ResponseWriter, field, msg string) {
	writeJSON(w, http.StatusUnprocessableEntity, invalidField{Error: "invalid", Message: msg, Field: field})
}

// validGroupName allows 1-64 characters on one line.
func validGroupName(name string) bool {
	n := len([]rune(name))
	return n >= 1 && n <= 64 && !strings.ContainsAny(name, "\r\n")
}

type groupBody struct {
	Name                  *string   `json:"name"`
	Type                  string    `json:"type"`
	Members               *[]string `json:"members"`
	ForbidSingleOperation *bool     `json:"forbid_single_operation"`
}

type groupCreated struct {
	Group
	DevicesToConfigure []deviceToConfigure `json:"devices_to_configure"`
}

func decodeGroupBody(w http.ResponseWriter, r *http.Request) (groupBody, bool) {
	var body groupBody
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
		writeInvalid(w, "body", "invalid JSON body")
		return body, false
	}
	if body.Name != nil && !validGroupName(*body.Name) {
		writeInvalid(w, "name", "a name is 1-64 characters on one line")
		return body, false
	}
	return body, true
}

func (f *Fake) handleGroupCreate(w http.ResponseWriter, r *http.Request) {
	body, ok := decodeGroupBody(w, r)
	if !ok {
		return
	}
	if body.Name == nil {
		writeInvalid(w, "name", "a name is required")
		return
	}
	label, known := groupTypes()[body.Type]
	if !known {
		writeInvalid(w, "type", "unknown group type")
		return
	}
	s := f.system.groups
	s.mu.Lock()
	s.next++
	id := strconv.Itoa(s.next)
	device := fmt.Sprintf("INT%07d", s.next)
	g := &Group{
		ID: id, Name: *body.Name, Type: body.Type, TypeLabel: label,
		Device: device, Ref: "VirtualDevices." + device,
	}
	if body.Members != nil {
		g.Members = append([]string{}, *body.Members...)
	}
	if body.ForbidSingleOperation != nil {
		g.ForbidSingleOperation = *body.ForbidSingleOperation
	}
	s.groups[id] = g
	created := groupCreated{Group: *g, DevicesToConfigure: []deviceToConfigure{}}
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, created)
}

// handleGroupUpdate applies {name?, members?, forbid_single_operation?};
// members replace the whole list.
func (f *Fake) handleGroupUpdate(w http.ResponseWriter, r *http.Request) {
	body, ok := decodeGroupBody(w, r)
	if !ok {
		return
	}
	s := f.system.groups
	s.mu.Lock()
	g, found := s.groups[r.PathValue("id")]
	if !found {
		s.mu.Unlock()
		writeError(w, http.StatusNotFound, "unknown-group", "no such group")
		return
	}
	if body.Name != nil {
		g.Name = *body.Name
	}
	if body.Members != nil {
		g.Members = append([]string{}, *body.Members...)
	}
	if body.ForbidSingleOperation != nil {
		g.ForbidSingleOperation = *body.ForbidSingleOperation
	}
	d := detailOf(*g)
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, d)
}

type groupDeleted struct {
	Deleted       bool     `json:"deleted"`
	FormerMembers []string `json:"former_members"`
}

func (f *Fake) handleGroupDelete(w http.ResponseWriter, r *http.Request) {
	s := f.system.groups
	s.mu.Lock()
	g, found := s.groups[r.PathValue("id")]
	if found {
		delete(s.groups, g.ID)
	}
	s.mu.Unlock()
	if !found {
		writeError(w, http.StatusNotFound, "unknown-group", "no such group")
		return
	}
	writeJSON(w, http.StatusOK, groupDeleted{Deleted: true, FormerMembers: append([]string{}, g.Members...)})
}

// Groups returns the groups the fake holds.
func (f *Fake) Groups() []Group { return f.system.groups.list() }
