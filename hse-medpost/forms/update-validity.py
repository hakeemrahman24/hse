#!/usr/bin/env python3
"""Update the validity (expiry) date of every offline HSE form in this folder.

Usage:
    python3 update-validity.py YYYY-MM-DD            # apply
    python3 update-validity.py YYYY-MM-DD --dry-run  # show what would change

A file counts as an "offline form" by content, not by name: it must have
exactly one `var FORM_EXPIRY_DATE="...";` line (the expiry gate every form
starts with). Any other *.html file directly in this folder (a readme
export, a one-off test page, etc.) is left alone and reported as skipped,
instead of being forced to match a naming pattern or aborting the run.

For each offline form found, it:
  1. Sets FORM_EXPIRY_DATE to the new date. The version shown on the page
     ("Form v<YYYYMMDD>") is computed from that value at runtime, so it
     updates with it.
  2. Renames the file so it ends in -v<ddmmyy>-<ddmmyy>.html: the expiry
     date, then the date this script ran. It replaces any existing
     -v<6 or 8 digits>[-<6 digits>] suffix (whatever came before stays untouched,
     so this doesn't require the "-form-" naming convention specifically).
     Running twice on the same day with the same expiry gives the same name,
     so the files are left as they are.
  3. Updates the readme table and the expiry sentence in form_readme.md.

Only files directly in this folder are touched; subfolders such as
graphify-out/ are ignored.
"""

import argparse
import datetime as dt
import re
import sys
from pathlib import Path

FORMS_DIR = Path(__file__).resolve().parent
README = FORMS_DIR / "form_readme.md"
EXPIRY_RE = re.compile(r'var FORM_EXPIRY_DATE="(\d{4}-\d{2}-\d{2})";')
VERSION_SUFFIX_RE = re.compile(r"-v\d{6,8}(?:-\d{6})?\.html$")
README_SUMMARY_RE = re.compile(r"All (?:four )?current forms expire \d{4}-\d{2}-\d{2}\.")


def parse_date(text: str) -> dt.date:
    try:
        return dt.date.fromisoformat(text)
    except ValueError:
        sys.exit(f"error: '{text}' is not a valid YYYY-MM-DD date")


def html_files():
    return sorted(p for p in FORMS_DIR.glob("*.html") if p.is_file())


def base_name(path: Path) -> str:
    """Filename without any existing -v<YYYYMMDD> version suffix or extension."""
    suffix_match = VERSION_SUFFIX_RE.search(path.name)
    return path.name[: suffix_match.start()] if suffix_match else path.stem


def plan_changes(new_date: dt.date):
    """Return (plan, skipped): plan is (path, old_date, new_path) per offline
    form found; skipped is the names of *.html files that aren't one."""
    new_dmy = new_date.strftime("%d%m%y")
    run_date = dt.date.today().strftime("%d%m%y")
    plan = []
    skipped = []
    for path in html_files():
        text = path.read_text(encoding="utf-8")
        found = EXPIRY_RE.findall(text)
        if not found:
            skipped.append(path.name)
            continue
        if len(found) > 1:
            sys.exit(f"error: {path.name} has {len(found)} FORM_EXPIRY_DATE lines, expected 1")
        new_path = path.with_name(f"{base_name(path)}-v{new_dmy}-{run_date}.html")
        if new_path != path and new_path.exists():
            sys.exit(f"error: {new_path.name} already exists, refusing to overwrite")
        plan.append((path, found[0], new_path))
    return plan, skipped


def apply_form(path: Path, new_date: dt.date, new_path: Path) -> None:
    text = path.read_text(encoding="utf-8")
    updated = EXPIRY_RE.sub(f'var FORM_EXPIRY_DATE="{new_date.isoformat()}";', text, count=1)
    new_path.write_text(updated, encoding="utf-8")
    if new_path != path:
        path.unlink()


def update_readme(plan, new_date: dt.date) -> None:
    if not README.exists():
        print("note: form_readme.md not found, skipping readme update")
        return
    text = README.read_text(encoding="utf-8")
    for old_path, old_date, new_path in plan:
        text = text.replace(old_path.name, new_path.name)
        text = text.replace(f"| {old_date} |", f"| {new_date.isoformat()} |")
    # Matches the summary sentence regardless of which date (or the old
    # "four"-count wording) it currently holds, so this keeps working on
    # every future run instead of only the first one.
    text = README_SUMMARY_RE.sub(f"All current forms expire {new_date.isoformat()}.", text)
    README.write_text(text, encoding="utf-8")


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("new_date", help="new expiry date, YYYY-MM-DD")
    parser.add_argument("--dry-run", action="store_true", help="show changes without writing")
    args = parser.parse_args()

    new_date = parse_date(args.new_date)
    if new_date <= dt.date.today():
        print(f"warning: {new_date} is not after today ({dt.date.today()}); forms would show as expired")

    plan, skipped = plan_changes(new_date)
    if not plan:
        sys.exit("error: no offline forms found in this folder (no *.html with FORM_EXPIRY_DATE)")

    for name in skipped:
        print(f"skip       {name} (not an offline form: no FORM_EXPIRY_DATE)")

    for path, old_date, new_path in plan:
        if old_date == new_date.isoformat() and new_path == path:
            print(f"unchanged  {path.name} (already {old_date})")
            continue
        rename = "" if new_path == path else f" -> {new_path.name}"
        print(f"update     {path.name}: {old_date} -> {new_date.isoformat()}{rename}")

    if args.dry_run:
        print("dry run: nothing written")
        return

    for path, _old_date, new_path in plan:
        apply_form(path, new_date, new_path)
    update_readme(plan, new_date)
    print(f"done: {len(plan)} offline form(s) set to {new_date.isoformat()} (version v{new_date:%Y%m%d})")


if __name__ == "__main__":
    main()
