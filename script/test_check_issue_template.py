# SPDX-License-Identifier: Apache-2.0
# Copyright (C) 2026 SukramJ.
"""Unit tests for check_issue_template.py, against the real issue forms.

Run: python3 script/test_check_issue_template.py (needs PyYAML)
"""

from __future__ import annotations

import sys
import unittest
from pathlib import Path

# script/ is not a package (its entries are standalone tools), so the module
# under test is imported from this file's own directory.
sys.path.insert(0, str(Path(__file__).resolve().parent))

import check_issue_template as checker  # noqa: E402

TEMPLATES = checker.load_templates()
UPLOAD = "https://github.com/user-attachments/files/123456/diagnostics.json"


def form_body(labels: list[str], *, ticked: bool = True, attachment: bool = True) -> str:
    """Render a body the way GitHub renders a submitted form's checklist."""
    mark = "x" if ticked else " "
    lines = ["### I confirm", ""] + [f"- [{mark}] {label}" for label in labels]
    if attachment:
        lines += ["", "### Diagnostics dump", "", f"[diagnostics.json]({UPLOAD})"]
    return "\n".join(lines) + "\n"


class TemplatesTest(unittest.TestCase):
    def test_both_forms_carry_the_required_checklist(self) -> None:
        self.assertEqual(len(TEMPLATES["en"]), 4)
        self.assertEqual(len(TEMPLATES["de"]), 4)
        self.assertIn("i searched for existing, similar issues.", TEMPLATES["en"])
        self.assertIn("ich habe nach bestehenden, gleichartigen issues gesucht.", TEMPLATES["de"])

    def test_link_markup_is_reduced_to_its_text(self) -> None:
        self.assertTrue(any("(see releases)" in label for label in TEMPLATES["en"]))


class CheckTest(unittest.TestCase):
    def test_submitted_form_passes(self) -> None:
        check = checker.check_against_forms(form_body(TEMPLATES["en"]), TEMPLATES)
        self.assertTrue(check.ok, check.violations)
        self.assertEqual(check.language, "en")

    def test_german_form_is_matched_to_the_german_template(self) -> None:
        check = checker.check_against_forms(form_body(TEMPLATES["de"]), TEMPLATES)
        self.assertTrue(check.ok, check.violations)
        self.assertEqual(check.language, "de")

    def test_free_text_is_bypassed_and_falls_back_to_english(self) -> None:
        check = checker.check_against_forms("Loom crashes when I click save.", TEMPLATES)
        self.assertEqual(check.violations, ("bypassed", "no-raw-data"))
        self.assertEqual(check.language, "en")

    def test_one_dropped_label_is_a_rewritten_checklist(self) -> None:
        check = checker.check_against_forms(form_body(TEMPLATES["en"][1:]), TEMPLATES)
        self.assertEqual(check.violations, ("rewritten",))

    def test_unticked_labels_are_reported(self) -> None:
        check = checker.check_against_forms(form_body(TEMPLATES["en"], ticked=False), TEMPLATES)
        self.assertEqual(check.violations, ("unchecked",))
        self.assertEqual(len(check.unchecked_labels), 4)

    def test_missing_upload_alone_is_not_a_violation(self) -> None:
        check = checker.check_against_forms(form_body(TEMPLATES["en"], attachment=False), TEMPLATES)
        self.assertTrue(check.ok, check.violations)

    def test_image_is_not_an_uploaded_file(self) -> None:
        self.assertFalse(checker.has_uploaded_file("![x](https://github.com/user-attachments/assets/abc)"))
        self.assertTrue(checker.has_uploaded_file(UPLOAD))

    def test_maintainers_and_skip_label_are_exempt(self) -> None:
        self.assertTrue(checker.is_exempt(author_association="OWNER", labels=[]))
        self.assertTrue(checker.is_exempt(author_association="NONE", labels=[checker.SKIP_LABEL]))
        self.assertFalse(checker.is_exempt(author_association="NONE", labels=["bug"]))


class CommentTest(unittest.TestCase):
    def test_closed_comment_asks_for_a_new_issue(self) -> None:
        check = checker.check_against_forms("free text", TEMPLATES)
        comment = checker.format_comment(check, auto_closed=True)
        self.assertTrue(comment.startswith(f"{checker.COMMENT_MARKER}\n\n## 🔒 Closed"))
        self.assertIn("does not reopen", comment)
        self.assertIn(checker.NEW_ISSUE_URL, comment)
        self.assertIn(checker.DISCUSSIONS_URL, comment)
        self.assertIn(checker.AI_POLICY_URL, comment)
        self.assertEqual(comment.count("- ❌"), len(check.violations))
        self.assertNotIn("stays open", comment)

    def test_closed_comment_in_german(self) -> None:
        check = checker.check_against_forms(form_body(TEMPLATES["de"][1:]), TEMPLATES)
        comment = checker.format_comment(check, auto_closed=True)
        self.assertIn("automatisch geschlossen", comment)
        self.assertIn("öffnet es nicht wieder", comment)
        self.assertNotIn("bleibt bis dahin offen", comment)

    def test_open_comment_names_unticked_labels(self) -> None:
        check = checker.check_against_forms(form_body(TEMPLATES["en"], ticked=False), TEMPLATES)
        comment = checker.format_comment(check, auto_closed=False)
        self.assertIn("stays open", comment)
        self.assertIn(f"`{TEMPLATES['en'][0]}`", comment)


if __name__ == "__main__":
    unittest.main()
