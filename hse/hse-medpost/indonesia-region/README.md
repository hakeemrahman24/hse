# indonesia-region

The whole of Indonesia's administrative areas (province, regency/city, district,
village: **91,599 rows**) as a `Region` table, for the malaria form's
Regency → District → Village dropdowns.

Source: [cahyadsn/wilayah](https://github.com/cahyadsn/wilayah) (MIT licence),
which follows the Ministry of Home Affairs decree named in `data/VERSION.json`.

| File | What it is |
|---|---|
| `update.py` | Fetches the latest data from GitHub, checks it, writes `data/`. Run on a dev machine. |
| `load.sh` | Loads `data/region-data.sql` into the database. Run where the database is (the Pi). |
| `data/region.tsv` | The snapshot: `code<TAB>name`. Easy to diff between updates. |
| `data/region-data.sql` | Generated `INSERT`s that `load.sh` runs. |
| `data/VERSION.json` | Which GitHub commit, which decree, when, how many rows per level. |
| `schema/region.mariadb.sql`, `schema/region.sqlite.sql` | The `Region` table (production / development). |
| `tests/` | `python3 -I -m unittest discover -s tests` |

## The table

```
Region (Code PK, Name, Level 1-4, ParentCode)
  94 Papua  →  94.01 …  →  94.01.05 Wanggar  →  94.01.05.2001 …
```

A code says its own level and parent. Other tables (such as the malaria screening
record) should **copy the code and the name** instead of pointing a foreign key at
`Region`, so an old record keeps the names it was written with when the
government renames or merges an area.

## Updating from GitHub

```
python3 -I update.py --dry-run   # fetch, check, show what changed, write nothing
python3 -I update.py             # same, then write data/
```

- The download is pinned to the newest commit of `db/wilayah.sql` (looked up through
  the GitHub API), so the file cannot change between asking and fetching. If the API
  is rate limited, use `--ref master`.
- The file is only ever **read as text**. Each row is checked, and our own SQL is
  generated from the checked rows; the downloaded SQL is never run.
- The update is refused (exit code 1, `data/` untouched) if: the file has fewer than
  80,000 rows; a code is malformed or duplicated; a row's parent is missing; a name is
  empty, longer than 100 characters, or has a backslash or control character; or the
  row count dropped more than 5% from the last snapshot (`--force` overrides only
  that last one).
- When nothing changed, nothing is written.

After an update, review the printed `+ - ~` list (added, removed, renamed), then copy
the folder to the Pi and load it.

## Loading

```
bash load.sh --env ../.env            # MariaDB, using the app's own .env
bash load.sh --sqlite /path/to.db     # SQLite
```

Creates the table if needed, then **deletes all rows and inserts the new ones in a
single transaction**; if anything fails the old rows are kept. Running it twice is
harmless. The password is taken from `.env` (never printed, never sourced).

Take a `mysqldump` backup of the database before the first run on production.

The MariaDB path has not been run against a real MariaDB server yet; the SQLite path
is covered by `tests/test_load.py`.

## Copying to the Pi

Copy the folder only. **Never copy `.env`** between machines; `load.sh` reads the
Pi's own.

```
rsync -a --delete indonesia-region/ orangepi@orangepi3b:projects/hse-medpost/indonesia-region/
```
