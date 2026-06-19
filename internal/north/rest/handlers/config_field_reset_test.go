// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/SukramJ/openccu-loom/internal/configstore"
	sqlitestore "github.com/SukramJ/openccu-loom/internal/store/sqlite"
)

// fakeConfigAdminSvc implements ConfigAdminService for reset handler tests.
type fakeConfigAdminSvc struct {
	getSectionRow    sqlitestore.SectionRow
	getSectionErr    error
	putSectionErr    error
	deleteSectionErr error
	effectiveResult  *configstore.EffectiveResult
	effectiveErr     error

	putCalled    bool
	putJSON      []byte
	deleteCalled bool
}

func (f *fakeConfigAdminSvc) Effective(_ context.Context) (*configstore.EffectiveResult, error) {
	return f.effectiveResult, f.effectiveErr
}

func (f *fakeConfigAdminSvc) GetSection(_ context.Context, _ configstore.Section) (sqlitestore.SectionRow, error) {
	return f.getSectionRow, f.getSectionErr
}

func (f *fakeConfigAdminSvc) PutSection(_ context.Context, _ configstore.Section, valueJSON []byte, _ string) (sqlitestore.SectionRow, error) {
	f.putCalled = true
	f.putJSON = valueJSON
	return sqlitestore.SectionRow{}, f.putSectionErr
}

func (f *fakeConfigAdminSvc) DeleteSection(_ context.Context, _ configstore.Section) error {
	f.deleteCalled = true
	return f.deleteSectionErr
}

func TestOwningSection(t *testing.T) {
	t.Parallel()

	cases := []struct {
		path string
		want configstore.Section
	}{
		{
			path: "north.rest.auth.oidc.client_secret",
			want: configstore.SectionOIDC,
		},
		{
			path: "north.rest.cors",
			want: configstore.SectionREST,
		},
		{
			path: "north.rest",
			want: configstore.SectionREST,
		},
		{
			path: "locale.locale",
			want: configstore.SectionLocale,
		},
		{
			path: "foo.bar",
			want: "",
		},
		{
			path: "",
			want: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			t.Parallel()
			got := owningSection(tc.path)
			if got != tc.want {
				t.Errorf("owningSection(%q) = %q, want %q", tc.path, got, tc.want)
			}
		})
	}
}

func TestDeleteLeaf(t *testing.T) {
	t.Parallel()

	unmarshalObj := func(t *testing.T, s string) map[string]any {
		t.Helper()
		var m map[string]any
		if err := json.Unmarshal([]byte(s), &m); err != nil {
			t.Fatalf("unmarshal test input %q: %v", s, err)
		}
		return m
	}

	marshalObj := func(t *testing.T, m map[string]any) string {
		t.Helper()
		b, err := json.Marshal(m)
		if err != nil {
			t.Fatalf("marshal result: %v", err)
		}
		return string(b)
	}

	cases := []struct {
		name        string
		input       string
		parts       []string
		wantDeleted bool
		checkFn     func(t *testing.T, obj map[string]any)
	}{
		{
			name:        "flat field deleted",
			input:       `{"a":1,"b":2}`,
			parts:       []string{"a"},
			wantDeleted: true,
			checkFn: func(t *testing.T, obj map[string]any) {
				t.Helper()
				if _, ok := obj["a"]; ok {
					t.Error("key 'a' should have been removed")
				}
				if _, ok := obj["b"]; !ok {
					t.Error("key 'b' should remain")
				}
				if len(obj) != 1 {
					t.Errorf("obj has %d keys, want 1: %s", len(obj), marshalObj(t, obj))
				}
			},
		},
		{
			name:        "nested field kept sibling",
			input:       `{"x":{"y":1,"z":2}}`,
			parts:       []string{"x", "y"},
			wantDeleted: true,
			checkFn: func(t *testing.T, obj map[string]any) {
				t.Helper()
				x, ok := obj["x"].(map[string]any)
				if !ok {
					t.Fatal("key 'x' should still be a map")
				}
				if _, ok := x["y"]; ok {
					t.Error("key 'x.y' should have been removed")
				}
				if _, ok := x["z"]; !ok {
					t.Error("key 'x.z' should remain")
				}
			},
		},
		{
			name:        "nested last child pruned",
			input:       `{"x":{"y":1}}`,
			parts:       []string{"x", "y"},
			wantDeleted: true,
			checkFn: func(t *testing.T, obj map[string]any) {
				t.Helper()
				if _, ok := obj["x"]; ok {
					t.Error("key 'x' should have been pruned after its last child was deleted")
				}
				if len(obj) != 0 {
					t.Errorf("obj should be empty, got: %s", marshalObj(t, obj))
				}
			},
		},
		{
			name:        "missing flat field",
			input:       `{"a":1}`,
			parts:       []string{"nope"},
			wantDeleted: false,
			checkFn: func(t *testing.T, obj map[string]any) {
				t.Helper()
				if len(obj) != 1 {
					t.Errorf("obj should be unchanged, got: %s", marshalObj(t, obj))
				}
			},
		},
		{
			name:        "missing nested field",
			input:       `{"x":{"y":1}}`,
			parts:       []string{"x", "nope"},
			wantDeleted: false,
			checkFn: func(t *testing.T, obj map[string]any) {
				t.Helper()
				x, ok := obj["x"].(map[string]any)
				if !ok {
					t.Fatal("key 'x' should still be a map")
				}
				if len(x) != 1 {
					t.Errorf("x should be unchanged, got: %s", marshalObj(t, obj))
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			obj := unmarshalObj(t, tc.input)
			got := deleteLeaf(obj, tc.parts)
			if got != tc.wantDeleted {
				t.Errorf("deleteLeaf() = %v, want %v", got, tc.wantDeleted)
			}
			tc.checkFn(t, obj)
		})
	}
}

func TestResetConfigField(t *testing.T) {
	t.Parallel()

	makeRequest := func(t *testing.T, path string) *http.Request {
		t.Helper()
		req := httptest.NewRequest(http.MethodDelete, "/api/v1/config/field/"+path, http.NoBody)
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("path", path)
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
		return req
	}

	t.Run("nested field pruned, PutSection called", func(t *testing.T) {
		t.Parallel()
		fake := &fakeConfigAdminSvc{
			getSectionRow: sqlitestore.SectionRow{
				Section:   string(configstore.SectionREST),
				ValueJSON: []byte(`{"cors":["x"],"rate_limit":{"enabled":true}}`),
			},
		}
		w := httptest.NewRecorder()
		ResetConfigField(fake, nil).ServeHTTP(w, makeRequest(t, "north.rest.rate_limit.enabled"))

		if w.Code != http.StatusNoContent {
			t.Fatalf("expected 204, got %d body=%s", w.Code, w.Body.String())
		}
		if !fake.putCalled {
			t.Fatal("PutSection should have been called")
		}
		if fake.deleteCalled {
			t.Fatal("DeleteSection should NOT have been called")
		}
		var result map[string]any
		if err := json.Unmarshal(fake.putJSON, &result); err != nil {
			t.Fatalf("PutSection JSON invalid: %v", err)
		}
		if _, ok := result["cors"]; !ok {
			t.Error("result JSON should still contain 'cors' key")
		}
		if _, ok := result["rate_limit"]; ok {
			t.Error("result JSON should NOT contain 'rate_limit' key (pruned as empty)")
		}
	})

	t.Run("last field causes DeleteSection", func(t *testing.T) {
		t.Parallel()
		fake := &fakeConfigAdminSvc{
			getSectionRow: sqlitestore.SectionRow{
				Section:   string(configstore.SectionMQTT),
				ValueJSON: []byte(`{"enabled":true}`),
			},
		}
		w := httptest.NewRecorder()
		ResetConfigField(fake, nil).ServeHTTP(w, makeRequest(t, "north.mqtt.enabled"))

		if w.Code != http.StatusNoContent {
			t.Fatalf("expected 204, got %d body=%s", w.Code, w.Body.String())
		}
		if !fake.deleteCalled {
			t.Fatal("DeleteSection should have been called when JSON becomes empty")
		}
		if fake.putCalled {
			t.Fatal("PutSection should NOT have been called")
		}
	})

	t.Run("path equals section, DeleteSection called", func(t *testing.T) {
		t.Parallel()
		fake := &fakeConfigAdminSvc{
			getSectionRow: sqlitestore.SectionRow{
				Section:   string(configstore.SectionMQTT),
				ValueJSON: []byte(`{"enabled":true}`),
			},
		}
		w := httptest.NewRecorder()
		ResetConfigField(fake, nil).ServeHTTP(w, makeRequest(t, "north.mqtt"))

		if w.Code != http.StatusNoContent {
			t.Fatalf("expected 204, got %d body=%s", w.Code, w.Body.String())
		}
		if !fake.deleteCalled {
			t.Fatal("DeleteSection should have been called when path == section")
		}
		if fake.putCalled {
			t.Fatal("PutSection should NOT have been called")
		}
	})

	t.Run("GetSection returns ErrSectionNotFound, 204 idempotent", func(t *testing.T) {
		t.Parallel()
		fake := &fakeConfigAdminSvc{
			getSectionErr: sqlitestore.ErrSectionNotFound,
		}
		w := httptest.NewRecorder()
		ResetConfigField(fake, nil).ServeHTTP(w, makeRequest(t, "north.mqtt.enabled"))

		if w.Code != http.StatusNoContent {
			t.Fatalf("expected 204, got %d", w.Code)
		}
		if fake.putCalled {
			t.Fatal("PutSection should NOT have been called")
		}
		if fake.deleteCalled {
			t.Fatal("DeleteSection should NOT have been called")
		}
	})

	t.Run("field not in JSON, 204 idempotent", func(t *testing.T) {
		t.Parallel()
		fake := &fakeConfigAdminSvc{
			getSectionRow: sqlitestore.SectionRow{
				Section:   string(configstore.SectionREST),
				ValueJSON: []byte(`{"cors":["x"]}`),
			},
		}
		w := httptest.NewRecorder()
		ResetConfigField(fake, nil).ServeHTTP(w, makeRequest(t, "north.rest.rate_limit.enabled"))

		if w.Code != http.StatusNoContent {
			t.Fatalf("expected 204, got %d body=%s", w.Code, w.Body.String())
		}
		if fake.putCalled {
			t.Fatal("PutSection should NOT have been called")
		}
		if fake.deleteCalled {
			t.Fatal("DeleteSection should NOT have been called")
		}
	})

	t.Run("unknown path returns 400", func(t *testing.T) {
		t.Parallel()
		fake := &fakeConfigAdminSvc{}
		w := httptest.NewRecorder()
		ResetConfigField(fake, nil).ServeHTTP(w, makeRequest(t, "foo.bar.baz"))

		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d body=%s", w.Code, w.Body.String())
		}
	})

	t.Run("nil svc returns 503", func(t *testing.T) {
		t.Parallel()
		w := httptest.NewRecorder()
		ResetConfigField(nil, nil).ServeHTTP(w, makeRequest(t, "north.mqtt.enabled"))

		if w.Code != http.StatusServiceUnavailable {
			t.Fatalf("expected 503, got %d body=%s", w.Code, w.Body.String())
		}
	})

	t.Run("PutSection error returns 500", func(t *testing.T) {
		t.Parallel()
		// JSON has two top-level keys so after removing rate_limit.enabled the
		// rate_limit object is pruned, but "cors" remains — obj is non-empty
		// and PutSection is called (where we inject an error).
		fake := &fakeConfigAdminSvc{
			getSectionRow: sqlitestore.SectionRow{
				Section:   string(configstore.SectionREST),
				ValueJSON: []byte(`{"cors":["x"],"rate_limit":{"enabled":true}}`),
			},
			putSectionErr: errors.New("db error"),
		}
		w := httptest.NewRecorder()
		ResetConfigField(fake, nil).ServeHTTP(w, makeRequest(t, "north.rest.rate_limit.enabled"))

		if w.Code != http.StatusInternalServerError {
			t.Fatalf("expected 500, got %d body=%s", w.Code, w.Body.String())
		}
	})
}
