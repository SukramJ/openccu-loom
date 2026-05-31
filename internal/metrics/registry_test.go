// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package metrics

import (
	"bytes"
	"strings"
	"testing"
)

func TestCounterIncAndAdd(t *testing.T) {
	r := NewRegistry()
	c := r.Counter("events_total", "Event count")
	c.Inc()
	c.Add(4)
	if got := c.Value(); got != 5 {
		t.Fatalf("value=%d", got)
	}
}

func TestGaugeSetGet(t *testing.T) {
	r := NewRegistry()
	g := r.Gauge("queue_depth", "Current queue depth")
	g.Set(3.5)
	if got := g.Value(); got != 3.5 {
		t.Fatalf("value=%v", got)
	}
}

func TestRegistryRender(t *testing.T) {
	r := NewRegistry()
	r.Counter("c_total", "Counter doc").Add(2)
	r.Gauge("g_depth", "Gauge doc").Set(1.25)

	var buf bytes.Buffer
	if err := r.Render(&buf); err != nil {
		t.Fatalf("render: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		"# HELP c_total Counter doc",
		"# TYPE c_total counter",
		"c_total 2",
		"# HELP g_depth Gauge doc",
		"# TYPE g_depth gauge",
		"g_depth 1.25",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("render missing %q\n%s", want, out)
		}
	}
}

func TestRegistrySortedOutput(t *testing.T) {
	r := NewRegistry()
	r.Counter("zz", "").Inc()
	r.Counter("aa", "").Inc()
	var buf bytes.Buffer
	_ = r.Render(&buf)
	if strings.Index(buf.String(), "aa") > strings.Index(buf.String(), "zz") {
		t.Fatal("metrics should be sorted alphabetically")
	}
}

func TestIdempotentRegister(t *testing.T) {
	r := NewRegistry()
	a := r.Counter("x", "")
	b := r.Counter("x", "")
	a.Inc()
	if b.Value() != 1 {
		t.Fatal("second registration must share storage")
	}
}

func TestRegistryRenderPrometheusText(t *testing.T) {
	t.Parallel()
	reg := NewRegistry()
	c := reg.Counter("test_counter", "a test counter")
	c.Inc()
	c.Inc()
	g := reg.Gauge("test_gauge", "a test gauge")
	g.Set(3.14)

	var buf strings.Builder
	if err := reg.Render(&buf); err != nil {
		t.Fatalf("Render returned error: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "# HELP test_counter a test counter") {
		t.Errorf("missing HELP line in output: %q", out)
	}
	if !strings.Contains(out, "test_counter 2") {
		t.Errorf("missing counter value in output: %q", out)
	}
	if !strings.Contains(out, "# TYPE test_gauge gauge") {
		t.Errorf("missing TYPE line for gauge: %q", out)
	}
}
