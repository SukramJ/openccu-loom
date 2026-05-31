#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
# Copyright (C) 2026 openccu-loom authors.
#
# doc_comment_sweep.py — remove wave/audit/phase tags from Go doc-comments.
# Usage: python3 script/doc_comment_sweep.py [--dry-run] [--pass2]
#
# Scope: all *.go files under internal/, pkg/, cmd/
# Excludes: *_test.go, internal/north/mqtt/ha_*.go

import re
import subprocess
import sys
import os

DRY_RUN = '--dry-run' in sys.argv
PASS2 = '--pass2' in sys.argv or 'pass2' in sys.argv

AUDIT_TAG = r'(?:V\d+-N\d+|A\d+-L\d+|M\d{3,}(?:[a-z])?|G-\d+|L-\d{4,}|W\d+-[A-Z]\d*|Phase-?\d+)'
MULTI_TAG = AUDIT_TAG + r'(?:\s*[,/]\s*' + AUDIT_TAG + r')*'


def clean_comment_line_pass1(line: str) -> str:
    """Pass 1: Remove audit/wave/phase tags from a comment line."""

    # Step 1: Remove trailing M-tag inside py-ref: "(context.py, M7032)" → "(context.py)"
    line = re.sub(r'(\([^)]*?\.py[^)]*?),\s*' + MULTI_TAG + r'(\))', r'\1\2', line)

    # Step 2: Remove " — TAG." or " — TAG" at end of line
    line = re.sub(r'\s+[—–]\s+(' + MULTI_TAG + r')\.$', '.', line)
    line = re.sub(r'\s+[—–]\s+(' + MULTI_TAG + r')$', '', line)

    # Step 3: Remove " (TAG)." / " (TAG, P\d)." at end of line → "."
    line = re.sub(r'\s+\(\s*' + MULTI_TAG + r'(?:\s*[,/]\s*' + MULTI_TAG + r')*\s*\)\.\s*$', '.', line)
    line = re.sub(r'\s+\(\s*' + MULTI_TAG + r'\s*,\s*P\d\s*\)\.\s*$', '.', line)
    line = re.sub(r'\s+\(\s*' + MULTI_TAG + r'(?:\s*[,/]\s*' + MULTI_TAG + r')*\s*\)\s*$', '', line)
    line = re.sub(r'\s+\(\s*' + MULTI_TAG + r'\s*,\s*P\d\s*\)\s*$', '', line)

    # Step 4: Mid-line inline standalone audit tag parens
    line = re.sub(r'\s+\(\s*' + MULTI_TAG + r'(?:\s*[,/]\s*' + MULTI_TAG + r')*\s*\)(?=[\s,.])', '', line)
    line = re.sub(r'\s+\(\s*' + MULTI_TAG + r'\s*,\s*P\d\s*\)(?=[\s,.])', '', line)

    # Step 5: Remove multi-range audit spans like "(M7032–M7039)"
    line = re.sub(r'\s+\(\s*M\d+[–—-]+M\d+\s*\)', '', line)

    # Step 6: Remove standalone audit-content parens (non-py-ref)
    def remove_audit_paren(m: re.Match) -> str:
        content = m.group(1)
        if '.py' in content or '.go' in content or ':' in content:
            return m.group(0)
        return ''
    line = re.sub(r'(?<=[\s.])\((' + AUDIT_TAG + r'[^)]*)\)\.?', remove_audit_paren, line)

    # Step 7: "TAG (P\d)." or "TAG (P\d)" as entire comment content
    line = re.sub(r'^(\s*//\s*)' + AUDIT_TAG + r'\s+\(P\d\)\.\s*$', r'\1', line)
    line = re.sub(r'^(\s*//\s*)' + AUDIT_TAG + r'\s+\(P\d\)\s*$', r'\1', line)

    # Step 8: "TAG word-label:" at start of comment (e.g. "// G-01 guard:", "// A6-L07 follow-up:")
    line = re.sub(r'^(\s*//\s*)' + AUDIT_TAG + r'\s+[\w-]+:\s*', r'\1', line)
    line = re.sub(r'^(\s*//\s*)' + AUDIT_TAG + r':\s*', r'\1', line)

    # Step 9: "TAG parity item:?" at start
    line = re.sub(r'^(\s*//\s*)' + AUDIT_TAG + r'\s+parity item:?\s*', r'\1', line)

    # Step 10: "Closes TAG[parity item]?" / "closes TAG"
    line = re.sub(r'\bCloses?\s+' + AUDIT_TAG + r'\s+parity item\.?', '', line, flags=re.IGNORECASE)
    line = re.sub(r'\bCloses?\s+' + MULTI_TAG + r'\.?', '', line, flags=re.IGNORECASE)
    line = re.sub(r'\bCloses?\s+parity_audit(?:\.md)?\s+gap\s+\*\*?' + AUDIT_TAG + r'\*\*?\.?', '', line, flags=re.IGNORECASE)
    line = re.sub(r'\bCloses?\s+parity\s+drift\s+' + AUDIT_TAG + r'\.?', '', line, flags=re.IGNORECASE)

    # Step 11: "Corresponds to TAG from the parity sprint[.]"
    line = re.sub(r'\bCorresponds to ' + MULTI_TAG + r' from the parity sprint\.?', '', line)

    # Step 12: Standalone "TAG." / "TAG" as entire comment content
    line = re.sub(r'^(\s*//\s*)' + MULTI_TAG + r'\.\s*$', r'\1', line)
    line = re.sub(r'^(\s*//\s*)' + MULTI_TAG + r'\s*$', r'\1', line)

    # Step 13: parity_audit / parity_request refs
    line = re.sub(r'\bparity_audit(?:\.md)?(?:\s*[§#\d.]*)?', '', line)
    line = re.sub(r'\bparity_request\.md\b', '', line)

    # Step 14: Phase-N
    line = re.sub(r'\bPhase-?\s*\d+\b', '', line)

    # Step 15: inline "(M27a — parity_audit ...)"
    line = re.sub(r'\s*\(M\d+[a-z]?\s*[—–-]+\s*parity_audit[^)]*\)', '', line)

    # Step 16: "audit TAG" references
    line = re.sub(r'\baudit\s+' + AUDIT_TAG + r'\.?', '', line)

    # Step 17: Wave/Welle references
    line = re.sub(
        r'\bWave-?\s*\d+\s*[A-Za-z.]*(?:\s+[A-Za-z.]+(?:\s*[\d.]+)?)*?\b',
        lambda m: '' if re.search(r'\d', m.group()) else m.group(),
        line,
    )
    line = re.sub(r'\bWave-\w+\b', '', line)
    line = re.sub(r'\bWave Schedule\b', '', line)
    line = re.sub(r'\bWelle\s*\d+\b', '', line)
    line = re.sub(r'\bW\d+-[A-Z]\d*\b', '', line)

    # Step 18: migration step N
    line = re.sub(r'\bmigration step \d+\b', '', line, flags=re.IGNORECASE)

    # Step 19: Section header tag cleanup
    line = re.sub(r'(//\s*[───]+\s*)M\d+(?:\+M\d+)*\s*[—–]\s*', r'\1', line)
    line = re.sub(r'(//\s*-+\s*)' + AUDIT_TAG + r'(?:[–—]+' + AUDIT_TAG + r')*[–—:]+\s*', r'\1', line)

    # Cleanup
    line = re.sub(r',\s*\)', ')', line)
    line = re.sub(r'\(\s*,\s*', '(', line)
    line = re.sub(r'\s*,\s*\)', ')', line)
    line = re.sub(r'\s+[—–]\s*$', '', line)
    line = re.sub(r'\s*[—–]\s*$', '', line)
    line = re.sub(r'\s+/\s*$', '', line)
    line = re.sub(r'  +', ' ', line)
    line = re.sub(r'\s+$', '', line)
    line = re.sub(r'\s+\.$', '.', line)

    return line


def clean_comment_line_pass2(line: str) -> str:
    """Pass 2: Handle patterns missed in pass 1."""

    # Pattern A: trailing " TAG." at end of comment — remove " TAG"
    line = re.sub(r'\s+' + MULTI_TAG + r'\.\s*$', '', line)
    # " TAG / TAG" or "TAG / TAG." at end
    line = re.sub(r'\s+' + AUDIT_TAG + r'\s*/\s*' + AUDIT_TAG + r'\s*$', '', line)
    # "/ TAG." at end — only when / is NOT part of //
    line = re.sub(r'(?<!/)\s*/\s*' + AUDIT_TAG + r'\s*$', '', line)

    # Pattern C: M-range / M-list section headers at start of comment content
    # "// M6145–M6152: text" / "// M1104 — text" / "// M6170/M6171: text"
    line = re.sub(r'^(\s*//\s+)M\d+(?:[–/+]M\d+)*\s*[—:]\s*', r'\1', line)
    # "// TAG —" or "// TAG:" at start (for all audit tag types)
    line = re.sub(r'^(\s*//\s+)' + AUDIT_TAG + r'\s*[—:]\s*', r'\1', line)

    # Inline "// TAG:" anywhere in line (field comments, inline comments)
    line = re.sub(r'(//\s*)' + AUDIT_TAG + r':\s*', r'\1', line)

    # "// A5-L22" at end of code line (inline trailing comment — pure audit tag)
    # Matches: code content, then "  // TAG" at end (not a comment-only line)
    line = re.sub(r'^(\s*\S.+?)\s+//\s+' + AUDIT_TAG + r'\s*$', r'\1', line)

    # Pattern F: "(W6 parity, G-52)" — paren with W-wave parity ref
    line = re.sub(r'\s+\(\s*W\d+\s+parity[,\s]*' + AUDIT_TAG + r'\s*\)', '', line)

    # Pattern G: "L-7002 (A7):" at start of comment
    line = re.sub(r'^(\s*//\s*)' + AUDIT_TAG + r'\s+\([A-Z]\d+\):\s*', r'\1', line)

    # "gap **G-49**" or "gap G-49"
    line = re.sub(r'\bgap\s+\*\*?' + AUDIT_TAG + r'\*\*?,?\s*', '', line)
    line = re.sub(r'\bgap\s+' + AUDIT_TAG + r'\b,?\s*', '', line)

    # mid-line "TAG:" in comment after semicolon (e.g. "text; M6178: more")
    line = re.sub(r';\s+' + AUDIT_TAG + r':\s*', '; ', line)

    # "the A4-L17 parity item" → "the parity item"
    line = re.sub(r'\bthe\s+' + AUDIT_TAG + r'\s+parity item', 'the parity item', line)

    # "Companion of V8-N29 / Q-23." → wipe the whole meaningful content
    # (becomes a pure-audit line, dropped by is_pure_audit_line)
    line = re.sub(r'^(\s*//\s*)Companion of\s+' + AUDIT_TAG + r'.*$', r'\1', line)

    # "Parity audit: TAG — ..." at start
    line = re.sub(r'^(\s*//\s*)Parity audit:\s+' + AUDIT_TAG + r'\s*[—–]\s*', r'\1', line)

    # "# A4-L17 — ..." section header (markdown heading in godoc)
    line = re.sub(r'^(\s*//\s*#\s*)' + AUDIT_TAG + r'\s*[—–]\s*', r'\1', line)

    # "**G-49**" bold audit tags
    line = re.sub(r'\*\*' + AUDIT_TAG + r'\*\*\s*', '', line)

    # Inline trailing comment "(TAG)" in code lines: "// counter (A4-L09)"
    # MUST run before the A-tag mid-sentence rules to avoid leaving "()"
    line = re.sub(r'(\s+//[^/]*\S)\s+\(\s*' + AUDIT_TAG + r'\s*\)\s*$', r'\1', line)

    # A-tag mid-sentence — specific phrase forms FIRST (before general removal)
    line = re.sub(r'\b(A\d+-L\d+)\s+follow-up[.,]?', '', line)
    line = re.sub(r'\b(A\d+-L\d+)\s+fix[.,]?', '', line)
    line = re.sub(r'\b(A\d+-L\d+)\s+parity item[.,]?', '', line)
    # "since A6-L09" / "Mirrors A2-L02 for" — BEFORE general A-tag removal
    line = re.sub(r'\bsince\s+(A\d+-L\d+)\b', 'since', line)
    line = re.sub(r'\bMirrors\s+(A\d+-L\d+)\s+for\s+', 'Mirrors ', line)
    # General A-tag removal
    line = re.sub(r'\b(A\d+-L\d+)(?:\s*\(P\d\))?\.', '.', line)  # "A3-L05 (P2)." → "."
    line = re.sub(r'\b(A\d+-L\d+)(?:\s*\(P\d\))?:', '', line)     # "A5-L14:" → ""
    line = re.sub(r'\)\.?\s+(A\d+-L\d+)\s*\(P\d\)\.', ').', line) # "(py). A3-L05 (P2)." → ")."
    line = re.sub(r'\b(A\d+-L\d+)(?:\s*\(P\d\))?(?=[\s,)])', '', line)  # mid-sentence
    # "A6-L07 production-replay" / "A5-L14 exposed" at start of comment after //
    line = re.sub(r'^(\s*//\s*)' + r'A\d+-L\d+(?:/L\d+|/\d+)*' + r'[:\s]+', r'\1', line)

    # "(api.py, M7013+M7014)" → "(api.py)" — MUST run before general M-tag rules
    line = re.sub(r'(\([^)]*?\.py[^)]*?),\s*M\d+(?:[+/–—]M\d+)*(\))', r'\1\2', line)
    # "(support/__init__.py:540, M7045 adjacent)." → "(support/__init__.py:540)."
    line = re.sub(r'(\([^)]*?\.py[^)]*?),\s*M\d+[^)]*(\))', r'\1\2', line)

    # ", M5124" / " M5124" before closing paren or end
    line = re.sub(r',\s*' + MULTI_TAG + r'(?=\s*[)/])', '', line)

    # "M7032–M7034" en-dash range mid-sentence
    line = re.sub(r'\bM\d+[–—]M\d+\b', '', line)

    # M-tag in parens alone: "(M5003)" / "(M7010)" / "(M5055)"
    line = re.sub(r'\s*\(\s*M\d+(?:[+–/-]M\d+)*\s*\)', '', line)
    # "M5055)." — M-tag then closing paren: the paren was part of outer context
    # "(interface_client.py M5050 + state_machine.py:180," — M-tag after py-file ref space
    line = re.sub(r'(\([^)]*?\.py[^)]*?)\s+M\d+(?:\s*\+\s*[^)]+)?(\))', r'\1\2', line)
    # py-ref with "M5050 + state_machine.py:180," — remove M-tag + " + " before next py-ref
    line = re.sub(r'\s+M\d+\s*\+\s*(?=[\w/].*\.py)', ' ', line)
    # "// M5055)." / "// M5130)" — M-tag is entire comment content before closing paren
    # These are continuation lines of multi-line py-refs. Drop the whole line.
    line = re.sub(r'^(\s*//\s*)' + AUDIT_TAG + r'\s*\)\.\s*$', r'\1', line)
    line = re.sub(r'^(\s*//\s*)' + AUDIT_TAG + r'\s*\)\s*$', r'\1', line)
    # "// A5-L08)." — A-tag before closing paren
    line = re.sub(r'^(\s*//\s*)A\d+-L\d+(?:\s*\(P\d\))?\s*\)\.\s*$', r'\1', line)
    line = re.sub(r'^(\s*//\s*)A\d+-L\d+(?:\s*\(P\d\))?\s*\)\s*$', r'\1', line)
    # Standalone M-tag before closing paren: "M5055)" or "M5130)" — last resort
    line = re.sub(r'\bM\d+\)', ')', line)

    # "M\d+ / Task" — remove M\d+ before " / Task"
    line = re.sub(r'\bM\d+\s*/\s*(?=Task\b)', '', line)

    # "/ M4233 wiring" — slash before M-tag at start of comment (orphan slash from removal)
    line = re.sub(r'^(\s*//\s*)/\s*M\d+\b', r'\1', line)
    # "(Task #30 / M1062 E5)" — remove " / TAG [word]" from within parens
    line = re.sub(r'\s*/\s*M\d+(?:\s+[A-Z]\d+)?(?=\s*[;)])', '', line)
    # "(Task #30 / M1062 E5 hot path)" — remove " / TAG word" from within parens (longer)
    line = re.sub(r'\s*/\s*M\d+(?:\s+[A-Z0-9]+)?(?=\s+\w)', '', line)

    # "Corresponds to M3142+M3143 from the parity sprint." — BEFORE M-multi-ref removal
    line = re.sub(r'\bCorresponds to\s+M\d+(?:[+/]M\d+)*\s+from the parity sprint\.?', '', line, flags=re.IGNORECASE)

    # M-multi-ref mid-sentence: "M1030 blend rule", "M1040 implementation" etc.
    # Remove standalone M-tag when followed by a lowercase word
    line = re.sub(r'\bM\d+(?:[+]M\d+)*\s+(?=[a-z_`(])', '', line)

    # "M5027-M5031: text" — hyphen range colon
    line = re.sub(r'\bM\d+-M\d+:\s*', '', line)

    # Remove G-tag references: "(category=light, G-46)" → "(category=light)"
    line = re.sub(r',\s*G-\d+(?=[\s)])', '', line)
    # "G-\d+" standalone in parens at end
    line = re.sub(r'\(\s*G-\d+\s*\)', '', line)

    # Strip "(P2)" leftover annotation after audit-tag removal
    line = re.sub(r'\s+\(P\d\)(?=[,. ]|$)', '', line)

    # Fix double periods ONLY in comment context (not in code like "...")
    # Only collapse ". ." (with space) or ". ." at end, not "..."
    line = re.sub(r'\. \.', '.', line)
    line = re.sub(r'\.\s+$', '.', line)
    # Stray leading " /" after tag removal at start of comment
    line = re.sub(r'^(\s*//\s*)/\s*', r'\1', line)
    # Stray ")." or ") " at very start of comment content (orphan from multi-line py-ref removal)
    line = re.sub(r'^(\s*//\s*)\)\.\s*', r'\1', line)
    line = re.sub(r'^(\s*//\s*)\)\s+(?=\w)', r'\1', line)
    # Double-space cleanup — only in the comment portion (after //)
    def collapse_comment_spaces(m: re.Match) -> str:
        prefix = m.group(1)  # indent + //
        rest = m.group(2)    # comment text
        return prefix + re.sub(r'  +', ' ', rest)
    line = re.sub(r'^(\s*//)(.*)$', collapse_comment_spaces, line)
    # Trailing orphaned punctuation
    line = re.sub(r'\s+[,;]\s*$', '', line)    # trailing , or ;
    line = re.sub(r'\s+[—–]\s*$', '', line)    # trailing em-dash
    # Trailing spaces
    line = re.sub(r'\s+$', '', line)
    line = re.sub(r'\s+\.$', '.', line)

    return line


def should_process_line(line: str, pass2: bool = False) -> bool:
    """Determine if a line should be processed for comment cleaning."""
    stripped = line.lstrip()
    if pass2:
        # Pass 2: process comment lines AND lines with inline trailing comments
        return True  # process all, rules only fire on comment content
    else:
        # Pass 1: only process lines that start with //
        return stripped.startswith('//')


def is_pure_audit_line(original: str, cleaned: str) -> bool:
    """Return True if a line was purely an audit reference (empty after cleaning)."""
    stripped_orig = original.strip()
    stripped_clean = cleaned.strip()
    return stripped_orig.startswith('//') and stripped_clean == '//'


def process_file(path: str, pass2: bool = False) -> tuple[int, int]:
    """Process a single Go file. Returns (lines_changed, lines_dropped)."""
    with open(path, 'r', encoding='utf-8') as f:
        lines = f.readlines()

    new_lines = []
    lines_changed = 0
    lines_dropped = 0
    i = 0
    while i < len(lines):
        raw = lines[i]
        line = raw.rstrip('\n')
        eol = '\n' if raw.endswith('\n') else ''
        stripped = line.lstrip()

        # Both passes: only process comment lines or lines with inline comments
        process = stripped.startswith('//') or ('//' in line) if pass2 else stripped.startswith('//')

        if process:
            clean_fn = clean_comment_line_pass2 if pass2 else clean_comment_line_pass1
            new_line = clean_fn(line)

            if new_line != line:
                lines_changed += 1
                if is_pure_audit_line(line, new_line):
                    # Drop pure-audit comment lines entirely
                    lines_dropped += 1
                    i += 1
                    continue
            new_lines.append(new_line + eol)
        else:
            new_lines.append(raw)
        i += 1

    if lines_changed and not DRY_RUN:
        with open(path, 'w', encoding='utf-8') as f:
            f.writelines(new_lines)

    return lines_changed, lines_dropped


def main() -> None:
    result = subprocess.run(
        [
            'grep', '-rl', '--include=*.go',
            (r'Welle\|Wave\|parity_audit\|G-[0-9]\|M[0-9]\{3,\}\|L-[0-9]'
             r'\|A[0-9]-L\|W[0-9]-[A-Z]\|V[0-9]-N[0-9]\|Phase-[0-9]'
             r'\|migration step\|closes Phase'),
            'internal/', 'pkg/', 'cmd/',
        ],
        capture_output=True, text=True,
        cwd='/Users/markus/Documents/GitHub/openccu-loom',
    )
    all_files = [
        f for f in result.stdout.strip().split('\n')
        if f
        and not f.endswith('_test.go')
        and 'ha_' not in f
    ]

    total_files = 0
    total_changed = 0
    total_dropped = 0

    for rel_path in sorted(all_files):
        abs_path = os.path.join('/Users/markus/Documents/GitHub/openccu-loom', rel_path)
        changed, dropped = process_file(abs_path, pass2=PASS2)
        if changed:
            total_files += 1
            total_changed += changed
            total_dropped += dropped
            print(f"  {'[DRY]' if DRY_RUN else '[OK] '} {rel_path}  ({changed} lines changed, {dropped} dropped)")

    mode = "PASS2" if PASS2 else "PASS1"
    print(f"\n[{mode}] Done: {total_files} files, {total_changed} lines changed, {total_dropped} lines dropped.")


if __name__ == '__main__':
    main()
