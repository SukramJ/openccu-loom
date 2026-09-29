// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package occulited_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/SukramJ/godevccu/pkg/litefake"

	"github.com/SukramJ/openccu-loom/internal/client/transport/occulited"
)

func TestDetectLiteBox(t *testing.T) {
	f := startFake(t, litefake.Options{})
	c := newClient(t, f.URL(), "")
	d, err := c.Detect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if d.Kind != occulited.DetectLite || d.Version == nil || !d.Pairing || d.Majors["rpc"] != 1 {
		t.Errorf("detection %+v", d)
	}
	if d.Version.Capabilities == nil || d.Version.Capabilities.Limits == nil || d.Version.Capabilities.Limits.StreamsPerToken != 2 {
		t.Errorf("capabilities %+v", d.Version.Capabilities)
	}
	v, err := c.MetaVersion(context.Background())
	if err != nil || v.API != "meta" || len(v.Raw) == 0 {
		t.Errorf("MetaVersion %+v %v", v, err)
	}
}

func TestDetectStartingBox(t *testing.T) {
	f := startFake(t, litefake.Options{StartNotReady: true})
	d, err := newClient(t, f.URL(), "").Detect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if d.Kind != occulited.DetectLiteNotReady || d.RetryAfter != 5*time.Second {
		t.Errorf("detection %+v", d)
	}
}

// ccuLike answers like a CCU: no meta API, checkrega OK. It fails the
// test when the lite token reaches it.
func ccuLike(t *testing.T, rega string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ise/checkrega.cgi" {
			if r.Header.Get("Authorization") != "" {
				t.Errorf("token sent to the CCU probe")
			}
			_, _ = io.WriteString(w, rega)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestDetectCCUAndUnknown(t *testing.T) {
	d, err := newClient(t, ccuLike(t, "OK\n").URL, "olt_x").Detect(context.Background())
	if err != nil || d.Kind != occulited.DetectCCU {
		t.Errorf("CCU: %+v %v", d, err)
	}
	d, err = newClient(t, ccuLike(t, "starting").URL, "olt_x").Detect(context.Background())
	if err != nil || d.Kind != occulited.DetectUnknown {
		t.Errorf("unknown: %+v %v", d, err)
	}
	// A lite box's HTML shell answers checkrega with HTML, never OK.
	f := startFake(t, litefake.Options{})
	f.Deviate(litefake.DeviateVersionHTML, true)
	d, err = newClient(t, f.URL(), "").Detect(context.Background())
	if err != nil || d.Kind != occulited.DetectUnknown {
		t.Errorf("html: %+v %v", d, err)
	}
}

func TestDetectRefusesAHigherMajor(t *testing.T) {
	f := startFake(t, litefake.Options{})
	f.Deviate(litefake.DeviateVersionMajor, true)
	d, err := newClient(t, f.URL(), "").Detect(context.Background())
	var me *occulited.MajorError
	if !errors.Is(err, occulited.ErrUnsupportedMajor) || !errors.As(err, &me) || me.API != "rpc" || me.Got != 2 {
		t.Errorf("err %v", err)
	}
	if d.Kind != occulited.DetectLite {
		t.Errorf("detection %+v", d)
	}
}

func TestVersionWithoutCapabilitiesIsAnOlderBox(t *testing.T) {
	v := occulited.Version{API: "meta"}
	if v.Pairing() || v.Majors()["auth"] != 1 || v.CheckMajors() != nil {
		t.Errorf("older box: pairing %v majors %v", v.Pairing(), v.Majors())
	}
	v.Capabilities = &occulited.Capabilities{APIs: map[string]int{"future": 9, "meta": 1}}
	if err := v.CheckMajors(); err != nil {
		t.Errorf("an unused API refused: %v", err)
	}
}

func TestDetectUnreachable(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	if _, err := newClient(t, url, "").Detect(context.Background()); err == nil {
		t.Error("unreachable URL detected")
	}
}
