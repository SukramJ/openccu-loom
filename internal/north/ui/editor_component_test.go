// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package ui

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"

	"github.com/SukramJ/openccu-loom/pkg/hmapi"
)

func componentFS(module string) fstest.MapFS {
	return fstest.MapFS{
		"component/editor.js":   {Data: []byte(module)},
		"component/routes.json": {Data: []byte(`{"routes":[{"method":"GET","prefix":"/devices"},{"method":"POST","prefix":"/devices/*/paramsets"}]}`)},
	}
}

func TestEditorComponentManifestCarriesTheModuleHashAndRoutes(t *testing.T) {
	comp, err := loadEditorComponent(componentFS("export const x = 1;"))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("export const x = 1;"))
	want := hex.EncodeToString(sum[:])
	m := comp.Manifest()
	if m.Hash != want {
		t.Fatalf("hash = %q, want %q", m.Hash, want)
	}
	if m.ModuleURL != "/app/component/editor-"+want+".js" {
		t.Fatalf("module_url = %q", m.ModuleURL)
	}
	if len(m.Routes) != 2 || m.Routes[0] != (hmapi.EditorRoute{Method: "GET", Prefix: "/devices"}) ||
		m.Routes[1] != (hmapi.EditorRoute{Method: "POST", Prefix: "/devices/*/paramsets"}) {
		t.Fatalf("routes = %v", m.Routes)
	}
	// A different module is a different hash: the URL must follow the content.
	other, err := loadEditorComponent(componentFS("export const x = 2;"))
	if err != nil {
		t.Fatal(err)
	}
	if other.Manifest().Hash == m.Hash {
		t.Fatal("two modules share one hash")
	}
}

func TestEditorComponentAbsentIsNotBuilt(t *testing.T) {
	_, err := loadEditorComponent(fstest.MapFS{"index.html": {Data: []byte("<html>")}})
	if !errors.Is(err, ErrEditorComponentNotBuilt) {
		t.Fatalf("err = %v, want ErrEditorComponentNotBuilt", err)
	}
}

func TestEditorComponentWithoutRoutesIsRejected(t *testing.T) {
	fsys := componentFS("x")
	fsys["component/routes.json"] = &fstest.MapFile{Data: []byte(`{"routes":[]}`)}
	if _, err := loadEditorComponent(fsys); err == nil || errors.Is(err, ErrEditorComponentNotBuilt) {
		t.Fatalf("err = %v, want a routes error", err)
	}
}

// TestEditorComponentRejectsAWideRoute pins the loader's refusal of an entry a
// relay could read more widely than the editor needs.
func TestEditorComponentRejectsAWideRoute(t *testing.T) {
	for _, entry := range []string{
		`{"method":"HEAD","prefix":"/devices"}`,
		`{"method":"get","prefix":"/devices"}`,
		`{"method":"GET","prefix":"devices"}`,
		`{"method":"GET","prefix":"/devices/"}`,
		`{"method":"GET","prefix":"/devices//x"}`,
		`{"method":"POST","prefix":"/devices/a*"}`,
	} {
		fsys := componentFS("x")
		fsys["component/routes.json"] = &fstest.MapFile{Data: []byte(`{"routes":[` + entry + `]}`)}
		if _, err := loadEditorComponent(fsys); err == nil {
			t.Errorf("%s: accepted, want a routes error", entry)
		}
	}
}

func TestEditorComponentServesOnlyTheCurrentHash(t *testing.T) {
	comp, err := loadEditorComponent(componentFS("export default 1;"))
	if err != nil {
		t.Fatal(err)
	}
	hash := comp.Manifest().Hash

	rec := httptest.NewRecorder()
	path := "component/editor-" + hash + ".js"
	if !comp.serveModule(rec, httptest.NewRequest(http.MethodGet, "/"+path, http.NoBody), path) {
		t.Fatal("hashed module path not handled")
	}
	if rec.Code != http.StatusOK || rec.Body.String() != "export default 1;" {
		t.Fatalf("status %d body %q", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
		t.Fatalf("cache-control = %q", got)
	}
	if got := rec.Header().Get("Content-Type"); got != "text/javascript; charset=utf-8" {
		t.Fatalf("content-type = %q", got)
	}

	// A stale hash is a 404, never the SPA's index.html served as script.
	rec = httptest.NewRecorder()
	stale := "component/editor-0000.js"
	if !comp.serveModule(rec, httptest.NewRequest(http.MethodGet, "/"+stale, http.NoBody), stale) {
		t.Fatal("stale module path fell through to the SPA fallback")
	}
	if rec.Code != http.StatusNotFound {
		t.Fatalf("stale hash status = %d", rec.Code)
	}

	// Any other path is not the component's business.
	if comp.serveModule(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/assets/x.js", http.NoBody), "assets/x.js") {
		t.Fatal("unrelated path claimed by the component")
	}
}
