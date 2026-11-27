// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package ui

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"strings"

	"github.com/SukramJ/openccu-loom/pkg/hmapi"
)

// The device editor as a web component (ADR 0088 decision 3, ADR 0089) is
// a second build of the SPA's sources into spa_dist/component/: the module
// and the list of REST routes the editor calls.
const (
	editorComponentDir    = "component/"
	editorComponentModule = editorComponentDir + "editor.js"
	editorComponentRoutes = editorComponentDir + "routes.json"
	// editorModulePrefix is the hashed name the module is served under,
	// below /app/: component/editor-<hash>.js.
	editorModulePrefix = editorComponentDir + "editor-"
)

// ErrEditorComponentNotBuilt reports a binary built without the component
// bundle — a development build whose SPA was not compiled.
var ErrEditorComponentNotBuilt = errors.New("editor component not built")

// EditorComponent is the embedded component bundle, read once.
type EditorComponent struct {
	manifest hmapi.EditorComponentManifest
	module   []byte
}

// LoadEditorComponent reads the component bundle from the embedded SPA.
// It returns ErrEditorComponentNotBuilt when the bundle is absent.
func LoadEditorComponent() (*EditorComponent, error) {
	sub, err := fs.Sub(spaFS, "spa_dist")
	if err != nil {
		return nil, fmt.Errorf("editor component: %w", err)
	}
	return loadEditorComponent(sub)
}

func loadEditorComponent(fsys fs.FS) (*EditorComponent, error) {
	module, err := fs.ReadFile(fsys, editorComponentModule)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrEditorComponentNotBuilt
	}
	if err != nil {
		return nil, fmt.Errorf("editor component module: %w", err)
	}
	raw, err := fs.ReadFile(fsys, editorComponentRoutes)
	if err != nil {
		return nil, fmt.Errorf("editor component routes: %w", err)
	}
	var routes struct {
		Routes []hmapi.EditorRoute `json:"routes"`
	}
	if err := json.Unmarshal(raw, &routes); err != nil {
		return nil, fmt.Errorf("editor component routes: %w", err)
	}
	if len(routes.Routes) == 0 {
		return nil, errors.New("editor component routes: empty list")
	}
	for _, r := range routes.Routes {
		if err := validEditorRoute(r); err != nil {
			return nil, fmt.Errorf("editor component routes: %w", err)
		}
	}
	sum := sha256.Sum256(module)
	hash := hex.EncodeToString(sum[:])
	return &EditorComponent{
		manifest: hmapi.EditorComponentManifest{
			ModuleURL: "/app/" + editorModulePrefix + hash + ".js",
			Hash:      hash,
			Routes:    routes.Routes,
		},
		module: module,
	}, nil
}

// Manifest returns the manifest. The Routes slice is a copy.
func (c *EditorComponent) Manifest() hmapi.EditorComponentManifest {
	m := c.manifest
	m.Routes = append([]hmapi.EditorRoute(nil), c.manifest.Routes...)
	return m
}

// editorRouteMethods are the methods a relay may be told to forward. HEAD,
// OPTIONS and anything else stay out: the editor never sends them, and a
// relay that forwards whatever the list names must not learn new verbs from
// a build mistake.
var editorRouteMethods = map[string]bool{
	http.MethodGet: true, http.MethodPut: true, http.MethodPost: true,
	http.MethodPatch: true, http.MethodDelete: true,
}

// validEditorRoute rejects an entry a relay could read more widely than the
// editor needs: an unknown method, a relative or slash-terminated prefix, an
// empty segment, or a `*` that is only part of a segment.
func validEditorRoute(r hmapi.EditorRoute) error {
	if !editorRouteMethods[r.Method] {
		return fmt.Errorf("route %q %q: method must be one of GET, PUT, POST, PATCH, DELETE", r.Method, r.Prefix)
	}
	if !strings.HasPrefix(r.Prefix, "/") || strings.HasSuffix(r.Prefix, "/") {
		return fmt.Errorf("route %s %q: prefix must start and must not end with /", r.Method, r.Prefix)
	}
	for _, seg := range strings.Split(r.Prefix[1:], "/") {
		if seg == "" || (seg != "*" && strings.Contains(seg, "*")) {
			return fmt.Errorf("route %s %q: empty segment or partial wildcard", r.Method, r.Prefix)
		}
	}
	return nil
}

// serveModule answers a request for the hashed module name. It reports
// false when path is not the module's hashed name, so the SPA handler
// goes on with its own lookup.
func (c *EditorComponent) serveModule(w http.ResponseWriter, r *http.Request, path string) bool {
	if !strings.HasPrefix(path, editorModulePrefix) {
		return false
	}
	if path != editorModulePrefix+c.manifest.Hash+".js" {
		// A stale hash names a module this daemon no longer has; an
		// index.html fallback would hand the integration HTML as script.
		http.NotFound(w, r)
		return true
	}
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	// The name changes whenever the content does.
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	_, _ = w.Write(c.module)
	return true
}
