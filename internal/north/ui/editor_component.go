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
		Prefixes []string `json:"prefixes"`
	}
	if err := json.Unmarshal(raw, &routes); err != nil {
		return nil, fmt.Errorf("editor component routes: %w", err)
	}
	if len(routes.Prefixes) == 0 {
		return nil, errors.New("editor component routes: empty list")
	}
	sum := sha256.Sum256(module)
	hash := hex.EncodeToString(sum[:])
	return &EditorComponent{
		manifest: hmapi.EditorComponentManifest{
			ModuleURL: "/app/" + editorModulePrefix + hash + ".js",
			Hash:      hash,
			Routes:    routes.Prefixes,
		},
		module: module,
	}, nil
}

// Manifest returns the manifest. The Routes slice is a copy.
func (c *EditorComponent) Manifest() hmapi.EditorComponentManifest {
	m := c.manifest
	m.Routes = append([]string(nil), c.manifest.Routes...)
	return m
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
