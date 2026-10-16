// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package docsref

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const wsapiPath = "assets/wsapi.json"

// wsEntry is one element of the `commands` array in assets/wsapi.json.
// Broadcasts carry kind "broadcast", a topic and a payload schema name;
// client commands carry their argument map instead.
type wsEntry struct {
	Name        string          `json:"name"`
	Category    string          `json:"category"`
	Description string          `json:"description"`
	Kind        string          `json:"kind"`
	Topic       string          `json:"topic"`
	Payload     string          `json:"payload"`
	Args        json.RawMessage `json:"args"`
}

func websocketPage(root string) ([]byte, error) {
	raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(wsapiPath))) //nolint:gosec // G304: a fixed asset path below the repository root the generator is run in
	if err != nil {
		return nil, fmt.Errorf("docsref: read %s: %w", wsapiPath, err)
	}
	var doc struct {
		Version  string    `json:"version"`
		Commands []wsEntry `json:"commands"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("docsref: parse %s: %w", wsapiPath, err)
	}

	broadcasts := make(map[string][]wsEntry)
	commands := make(map[string][]wsEntry)
	for _, e := range doc.Commands {
		if e.Kind == "broadcast" {
			broadcasts[e.Category] = append(broadcasts[e.Category], e)
		} else {
			commands[e.Category] = append(commands[e.Category], e)
		}
	}

	var b strings.Builder
	b.WriteString("# WebSocket reference\n\n")
	b.WriteString(generatedMarker("`" + wsapiPath + "`"))
	fmt.Fprintf(&b, `
Every server-pushed broadcast and every client command of the WebSocket
API, generated from `+"`%s`"+` (catalogue version `+"`%s`"+`). The envelope,
subscription matching, central scoping and resume rules are described in
[External Client Wire Contract](../external-clients/topic-hierarchy.md);
authentication and connection set-up in
[REST & WebSocket API](../integrations/rest-ws.md).

In a topic, `+"`{name}`"+` placeholders stand for one topic segment. In an
argument list, a type ending in `+"`?`"+` marks an optional argument.
`, wsapiPath, doc.Version)

	ids := anchors{"broadcasts": true, "commands": true}
	b.WriteString("\n## Broadcasts {#broadcasts}\n")
	for _, cat := range sortedKeys(broadcasts) {
		fmt.Fprintf(&b, "\n### `%s` {#%s}\n\n", cat, ids.next("broadcasts", cat))
		entries := broadcasts[cat]
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
		rows := make([][]string, 0, len(entries))
		for _, e := range entries {
			rows = append(rows, []string{code(e.Name), code(e.Topic), code(e.Payload), cell(e.Description)})
		}
		table(&b, []string{"Name", "Topic", "Payload", "Description"}, rows)
	}

	b.WriteString("\n## Commands {#commands}\n")
	for _, cat := range sortedKeys(commands) {
		fmt.Fprintf(&b, "\n### `%s` {#%s}\n\n", cat, ids.next("commands", cat))
		entries := commands[cat]
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
		rows := make([][]string, 0, len(entries))
		for _, e := range entries {
			args, err := renderArgs(e.Args)
			if err != nil {
				return nil, fmt.Errorf("docsref: %s args of %q: %w", wsapiPath, e.Name, err)
			}
			rows = append(rows, []string{code(e.Name), code(args), cell(e.Description)})
		}
		table(&b, []string{"Name", "Arguments", "Description"}, rows)
	}
	return []byte(b.String()), nil
}

// renderArgs lists a command's arguments as `name: type` in the order the
// catalogue declares them, which puts the required ones first.
func renderArgs(raw json.RawMessage) (string, error) {
	if len(bytes.TrimSpace(raw)) == 0 || string(bytes.TrimSpace(raw)) == "null" {
		return "", nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	tok, err := dec.Token()
	if err != nil {
		return "", err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return "", errors.New("args is not an object")
	}
	var parts []string
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return "", err
		}
		key, ok := keyTok.(string)
		if !ok {
			return "", errors.New("args key is not a string")
		}
		var typ string
		if err := dec.Decode(&typ); err != nil {
			return "", fmt.Errorf("arg %q: %w", key, err)
		}
		parts = append(parts, key+": "+typ)
	}
	return strings.Join(parts, ", "), nil
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
