# SPDX-License-Identifier: Apache-2.0
# Copyright (C) 2026 SukramJ.
"""Close an issue that was not filed through the bug-report form.

The bug-report forms mark every item of the "I confirm" checklist as
``required: true``. GitHub enforces that only in the browser: an issue created
through the REST/GraphQL API (``gh issue create``, an MCP server, an agent) can
carry an arbitrary body, with the checklist shortened, reworded or left
unchecked, and the diagnostics dump and the log dropped along the way.

The required checkbox labels are read from ``.github/ISSUE_TEMPLATE/*.yml`` at
run time, so the check cannot drift away from the templates. Violations:

- ``bypassed``    - the body keeps less than half of the required checklist.
- ``rewritten``   - required checklist labels are missing or reworded.
- ``unchecked``   - required checklist labels are present but not ticked.
- ``no-raw-data`` - no uploaded file; reported only alongside a violation above.

On a violation the issue gets the ``invalid-template`` label and one comment,
and it is closed as not planned unless AUTO_CLOSE is "false" (the
``validate-issue-template`` workflow reads it from the repository variable
AUTO_CLOSE_INVALID_TEMPLATE). Maintainers and issues labelled
``skip-template-check`` are exempt.

Run: GITHUB_TOKEN=... REPO_NAME=owner/repo ISSUE_NUMBER=n python3 script/check_issue_template.py
"""

from __future__ import annotations

import json
import os
import re
import sys
import urllib.error
import urllib.parse
import urllib.request
from collections.abc import Iterable, Sequence
from dataclasses import dataclass
from pathlib import Path
from typing import Any

import yaml

TEMPLATE_DIR = Path(__file__).resolve().parent.parent / ".github" / "ISSUE_TEMPLATE"

# Issue form per comment language.
TEMPLATE_FILES = {"en": "bug_report.yml", "de": "bug_report_de.yml"}

INVALID_TEMPLATE_LABEL = "invalid-template"
INVALID_TEMPLATE_LABEL_COLOR = "b60205"
INVALID_TEMPLATE_LABEL_DESCRIPTION = "Issue was not filed through the issue form, or the form was modified"

# A maintainer adds this label to silence the check on one issue.
SKIP_LABEL = "skip-template-check"

# Maintainers legitimately file issues through the API.
EXEMPT_ASSOCIATIONS = frozenset({"OWNER", "MEMBER", "COLLABORATOR"})

# Marks this check's comment, so a later edit of the issue does not post a second one.
COMMENT_MARKER = "<!-- issue-template-check -->"

# Below this share of required labels found verbatim, the body counts as freely
# composed rather than as an edited form.
MIN_STRUCTURE_RATIO = 0.5

# Required labels that may be missing before the checklist counts as rewritten.
# Zero: measured against every externally filed issue (five, #573-#584), each of
# which carries all four labels of its form, ticked. Raise it only when a label
# is added to the forms and older issues get edited.
MAX_DRIFT_MISSING = 0

NEW_ISSUE_URL = "https://github.com/SukramJ/openccu-loom/issues/new/choose"
DISCUSSIONS_URL = "https://github.com/SukramJ/openccu-loom/discussions"
AI_POLICY_URL = "https://github.com/SukramJ/openccu-loom/blob/main/AI_POLICY.md#6-do-not-bypass-the-issue-form"
DIAGNOSTICS_DOC_URL = "https://sukramj.github.io/openccu-loom/user-guide/#diagnostics-and-recording"

GITHUB_API = "https://api.github.com"

# Files dropped into an issue form field render as links of this shape; images
# land under user-attachments/assets and do not count.
_UPLOADED_FILE_PATTERN = re.compile(r"https://github\.com/user-attachments/files/\d+/")

# A rendered checklist line, e.g. "- [x] I searched for existing, similar issues."
_CHECKBOX_LINE_PATTERN = re.compile(r"^\s*[-*]\s*\[( |x|X)\]\s*(.+?)\s*$")

_MARKDOWN_LINK_PATTERN = re.compile(r"\[([^\]]*)\]\([^)]*\)")
_MARKDOWN_EMPHASIS_PATTERN = re.compile(r"[*`]+")
_WHITESPACE_PATTERN = re.compile(r"\s+")


# --- Template and body inspection --------------------------------------------


def normalize_label(text: str) -> str:
    """Reduce a checklist label to what survives GitHub's rendering, casefolded.

    Links keep their text, emphasis markers go and whitespace collapses, so a
    template label and its rendered line compare equal. Case is ignored: only a
    reworded or omitted label is a violation.
    """
    without_links = _MARKDOWN_LINK_PATTERN.sub(r"\1", text or "")
    without_emphasis = _MARKDOWN_EMPHASIS_PATTERN.sub("", without_links)
    return _WHITESPACE_PATTERN.sub(" ", without_emphasis).strip().casefold()


def load_required_checkbox_labels(template_path: Path) -> list[str]:
    """Return the normalized labels of every ``required: true`` checkbox option."""
    data = yaml.safe_load(template_path.read_text(encoding="utf-8"))
    labels: list[str] = []
    for block in data.get("body", []):
        if not isinstance(block, dict) or block.get("type") != "checkboxes":
            continue
        for option in block.get("attributes", {}).get("options", []):
            if isinstance(option, dict) and option.get("required") is True:
                labels.append(normalize_label(str(option.get("label", ""))))
    return [label for label in labels if label]


def load_templates() -> dict[str, list[str]]:
    """Return the required checkbox labels per comment language."""
    return {lang: load_required_checkbox_labels(TEMPLATE_DIR / name) for lang, name in TEMPLATE_FILES.items()}


def parse_checkbox_lines(issue_body: str) -> dict[str, bool]:
    """Map each normalized checklist label in the body to its ticked state."""
    states: dict[str, bool] = {}
    for line in (issue_body or "").splitlines():
        if (match := _CHECKBOX_LINE_PATTERN.match(line)) is not None:
            states[normalize_label(match.group(2))] = match.group(1).lower() == "x"
    return states


def has_uploaded_file(issue_body: str) -> bool:
    """Return True if the body links at least one file uploaded through GitHub."""
    return _UPLOADED_FILE_PATTERN.search(issue_body or "") is not None


@dataclass(frozen=True)
class TemplateCheck:
    """Result of comparing an issue body against one issue form."""

    language: str
    violations: tuple[str, ...] = ()
    missing_labels: tuple[str, ...] = ()
    unchecked_labels: tuple[str, ...] = ()

    @property
    def ok(self) -> bool:
        return not self.violations


def check_issue_body(issue_body: str, *, required_labels: Sequence[str], language: str) -> TemplateCheck:
    """Compare an issue body against the required checklist of one issue form."""
    states = parse_checkbox_lines(issue_body)
    present = [label for label in required_labels if label in states]
    missing = [label for label in required_labels if label not in states]
    unchecked = [label for label in present if not states[label]]

    violations: list[str] = []
    if required_labels and len(present) < MIN_STRUCTURE_RATIO * len(required_labels):
        violations.append("bypassed")
    elif len(missing) > MAX_DRIFT_MISSING:
        violations.append("rewritten")
    if unchecked:
        violations.append("unchecked")
    if violations and not has_uploaded_file(issue_body):
        violations.append("no-raw-data")

    return TemplateCheck(
        language=language,
        violations=tuple(violations),
        missing_labels=tuple(missing),
        unchecked_labels=tuple(unchecked),
    )


def check_against_forms(issue_body: str, templates: dict[str, list[str]]) -> TemplateCheck:
    """Check the body against the form it matches best.

    The form with more required labels present in the body wins; a tie (a
    freely composed body matches neither) falls back to English.
    """
    states = parse_checkbox_lines(issue_body)
    language = max(templates, key=lambda lang: (sum(label in states for label in templates[lang]), lang == "en"))
    return check_issue_body(issue_body, required_labels=templates[language], language=language)


# --- Comment -----------------------------------------------------------------

_VIOLATION_TEXT = {
    "en": {
        "bypassed": (
            "The issue body does not match the issue form. It looks like it was composed directly "
            "(REST/GraphQL API, `gh issue create`, an MCP server or an agent) instead of being "
            "submitted through the form, which skips the form's required-field validation."
        ),
        "rewritten": "Items of the mandatory checklist are missing or were reworded.",
        "unchecked": "Items of the mandatory checklist are not ticked.",
        "no-raw-data": (
            "No uploaded file is attached. A bug report needs the diagnostics dump (`.json`) **and** "
            "a log file (DEBUG preferred), attached as files — a pasted excerpt is not enough."
        ),
    },
    "de": {
        "bypassed": (
            "Der Issue-Text entspricht nicht dem Formular. Er wurde offenbar direkt verfasst "
            "(REST/GraphQL-API, `gh issue create`, ein MCP-Server oder ein Agent), statt über das "
            "Formular abgeschickt zu werden — dabei wird die Pflichtfeld-Prüfung des Formulars übergangen."
        ),
        "rewritten": "Punkte der Pflicht-Checkliste fehlen oder wurden umformuliert.",
        "unchecked": "Punkte der Pflicht-Checkliste sind nicht angehakt.",
        "no-raw-data": (
            "Es ist keine hochgeladene Datei angehängt. Ein Fehlerbericht braucht den Diagnose-Dump "
            "(`.json`) **und** eine Protokolldatei (am besten DEBUG) als Dateianhang — ein eingefügter "
            "Auszug reicht nicht."
        ),
    },
}


def format_comment(check: TemplateCheck, *, auto_closed: bool) -> str:
    """Format the comment posted on a violation, in the language of the matched form."""
    de = check.language == "de"
    texts = _VIOLATION_TEXT["de" if de else "en"]
    parts = [COMMENT_MARKER]

    if auto_closed and de:
        parts.append(
            "## 🔒 Geschlossen: Issue-Formular nicht verwendet\n\n"
            "Dieses Issue wurde automatisch geschlossen, weil es nicht über das Issue-Formular "
            "angelegt wurde. Das Formular stellt sicher, dass jeder Bericht die Angaben enthält, "
            "ohne die sich ein Fehler nicht nachvollziehen lässt: Versionen, der Diagnose-Dump "
            "(`.json`) und eine Protokolldatei.\n\nFestgestellt:"
        )
    elif auto_closed:
        parts.append(
            "## 🔒 Closed: issue form not used\n\n"
            "This issue was closed automatically because it was not filed through the issue form. "
            "The form makes sure every report carries the information a bug cannot be traced "
            "without: versions, the diagnostics dump (`.json`) and a log file.\n\nFound:"
        )
    elif de:
        parts.append("## ⚠️ Issue-Formular nicht verwendet\n\nDieses Issue kann so nicht bearbeitet werden:")
    else:
        parts.append("## ⚠️ Issue form not used\n\nThis issue cannot be processed as filed:")

    parts.append("\n".join(f"- ❌ {texts[violation]}" for violation in check.violations))

    if check.unchecked_labels:
        header = "Nicht angehakt:" if de else "Not ticked:"
        parts.append(header + "\n\n" + "\n".join(f"- `{label}`" for label in check.unchecked_labels))

    if auto_closed and de:
        parts.append(
            f"**So geht es weiter:** Bitte ein **neues** Issue über das [Formular]({NEW_ISSUE_URL}) im "
            f"Browser anlegen, mit Diagnose-Dump und Protokolldatei als Dateianhang "
            f"([wie man sie erhält]({DIAGNOSTICS_DOC_URL})). Dieses Issue zu bearbeiten öffnet es "
            f"nicht wieder."
        )
    elif auto_closed:
        parts.append(
            f"**Next step:** please open a **new** issue through the [issue form]({NEW_ISSUE_URL}) in "
            f"the browser, with the diagnostics dump and the log file attached as files "
            f"([how to obtain them]({DIAGNOSTICS_DOC_URL})). Editing this issue does not reopen it."
        )
    elif de:
        parts.append(
            f"**So geht es weiter:** Bitte das [Formular]({NEW_ISSUE_URL}) im Browser ausfüllen und "
            f"absenden — mit Diagnose-Dump und Protokolldatei als Dateianhang "
            f"([wie man sie erhält]({DIAGNOSTICS_DOC_URL}))."
        )
    else:
        parts.append(
            f"**Next step:** please fill in the [issue form]({NEW_ISSUE_URL}) in the browser and submit "
            f"it there — with the diagnostics dump and the log file attached as files "
            f"([how to obtain them]({DIAGNOSTICS_DOC_URL}))."
        )

    if de:
        parts.append(
            f"**Falls dieser Bericht von einem KI-Werkzeug erstellt wurde:** [AI_POLICY.md]({AI_POLICY_URL}) "
            f"ist verbindlich: Das Formular darf nicht umgangen oder umformuliert werden, und das Issue "
            f"braucht die Rohdaten selbst als Anhang, keine von einem KI-Werkzeug erzeugte "
            f"Zusammenfassung oder Diagnose."
        )
    else:
        parts.append(
            f"**If this report was produced by an AI tool:** [AI_POLICY.md]({AI_POLICY_URL}) is "
            f"binding: the form must not be bypassed or reworded, and the issue needs the raw files "
            f"themselves, not a summary or diagnosis generated by an AI tool."
        )

    if auto_closed:
        parts.append(
            f"_Fragen und Ideen gehören in die [Diskussionen]({DISCUSSIONS_URL})._"
            if de
            else f"_Questions and ideas belong in the [Discussions]({DISCUSSIONS_URL})._"
        )
    else:
        parts.append(
            "_Bitte das Issue entsprechend ergänzen; es bleibt bis dahin offen._"
            if de
            else "_Please amend the issue accordingly; it stays open until then._"
        )

    return "\n\n".join(parts) + "\n"


def is_exempt(*, author_association: str, labels: Iterable[str]) -> bool:
    """Return True when the check must not act on this issue."""
    return author_association.upper() in EXEMPT_ASSOCIATIONS or SKIP_LABEL in set(labels)


# --- GitHub side effects -----------------------------------------------------


class GitHub:
    """The handful of REST calls the check makes, against one repository."""

    def __init__(self, token: str, repo: str) -> None:
        self._token = token
        self._repo = repo

    def request(self, method: str, path: str, payload: dict[str, Any] | None = None) -> Any:
        data = None if payload is None else json.dumps(payload).encode()
        req = urllib.request.Request(
            f"{GITHUB_API}/repos/{self._repo}{path}",
            data=data,
            method=method,
            headers={
                "Accept": "application/vnd.github+json",
                "Authorization": f"Bearer {self._token}",
                "X-GitHub-Api-Version": "2022-11-28",
            },
        )
        with urllib.request.urlopen(req, timeout=30) as resp:
            body = resp.read()
        return json.loads(body) if body else None

    def comment_bodies(self, number: int) -> list[str]:
        bodies: list[str] = []
        page = 1
        while batch := self.request("GET", f"/issues/{number}/comments?per_page=100&page={page}"):
            bodies.extend(comment.get("body") or "" for comment in batch)
            page += 1
        return bodies

    def ensure_label(self) -> None:
        try:
            self.request("GET", f"/labels/{urllib.parse.quote(INVALID_TEMPLATE_LABEL)}")
        except urllib.error.HTTPError as err:
            if err.code != 404:
                raise
            self.request(
                "POST",
                "/labels",
                {
                    "name": INVALID_TEMPLATE_LABEL,
                    "color": INVALID_TEMPLATE_LABEL_COLOR,
                    "description": INVALID_TEMPLATE_LABEL_DESCRIPTION,
                },
            )


def main() -> int:
    token = os.environ.get("GITHUB_TOKEN", "")
    repo = os.environ.get("REPO_NAME", "")
    number = int(os.environ.get("ISSUE_NUMBER") or "0")
    auto_close = (os.environ.get("AUTO_CLOSE") or "true").strip().casefold() != "false"
    if not (token and repo and number):
        print("error: GITHUB_TOKEN, REPO_NAME and ISSUE_NUMBER are required", file=sys.stderr)
        return 1

    gh = GitHub(token, repo)
    issue = gh.request("GET", f"/issues/{number}")
    labels = {label["name"] for label in issue.get("labels", [])}
    if is_exempt(author_association=issue.get("author_association", ""), labels=labels):
        print(f"#{number}: exempt (association={issue.get('author_association')!r})")
        return 0

    templates = load_templates()
    check = check_against_forms(issue.get("body") or "", templates)
    print(f"#{number}: language={check.language} violations={check.violations}")

    if check.ok:
        if INVALID_TEMPLATE_LABEL in labels:
            gh.request("DELETE", f"/issues/{number}/labels/{urllib.parse.quote(INVALID_TEMPLATE_LABEL)}")
        return 0

    if INVALID_TEMPLATE_LABEL not in labels:
        gh.ensure_label()
        gh.request("POST", f"/issues/{number}/labels", {"labels": [INVALID_TEMPLATE_LABEL]})

    # The marker gates the close too: an issue a maintainer reopened after the
    # first comment is not closed again by a later edit.
    if any(COMMENT_MARKER in body for body in gh.comment_bodies(number)):
        print(f"#{number}: already commented")
        return 0

    closing = auto_close and issue.get("state") == "open"
    gh.request("POST", f"/issues/{number}/comments", {"body": format_comment(check, auto_closed=closing)})
    if closing:
        gh.request("PATCH", f"/issues/{number}", {"state": "closed", "state_reason": "not_planned"})
        print(f"#{number}: closed as not planned")
    return 0


if __name__ == "__main__":
    sys.exit(main())
