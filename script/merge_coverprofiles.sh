#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
# Copyright (C) 2026 SukramJ.
#
# merge_coverprofiles.sh — concatenate Go coverage profiles into one.
#
#   script/merge_coverprofiles.sh <output> <input>...
#
# A Go text coverage profile is one `mode: <set|count|atomic>` header line
# followed by one `file:startLine.startCol,endLine.endCol numStmt count` row
# per basic block. `go tool cover` rejects a file carrying more than one
# header, so merging means keeping the first header and dropping the rest.
#
# The CI shards test disjoint package sets and no shard passes -coverpkg, so
# each block is reported by exactly one shard: the rows need concatenating,
# not summing. A mismatched header means one leg ran a different -covermode
# and the resulting total would be meaningless, so that is a hard error.

set -euo pipefail

OUT="${1:?usage: merge_coverprofiles.sh <output> <input>...}"
shift

if [[ $# -eq 0 ]]; then
    echo "merge_coverprofiles: no input profiles given" >&2
    exit 2
fi

mode=""
tmp="$(mktemp)"
trap 'rm -f "$tmp"' EXIT

for f in "$@"; do
    if [[ ! -f "$f" ]]; then
        echo "merge_coverprofiles: input profile $f not found" >&2
        exit 2
    fi
    header="$(head -n 1 "$f")"
    if [[ "$header" != mode:* ]]; then
        echo "merge_coverprofiles: $f does not start with a mode: header" >&2
        exit 2
    fi
    if [[ -z "$mode" ]]; then
        mode="$header"
    elif [[ "$header" != "$mode" ]]; then
        echo "merge_coverprofiles: $f has header '$header', expected '$mode'" >&2
        exit 2
    fi
    tail -n +2 "$f" >>"$tmp"
done

{
    printf '%s\n' "$mode"
    cat "$tmp"
} >"$OUT"

echo "merged $# profile(s) into $OUT ($(wc -l <"$OUT" | tr -d ' ') lines, $mode)"
