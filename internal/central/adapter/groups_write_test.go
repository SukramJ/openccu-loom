// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Tests for GroupsDomain's write surface (Create / Update / Delete / Types /
// SuitableMembers) and writerFor's capability gate, in groups_write.go.
//
// Strategy: mirrors groups_test.go — registerCentralWithClient (defined
// there) wires a central.Unit with a registered client entry so
// primaryBackendOf resolves the primary InterfaceClient, plus a fake
// backends.Operations registered on a clientpkg.ValueWriter.
// fakeGroupWriterOps embeds the full fakeOperations stub and additionally
// implements heatingGroupWriter, backed by an in-memory roster serialized to
// the groups.gson wire shape group.ParseGroupList expects — so the
// fire-and-poll Create path (save, then poll GetHeatingGroupList for the new
// group) can be exercised without a live CCU or the real jpages transport.

package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	clientpkg "github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/internal/model/group"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

// TestMain shrinks the fire-and-poll cadence Create uses so the "group never
// appears" failure path does not block the suite for the production 60s
// timeout. Restoring the defaults afterward is unnecessary — no other test
// in this package depends on their production values.
func TestMain(m *testing.M) {
	groupSavePollInterval = 1 * time.Millisecond
	groupSavePollTimeout = 2 * time.Second
	os.Exit(m.Run())
}

// fakeGroupRecord is one in-memory roster entry backing fakeGroupWriterOps.
type fakeGroupRecord struct {
	ID      int
	Name    string
	TypeID  string
	Members []string
}

// fakeGroupWriterOps embeds the full fake Operations surface and implements
// heatingGroupWriter (GetHeatingGroupList + the five jpages mutation
// methods) on top of an in-memory roster, so GroupsDomain's write path can
// be driven without a live CCU.
type fakeGroupWriterOps struct {
	fakeOperations

	mu     sync.Mutex
	nextID int
	roster []fakeGroupRecord

	draftID    int
	draftTypes []backends.HeatingGroupType
	draftErr   error

	saveErr error

	deleteErr error

	suitable    backends.SuitableHeatingGroupMembers
	suitableErr error

	metadataCalls []string
}

func newFakeGroupWriterOps() *fakeGroupWriterOps {
	return &fakeGroupWriterOps{
		fakeOperations: fakeOperations{kind: backends.KindCCU},
		nextID:         100,
	}
}

// seed adds an existing group directly to the roster, bypassing Save — used
// to set up the "known id" Update/Delete fixtures.
func (f *fakeGroupWriterOps) seed(id int, name, typeID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.roster = append(f.roster, fakeGroupRecord{ID: id, Name: name, TypeID: typeID})
}

func (f *fakeGroupWriterOps) rosterLen() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.roster)
}

// GetHeatingGroupList serializes the roster to the groups.gson wire shape
// group.ParseGroupList decodes (see internal/model/group/group.go).
func (f *fakeGroupWriterOps) GetHeatingGroupList(_ context.Context) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	type wireMemberType struct {
		ID string `json:"id"`
	}
	type wireMember struct {
		ID         string         `json:"id"`
		MemberType wireMemberType `json:"memberType"`
	}
	type wireGroupType struct {
		ID    string `json:"id"`
		Label string `json:"label"`
	}
	type wireGroup struct {
		ID              int            `json:"id"`
		GroupType       wireGroupType  `json:"groupType"`
		GroupProperties map[string]any `json:"groupProperties"`
		GroupMembers    []wireMember   `json:"groupMembers"`
	}
	payload := struct {
		Groups []wireGroup `json:"groups"`
	}{}
	for _, g := range f.roster {
		wg := wireGroup{
			ID:              g.ID,
			GroupType:       wireGroupType{ID: g.TypeID},
			GroupProperties: map[string]any{"NAME": g.Name},
		}
		for _, m := range g.Members {
			wg.GroupMembers = append(wg.GroupMembers, wireMember{ID: m})
		}
		payload.Groups = append(payload.Groups, wg)
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func (f *fakeGroupWriterOps) CreateHeatingGroupDraft(_ context.Context) (int, []backends.HeatingGroupType, error) {
	if f.draftErr != nil {
		return 0, nil, f.draftErr
	}
	return f.draftID, f.draftTypes, nil
}

// SaveHeatingGroup mirrors the real backend's fire-and-poll contract: a new
// group is assigned a fresh id (never the caller-supplied draft placeholder)
// so Create's poll loop has to discover it by name, exactly as it does
// against the real jpages proxy (see ccu_groups.go's CreateHeatingGroupDraft
// doc comment).
func (f *fakeGroupWriterOps) SaveHeatingGroup(_ context.Context, in backends.HeatingGroupSaveInput) error {
	if f.saveErr != nil {
		return f.saveErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if in.IsNew {
		id := f.nextID
		f.nextID++
		f.roster = append(f.roster, fakeGroupRecord{ID: id, Name: in.Name, TypeID: in.TypeID, Members: in.MemberIDs})
		return nil
	}
	for i := range f.roster {
		if f.roster[i].ID == in.GroupID {
			f.roster[i].Name = in.Name
			f.roster[i].TypeID = in.TypeID
			f.roster[i].Members = in.MemberIDs
			return nil
		}
	}
	return nil
}

func (f *fakeGroupWriterOps) DeleteHeatingGroup(_ context.Context, groupID int) error {
	if f.deleteErr != nil {
		return f.deleteErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for i, g := range f.roster {
		if g.ID == groupID {
			f.roster = append(f.roster[:i], f.roster[i+1:]...)
			break
		}
	}
	return nil
}

func (f *fakeGroupWriterOps) SuitableHeatingGroupMembers(_ context.Context, _ string) (backends.SuitableHeatingGroupMembers, error) {
	if f.suitableErr != nil {
		return backends.SuitableHeatingGroupMembers{}, f.suitableErr
	}
	return f.suitable, nil
}

func (f *fakeGroupWriterOps) SetInHeatingGroupMetadata(_ context.Context, deviceAddress string, _ bool) error {
	f.mu.Lock()
	f.metadataCalls = append(f.metadataCalls, deviceAddress)
	f.mu.Unlock()
	return nil
}

// ─── Create ─────────────────────────────────────────────────────────────

// TestGroupsDomainCreateReturnsNewGroupAndGrowsRoster verifies the
// fire-and-poll happy path: after SaveHeatingGroup commits, Create's poll
// discovers the new roster entry by name and returns it, with the roster
// grown by exactly one.
func TestGroupsDomainCreateReturnsNewGroupAndGrowsRoster(t *testing.T) {
	t.Parallel()
	reg := central.NewRegistry()
	w := clientpkg.NewValueWriter()
	fb := newFakeGroupWriterOps()
	fb.draftID = 7
	fb.draftTypes = []backends.HeatingGroupType{{ID: "hmip.heating.group", LabelKey: "lblHmip"}}
	registerCentralWithClient(t, reg, w, "ccu-01", fb)

	d := NewGroupsDomain(reg, w)
	got, err := d.Create(context.Background(), "ccu-01", group.CreateInput{
		Name:      "Kitchen",
		TypeID:    "hmip.heating.group",
		MemberIDs: []string{"000AAA0000001:1"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if got.Name != "Kitchen" {
		t.Errorf("Name = %q, want Kitchen", got.Name)
	}
	if got.TypeID != "hmip.heating.group" {
		t.Errorf("TypeID = %q, want hmip.heating.group", got.TypeID)
	}
	if rl := fb.rosterLen(); rl != 1 {
		t.Errorf("roster length = %d, want 1", rl)
	}
}

// TestGroupsDomainCreatePropagatesSaveErrorWhenGroupNeverAppears verifies
// that when SaveHeatingGroup fails and no matching group ever appears in the
// roster, Create surfaces the save error rather than hanging until the full
// production poll timeout (shrunk by TestMain for this suite).
func TestGroupsDomainCreatePropagatesSaveErrorWhenGroupNeverAppears(t *testing.T) {
	t.Parallel()
	reg := central.NewRegistry()
	w := clientpkg.NewValueWriter()
	fb := newFakeGroupWriterOps()
	fb.saveErr = errors.New("save failed")
	registerCentralWithClient(t, reg, w, "ccu-01", fb)

	d := NewGroupsDomain(reg, w)
	_, err := d.Create(context.Background(), "ccu-01", group.CreateInput{
		Name: "Ghost", TypeID: "hmip.heating.group",
	})
	if err == nil {
		t.Fatal("Create: want error, got nil")
	}
	if rl := fb.rosterLen(); rl != 0 {
		t.Errorf("roster length = %d, want 0 (save never committed)", rl)
	}
}

// ─── Update ─────────────────────────────────────────────────────────────

// TestGroupsDomainUpdateUnknownIDReturnsGroupNotFound verifies that
// naming a group id absent from the roster 404s before any save is
// attempted.
func TestGroupsDomainUpdateUnknownIDReturnsGroupNotFound(t *testing.T) {
	t.Parallel()
	reg := central.NewRegistry()
	w := clientpkg.NewValueWriter()
	fb := newFakeGroupWriterOps()
	registerCentralWithClient(t, reg, w, "ccu-01", fb)

	d := NewGroupsDomain(reg, w)
	err := d.Update(context.Background(), "ccu-01", 999, group.UpdateInput{Name: "x"})
	if !errors.Is(err, hmerr.ErrGroupNotFound) {
		t.Fatalf("err = %v, want hmerr.ErrGroupNotFound", err)
	}
}

// TestGroupsDomainUpdateKnownIDSucceeds verifies the edit path against an
// existing roster entry returns no error.
func TestGroupsDomainUpdateKnownIDSucceeds(t *testing.T) {
	t.Parallel()
	reg := central.NewRegistry()
	w := clientpkg.NewValueWriter()
	fb := newFakeGroupWriterOps()
	fb.seed(3, "Kitchen", "hmip.heating.group")
	registerCentralWithClient(t, reg, w, "ccu-01", fb)

	d := NewGroupsDomain(reg, w)
	err := d.Update(context.Background(), "ccu-01", 3, group.UpdateInput{
		Name:      "Kitchen Renamed",
		MemberIDs: []string{"000AAA0000001:1"},
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
}

// ─── Delete ─────────────────────────────────────────────────────────────

// TestGroupsDomainDeleteUnknownIDReturnsGroupNotFound mirrors the Update
// gate on the delete path.
func TestGroupsDomainDeleteUnknownIDReturnsGroupNotFound(t *testing.T) {
	t.Parallel()
	reg := central.NewRegistry()
	w := clientpkg.NewValueWriter()
	fb := newFakeGroupWriterOps()
	registerCentralWithClient(t, reg, w, "ccu-01", fb)

	d := NewGroupsDomain(reg, w)
	err := d.Delete(context.Background(), "ccu-01", 999)
	if !errors.Is(err, hmerr.ErrGroupNotFound) {
		t.Fatalf("err = %v, want hmerr.ErrGroupNotFound", err)
	}
}

// TestGroupsDomainDeleteKnownIDRemovesFromRoster verifies a known id is
// removed from the backing roster.
func TestGroupsDomainDeleteKnownIDRemovesFromRoster(t *testing.T) {
	t.Parallel()
	reg := central.NewRegistry()
	w := clientpkg.NewValueWriter()
	fb := newFakeGroupWriterOps()
	fb.seed(3, "Kitchen", "hmip.heating.group")
	registerCentralWithClient(t, reg, w, "ccu-01", fb)

	d := NewGroupsDomain(reg, w)
	if err := d.Delete(context.Background(), "ccu-01", 3); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if rl := fb.rosterLen(); rl != 0 {
		t.Errorf("roster length = %d, want 0", rl)
	}
}

// ─── Types / SuitableMembers ────────────────────────────────────────────

// TestGroupsDomainTypesMapsThroughDraftTypes verifies Types translates the
// backend's HeatingGroupType list into the domain's group.Type shape.
func TestGroupsDomainTypesMapsThroughDraftTypes(t *testing.T) {
	t.Parallel()
	reg := central.NewRegistry()
	w := clientpkg.NewValueWriter()
	fb := newFakeGroupWriterOps()
	fb.draftTypes = []backends.HeatingGroupType{{ID: "hmip.heating.group", LabelKey: "lblHmip"}}
	registerCentralWithClient(t, reg, w, "ccu-01", fb)

	d := NewGroupsDomain(reg, w)
	got, err := d.Types(context.Background(), "ccu-01")
	if err != nil {
		t.Fatalf("Types: %v", err)
	}
	want := group.Type{ID: "hmip.heating.group", LabelKey: "lblHmip"}
	if len(got) != 1 || got[0] != want {
		t.Errorf("Types = %+v, want [%+v]", got, want)
	}
}

// TestGroupsDomainSuitableMembersMapsThrough verifies SuitableMembers
// translates the backend's assignable/leftover candidate lists.
func TestGroupsDomainSuitableMembersMapsThrough(t *testing.T) {
	t.Parallel()
	reg := central.NewRegistry()
	w := clientpkg.NewValueWriter()
	fb := newFakeGroupWriterOps()
	fb.suitable = backends.SuitableHeatingGroupMembers{
		Assignable: []backends.HeatingGroupMember{{ID: "000AAA:1", SerialNumber: "000AAA", Type: "SWITCH_ACTUATOR"}},
		Leftover:   []backends.HeatingGroupMember{{ID: "000BBB:1", SerialNumber: "000BBB", Type: "SENSOR_WINDOW"}},
	}
	registerCentralWithClient(t, reg, w, "ccu-01", fb)

	d := NewGroupsDomain(reg, w)
	got, err := d.SuitableMembers(context.Background(), "ccu-01", "hmip.heating.group")
	if err != nil {
		t.Fatalf("SuitableMembers: %v", err)
	}
	if len(got.Assignable) != 1 || got.Assignable[0].Address != "000AAA:1" {
		t.Errorf("Assignable = %+v", got.Assignable)
	}
	if len(got.Leftover) != 1 || got.Leftover[0].Address != "000BBB:1" {
		t.Errorf("Leftover = %+v", got.Leftover)
	}
}

// ─── writerFor ──────────────────────────────────────────────────────────

// TestGroupsDomainWriterForSingleCentralResolves verifies the empty-name
// convenience path: with exactly one registered central, writerFor("")
// resolves it without requiring the caller to name it explicitly.
func TestGroupsDomainWriterForSingleCentralResolves(t *testing.T) {
	t.Parallel()
	reg := central.NewRegistry()
	w := clientpkg.NewValueWriter()
	fb := newFakeGroupWriterOps()
	registerCentralWithClient(t, reg, w, "ccu-only", fb)

	d := NewGroupsDomain(reg, w)
	got, err := d.writerFor("")
	if err != nil {
		t.Fatalf("writerFor: %v", err)
	}
	if got == nil {
		t.Fatal("writerFor returned a nil writer without an error")
	}
}

// TestGroupsDomainWriterForUnsupportedBackend verifies that a backend
// lacking the group-writer methods (the plain fakeOperations stub) surfaces
// backends.ErrUnsupported rather than a type-assertion panic.
func TestGroupsDomainWriterForUnsupportedBackend(t *testing.T) {
	t.Parallel()
	reg := central.NewRegistry()
	w := clientpkg.NewValueWriter()
	registerCentralWithClient(t, reg, w, "ccu-01", &fakeOperations{kind: backends.KindCCU})

	d := NewGroupsDomain(reg, w)
	_, err := d.writerFor("ccu-01")
	if !errors.Is(err, backends.ErrUnsupported) {
		t.Fatalf("err = %v, want backends.ErrUnsupported", err)
	}
}
