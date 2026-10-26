#!/usr/bin/env python3
"""Update the Indonesia region snapshot from GitHub.

Pulls db/wilayah.sql from https://github.com/cahyadsn/wilayah (MIT licence), checks
it strictly, shows what changed since the last snapshot, and writes:

    data/region.tsv       code<TAB>name, the canonical snapshot (easy to diff)
    data/region-data.sql  INSERT statements for the Region table (what load.sh runs)
    data/VERSION.json     which GitHub commit, which decree, when, how many rows

Run it on a development machine, then copy the folder to the Pi; the Pi only
needs load.sh. Nothing downloaded is ever executed: the file is read as text,
every row is checked, and our own SQL is generated from the checked rows.

    python3 update.py              # fetch the latest commit and write the snapshot
    python3 update.py --dry-run    # fetch and check, show the changes, write nothing
    python3 update.py --ref master # use a branch or commit (also the fallback when
                                   # GitHub's API is rate limited)
"""
from __future__ import annotations

import argparse
import hashlib
import json
import logging
import os
import re
import sys
import tempfile
import urllib.request
from dataclasses import dataclass
from datetime import datetime, timezone
from pathlib import Path
from typing import Callable, Optional, Sequence

REPO = "cahyadsn/wilayah"
SOURCE_PATH = "db/wilayah.sql"
RAW_URL = "https://raw.githubusercontent.com/{repo}/{ref}/{path}"
COMMITS_URL = f"https://api.github.com/repos/{REPO}/commits?path={SOURCE_PATH}&per_page=1"
ALLOWED_HOSTS = ("https://raw.githubusercontent.com/", "https://api.github.com/")

# The whole country is about 91,600 rows. Anything far below that is a broken or
# truncated download, not a smaller Indonesia.
MIN_TOTAL = 80_000
# A new snapshot may lose at most this share of the previous one without --force.
MAX_SHRINK = 0.05
MAX_NAME_LENGTH = 100
MAX_DOWNLOAD_BYTES = 20_000_000
SQL_BATCH = 500
REPORT_LIMIT = 20

LEVEL_NAMES = {1: "province", 2: "regency", 3: "district", 4: "village"}

Row = tuple[str, str]  # (code, name)

_CODE_RE = re.compile(r"^\d{2}(\.\d{2}(\.\d{2}(\.\d{4})?)?)?$")
# One data line of the source INSERT: ('11.01.02.2018','Pasi Kuala Ba''u'),
_ROW_RE = re.compile(r"^\('([0-9.]+)','((?:[^']|'')*)'\)\s*[,;]\s*$")
_DECREE_RE = re.compile(r"Kepmendagri No [\w.\-]+ Tahun \d{4}")

log = logging.getLogger("indonesia-region")


class ValidationError(Exception):
    """The downloaded data is not safe to use."""


@dataclass(frozen=True)
class Diff:
    added: list[Row]
    removed: list[Row]
    renamed: list[tuple[str, str, str]]  # (code, old name, new name)

    @property
    def changed(self) -> bool:
        return bool(self.added or self.removed or self.renamed)


def level_of(code: str) -> int:
    """1 province, 2 regency/city, 3 district, 4 village; the code says which."""
    return code.count(".") + 1


def parent_of(code: str) -> Optional[str]:
    return code.rsplit(".", 1)[0] if "." in code else None


def extract_decree(text: str) -> Optional[str]:
    match = _DECREE_RE.search(text)
    return match.group(0) if match else None


def parse_wilayah(text: str) -> list[Row]:
    """Read the (code, name) rows out of the source file.

    Every line that looks like a data row must parse; one that does not is an
    error, so a format change upstream cannot silently drop rows.
    """
    rows: list[Row] = []
    for number, line in enumerate(text.splitlines(), start=1):
        stripped = line.strip()
        if not stripped.startswith("('"):
            continue
        match = _ROW_RE.match(stripped)
        if not match:
            raise ValidationError(f"line {number} looks like a data row but cannot be read: {stripped[:80]!r}")
        rows.append((match.group(1), match.group(2).replace("''", "'")))
    if not rows:
        raise ValidationError("no data rows found; is this the right file?")
    return rows


def validate(
    rows: Sequence[Row],
    previous: Optional[Sequence[Row]] = None,
    min_total: int = MIN_TOTAL,
    allow_shrink: bool = False,
) -> None:
    """Raise ValidationError unless the rows are safe to load."""
    if len(rows) < min_total:
        raise ValidationError(f"only {len(rows)} rows; at least {min_total} expected")

    problems: list[str] = []
    seen: set[str] = set()
    for code, name in rows:
        if not _CODE_RE.match(code):
            problems.append(f"invalid code {code!r}")
        if code in seen:
            problems.append(f"duplicate code {code!r}")
        seen.add(code)
        if not name.strip():
            problems.append(f"empty name for {code}")
        elif len(name) > MAX_NAME_LENGTH:
            problems.append(f"name of {code} is longer than {MAX_NAME_LENGTH} characters")
        elif "\\" in name or any(ord(c) < 32 or ord(c) == 127 for c in name):
            # A backslash means something different to MariaDB and SQLite, so a
            # name with one is refused instead of escaped.
            problems.append(f"name of {code} has a backslash or control character")
    for code, _ in rows:
        parent = parent_of(code)
        if parent is not None and parent not in seen:
            problems.append(f"{code} has no parent {parent}")

    if previous is not None and not allow_shrink and len(rows) < len(previous) * (1 - MAX_SHRINK):
        problems.append(f"row count dropped from {len(previous)} to {len(rows)}; use --force if that is real")

    if problems:
        more = f" (and {len(problems) - 10} more)" if len(problems) > 10 else ""
        raise ValidationError("; ".join(problems[:10]) + more)


def diff(old: Sequence[Row], new: Sequence[Row]) -> Diff:
    old_map, new_map = dict(old), dict(new)
    added = sorted((c, n) for c, n in new_map.items() if c not in old_map)
    removed = sorted((c, n) for c, n in old_map.items() if c not in new_map)
    renamed = sorted((c, old_map[c], n) for c, n in new_map.items() if c in old_map and old_map[c] != n)
    return Diff(added, removed, renamed)


def to_tsv(rows: Sequence[Row]) -> str:
    return "".join(f"{code}\t{name}\n" for code, name in sorted(rows))


def read_tsv(text: str) -> list[Row]:
    rows: list[Row] = []
    for line in text.splitlines():
        code, _, name = line.partition("\t")
        rows.append((code, name))
    return sorted(rows)


def _sql_text(value: str) -> str:
    return "'" + value.replace("'", "''") + "'"


def to_sql(rows: Sequence[Row], batch: int = SQL_BATCH) -> str:
    """INSERTs for the Region table. Only quotes need escaping: validate() has
    already refused backslashes and control characters, so the same text means
    the same thing to MariaDB and SQLite."""
    ordered = sorted(rows)
    out = ["-- Generated by update.py from data/region.tsv. Do not edit by hand.\n"]
    for start in range(0, len(ordered), batch):
        values = []
        for code, name in ordered[start : start + batch]:
            parent = parent_of(code)
            values.append(
                f"({_sql_text(code)},{_sql_text(name)},{level_of(code)},{_sql_text(parent) if parent else 'NULL'})"
            )
        out.append("INSERT INTO Region (Code, Name, Level, ParentCode) VALUES\n" + ",\n".join(values) + ";\n")
    return "".join(out)


def read_snapshot(out: Path) -> Optional[list[Row]]:
    tsv = out / "region.tsv"
    return read_tsv(tsv.read_text(encoding="utf-8")) if tsv.exists() else None


def _replace_atomically(path: Path, content: str) -> None:
    """Write beside the target, then rename over it, so a crash never leaves a
    half-written file."""
    fd, tmp = tempfile.mkstemp(dir=path.parent, prefix=path.name + ".")
    try:
        with os.fdopen(fd, "w", encoding="utf-8", newline="\n") as handle:
            handle.write(content)
        os.chmod(tmp, 0o644)  # mkstemp makes files private; these are plain data
        os.replace(tmp, path)
    except BaseException:
        if os.path.exists(tmp):
            os.unlink(tmp)
        raise


def write_snapshot(out: Path, rows: Sequence[Row], meta: dict[str, str]) -> None:
    out.mkdir(parents=True, exist_ok=True)
    levels = {name: 0 for name in LEVEL_NAMES.values()}
    for code, _ in rows:
        levels[LEVEL_NAMES[level_of(code)]] += 1
    version = {
        **meta,
        "fetched_at": datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%S"),
        "total": len(rows),
        "levels": levels,
    }
    _replace_atomically(out / "region.tsv", to_tsv(rows))
    _replace_atomically(out / "region-data.sql", to_sql(rows))
    # VERSION last: it is the marker that the other two are complete.
    _replace_atomically(out / "VERSION.json", json.dumps(version, indent=2, ensure_ascii=False) + "\n")


def fetch_text(url: str) -> str:
    if not url.startswith(ALLOWED_HOSTS):
        raise ValueError(f"refusing to fetch {url}")
    request = urllib.request.Request(url, headers={"User-Agent": "indonesia-region-update"})
    with urllib.request.urlopen(request, timeout=60) as response:  # noqa: S310 - host checked above
        body = response.read(MAX_DOWNLOAD_BYTES + 1)
    if len(body) > MAX_DOWNLOAD_BYTES:
        raise ValueError(f"download is larger than {MAX_DOWNLOAD_BYTES} bytes")
    return body.decode("utf-8")


def latest_commit() -> str:
    """The newest commit that touched the source file, so the download is
    pinned: the file cannot change between asking and fetching."""
    commits = json.loads(fetch_text(COMMITS_URL))
    sha = commits[0]["sha"]
    if not re.fullmatch(r"[0-9a-f]{40}", sha):
        raise ValueError(f"unexpected commit id {sha!r}")
    return sha


def _report(change: Diff, previous: Optional[Sequence[Row]], rows: Sequence[Row]) -> None:
    if previous is None:
        log.info("first snapshot: %d rows", len(rows))
        return
    log.info(
        "changes since the last snapshot: %d added, %d removed, %d renamed",
        len(change.added), len(change.removed), len(change.renamed),
    )
    for code, name in change.added[:REPORT_LIMIT]:
        log.info("  + %s  %s", code, name)
    for code, name in change.removed[:REPORT_LIMIT]:
        log.info("  - %s  %s", code, name)
    for code, old, new in change.renamed[:REPORT_LIMIT]:
        log.info("  ~ %s  %s -> %s", code, old, new)


def main(
    argv: Optional[Sequence[str]] = None,
    fetch: Callable[[str], str] = fetch_text,
    latest_ref: Callable[[], str] = latest_commit,
) -> int:
    here = Path(__file__).resolve().parent
    parser = argparse.ArgumentParser(description="Update the Indonesia region snapshot from GitHub.")
    parser.add_argument("--out", type=Path, default=here / "data", help="where the snapshot lives (default: ./data)")
    parser.add_argument("--ref", help="branch or commit to download (default: the newest commit of the source file)")
    parser.add_argument("--from-file", type=Path, help="read this wilayah.sql instead of downloading")
    parser.add_argument("--dry-run", action="store_true", help="check and show changes, write nothing")
    parser.add_argument("--force", action="store_true", help="accept a snapshot that is much smaller than the last")
    parser.add_argument("--min-total", type=int, default=MIN_TOTAL, help=argparse.SUPPRESS)
    args = parser.parse_args(argv)

    try:
        if args.from_file:
            text = args.from_file.read_text(encoding="utf-8")
            ref, source = "local-file", str(args.from_file)
        else:
            ref = args.ref or latest_ref()
            source = RAW_URL.format(repo=REPO, ref=ref, path=SOURCE_PATH)
            text = fetch(source)
    except (OSError, ValueError, KeyError, IndexError) as exc:  # JSONDecodeError is a ValueError
        log.error("could not get the source file: %s", exc)
        log.error("if GitHub's API is rate limited, retry with --ref master")
        return 2

    try:
        rows = sorted(parse_wilayah(text))
        previous = read_snapshot(args.out)
        validate(rows, previous=previous, min_total=args.min_total, allow_shrink=args.force)
    except ValidationError as exc:
        log.error("refusing this data, nothing was changed: %s", exc)
        return 1

    change = diff(previous or [], rows)
    _report(change, previous, rows)
    if args.dry_run:
        log.info("dry run: nothing written")
        return 0
    if previous is not None and not change.changed:
        log.info("already up to date (%d rows); nothing written", len(rows))
        return 0

    meta = {
        "source": source,
        "ref": ref,
        "decree": extract_decree(text) or "unknown",
        "source_sha256": hashlib.sha256(text.encode("utf-8")).hexdigest(),
    }
    write_snapshot(args.out, rows, meta)
    log.info("wrote %s (%d rows, %s)", args.out, len(rows), meta["decree"])
    return 0


if __name__ == "__main__":
    logging.basicConfig(level=logging.INFO, format="%(message)s", stream=sys.stdout)
    sys.exit(main())
