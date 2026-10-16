// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package docsref

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/SukramJ/openccu-loom/internal/north/rest/handlers"
	"github.com/SukramJ/openccu-loom/internal/uicatalog"
)

// configGroup is the heading a field is listed under: its first path
// segment, or the first two for the north-bound adapters, which are
// configured independently of one another.
func configGroup(path string) string {
	parts := strings.Split(path, ".")
	if parts[0] == "north" && len(parts) > 2 {
		return parts[0] + "." + parts[1]
	}
	return parts[0]
}

func configurationPage(root string) ([]byte, error) {
	cat, err := uicatalog.Load(root)
	if err != nil {
		return nil, err
	}
	schema := handlers.ConfigSchema()

	groups := make(map[string][]handlers.SchemaField)
	for _, f := range schema.Fields {
		g := configGroup(f.Path)
		groups[g] = append(groups[g], f)
	}
	names := make([]string, 0, len(groups))
	for g := range groups {
		names = append(names, g)
	}
	sort.Strings(names)

	var b strings.Builder
	b.WriteString("# Configuration reference\n\n")
	b.WriteString(generatedMarker("the config schema served by `GET /api/v1/config/schema` and the English help texts in `" + uicatalog.Path + "`"))
	b.WriteString(`
Every configuration field of the daemon, generated from the same field
list that ` + "`GET /api/v1/config/schema`" + ` serves and the English help texts the
Web UI shows beneath each field. How the
layers combine, which environment variables override which key, and how
secrets are stored is explained in [Configuration](../admin/configuration.md).

- **Key** — the dotted YAML path. ` + "`centrals.*`" + ` keys belong to one entry of the
  ` + "`centrals:`" + ` list.
- **Type** — the Go type the value decodes into; ` + "`*bool`" + ` is a tri-state
  switch (true, false or unset).
- **Default** — the explicit default the schema carries for fields whose
  zero value would mislead. An empty cell means the schema carries none.
- **Class** — ` + "`basic`" + `, ` + "`expert`" + ` or ` + "`secret`" + `; secret values are masked in
  ` + "`GET /api/v1/config`" + `.
- **Restart** — ` + "`yes`" + ` when a change only takes effect after a restart.

Fields edited through dedicated surfaces (the bootstrap listener, users
and API tokens, navigation profiles) are not part of this list.
`)
	ids := anchors{}
	for _, g := range names {
		fmt.Fprintf(&b, "\n## `%s` {#%s}\n\n", g, ids.next("", g))
		fields := groups[g]
		sort.Slice(fields, func(i, j int) bool { return fields[i].Path < fields[j].Path })
		rows := make([][]string, 0, len(fields))
		for _, f := range fields {
			restart := "no"
			if f.RestartRequired {
				restart = "yes"
			}
			rows = append(rows, []string{
				code(f.Path),
				code(f.GoType),
				code(renderDefault(f.GoType, f.Default)),
				f.Class,
				restart,
				cell(cat.EN["config.help."+f.Path]),
			})
		}
		table(&b, []string{"Key", "Type", "Default", "Class", "Restart", "Description"}, rows)
	}
	return []byte(b.String()), nil
}

// renderDefault formats a schema default for the reference. A nil or
// zero default renders empty. Durations travel as nanoseconds on the wire
// and are shown in Go duration notation here.
func renderDefault(goType string, v any) string {
	if v == nil || reflect.ValueOf(v).IsZero() {
		return ""
	}
	if goType == "time.Duration" {
		switch n := v.(type) {
		case int64:
			return time.Duration(n).String()
		case int:
			return time.Duration(n).String()
		}
	}
	if s, ok := v.(string); ok {
		return s
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(raw)
}
