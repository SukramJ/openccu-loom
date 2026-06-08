"""MkDocs hook: hide the language switcher on pages without a real translation.

With ``fallback_to_default: true`` the i18n plugin builds every page under
``/de/`` even when no ``.de.md`` file exists (serving the English source as
the fallback).  The Material theme would then show a language selector on
*all* pages.  This hook removes the selector — and the non-English hreflang
alternate links — on pages that have no actual translation, so the site
reads as English-only until German pages are added.
"""

from __future__ import annotations

from pathlib import Path
import re

# Cache: set of .de.md source files that exist in docs/.
_de_sources: set[str] | None = None


def _get_de_sources(docs_dir: str) -> set[str]:
    """Scan the docs dir for .de.md files (cached across page invocations)."""
    global _de_sources  # noqa: PLW0603 - module-level cache is intentional
    if _de_sources is None:
        docs_path = Path(docs_dir)
        _de_sources = {str(p.relative_to(docs_path)) for p in docs_path.rglob("*.de.md")}
    return _de_sources


def _has_translation(src_path: str, docs_dir: str) -> bool:
    """Return True when a real .de.md translation exists for this page."""
    de_sources = _get_de_sources(docs_dir)

    if src_path.endswith(".de.md"):
        # This IS a .de.md page — a translation exists by definition.
        return True

    # English page: check whether a .de.md sibling exists.
    return src_path.removesuffix(".md") + ".de.md" in de_sources


# Material theme language selector (minified HTML, attributes without quotes).
_LANG_SELECTOR_RE = re.compile(
    r"<div class=md-header__option>\s*<div class=md-select>.*?</div>\s*</div>\s*</div>",
    re.DOTALL,
)

# hreflang alternate links for non-English languages.
_HREFLANG_RE = re.compile(r"<link rel=alternate href=[^ >]+ hreflang=(?!en)[a-z]{2}>")


def on_post_page(output: str, page, config) -> str:
    """Strip the language selector from pages without a real translation."""
    src_path = page.file.src_path

    if not _has_translation(src_path, config.docs_dir):
        output = _LANG_SELECTOR_RE.sub("", output)
        output = _HREFLANG_RE.sub("", output)

    return output
