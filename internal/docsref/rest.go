// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package docsref

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
)

const openapiPath = "assets/openapi.yaml"

// restMethodOrder fixes the row order of operations sharing one path.
var restMethodOrder = []string{"GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS", "TRACE", "CONNECT"}

type restRow struct {
	method, path, summary, access string
}

func restPage(root string) ([]byte, error) {
	loader := openapi3.NewLoader()
	doc, err := loader.LoadFromFile(filepath.Join(root, filepath.FromSlash(openapiPath)))
	if err != nil {
		return nil, fmt.Errorf("docsref: load %s: %w", openapiPath, err)
	}
	base := ""
	if len(doc.Servers) > 0 {
		base = doc.Servers[0].URL
	}

	groups := make(map[string][]restRow)
	for _, path := range doc.Paths.InMatchingOrder() {
		item := doc.Paths.Value(path)
		ops := item.Operations()
		for _, method := range restMethodOrder {
			op, ok := ops[method]
			if !ok {
				continue
			}
			tag := "untagged"
			if len(op.Tags) > 0 {
				tag = op.Tags[0]
			}
			groups[tag] = append(groups[tag], restRow{
				method:  method,
				path:    path,
				summary: summaryOf(op),
				access:  accessOf(op, doc.Security),
			})
		}
	}

	tagDesc := make(map[string]string)
	for _, t := range doc.Tags {
		tagDesc[t.Name] = t.Description
	}

	var b strings.Builder
	b.WriteString("# REST API reference\n\n")
	b.WriteString(generatedMarker("`" + openapiPath + "`"))
	fmt.Fprintf(&b, `
An index of every REST operation, generated from `+"`%s`"+`. Paths are
relative to the server base `+"`%s`"+`. Request and response schemas are
in the OpenAPI document itself; authentication, versioning and examples
in [REST & WebSocket API](../integrations/rest-ws.md).

**Access** is what the operation's `+"`security`"+` declaration (or the
document-wide default it inherits) requires: `+"`public`"+` needs no
credentials, `+"`authenticated`"+` any signed-in identity, and a role name
(`+"`viewer`"+`, `+"`operator`"+`, `+"`admin`"+`) that role or a higher one.
`, openapiPath, base)

	ids := anchors{}
	for _, tag := range sortedKeys(groups) {
		fmt.Fprintf(&b, "\n## %s {#%s}\n\n", tag, ids.next("", tag))
		if d := strings.TrimSpace(tagDesc[tag]); d != "" {
			b.WriteString(strings.Join(strings.Fields(d), " ") + "\n\n")
		}
		rows := groups[tag]
		sort.SliceStable(rows, func(i, j int) bool {
			if rows[i].path != rows[j].path {
				return rows[i].path < rows[j].path
			}
			return methodRank(rows[i].method) < methodRank(rows[j].method)
		})
		cells := make([][]string, 0, len(rows))
		for _, r := range rows {
			cells = append(cells, []string{r.method, code(r.path), cell(r.summary), r.access})
		}
		table(&b, []string{"Method", "Path", "Summary", "Access"}, cells)
	}
	return []byte(b.String()), nil
}

func methodRank(m string) int {
	for i, x := range restMethodOrder {
		if x == m {
			return i
		}
	}
	return len(restMethodOrder)
}

// summaryOf returns the operation summary, falling back to the first line
// of its description for operations that carry no summary.
func summaryOf(op *openapi3.Operation) string {
	if s := strings.TrimSpace(op.Summary); s != "" {
		return s
	}
	for _, line := range strings.Split(op.Description, "\n") {
		if l := strings.TrimSpace(line); l != "" {
			return l
		}
	}
	return ""
}

// accessOf classifies the effective security requirement: an empty list
// is public, an openIdConnect scope list names the minimum role, and any
// other requirement means some authenticated identity.
func accessOf(op *openapi3.Operation, global openapi3.SecurityRequirements) string {
	reqs := global
	if op.Security != nil {
		reqs = *op.Security
	}
	if len(reqs) == 0 {
		return "public"
	}
	var roles []string
	seen := make(map[string]bool)
	for _, req := range reqs {
		for _, scope := range req["openIdConnect"] {
			if !seen[scope] {
				seen[scope] = true
				roles = append(roles, scope)
			}
		}
	}
	if len(roles) == 0 {
		return "authenticated"
	}
	return strings.Join(roles, ", ")
}
