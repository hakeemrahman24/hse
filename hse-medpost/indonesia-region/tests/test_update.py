import json
import os
import sqlite3
import sys
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
sys.path.insert(0, str(ROOT))
import update  # noqa: E402

SAMPLE = """/*
note     : Data Kode Wilayah sesuai Kepmendagri No 300.2.2-2138 Tahun 2025
*/
DROP TABLE IF EXISTS wilayah;
CREATE TABLE IF NOT EXISTS wilayah (kode varchar(13) NOT NULL, nama varchar(100) NOT NULL, PRIMARY KEY (kode));
INSERT INTO wilayah (kode, nama)
VALUES
('11','Aceh'),
('11.01','Kab. Aceh Selatan'),
('11.01.01','Bakongan'),
('11.01.01.2001','Keude Bakongan'),
('11.01.02','Kluet Utara'),
('11.01.02.2018','Pasi Kuala Ba''u');
"""


def rows(*pairs):
    return list(pairs)


GOOD = rows(
    ("11", "Aceh"),
    ("11.01", "Kab. Aceh Selatan"),
    ("11.01.01", "Bakongan"),
    ("11.01.01.2001", "Keude Bakongan"),
)


class ParseTests(unittest.TestCase):
    def test_parses_every_row_and_unescapes_apostrophes(self):
        parsed = update.parse_wilayah(SAMPLE)
        self.assertEqual(len(parsed), 6)
        self.assertEqual(parsed[0], ("11", "Aceh"))
        self.assertEqual(parsed[-1], ("11.01.02.2018", "Pasi Kuala Ba'u"))

    def test_a_file_with_no_rows_is_refused(self):
        with self.assertRaises(update.ValidationError):
            update.parse_wilayah("/* nothing here */")

    def test_extracts_the_decree_from_the_header(self):
        self.assertEqual(update.extract_decree(SAMPLE), "Kepmendagri No 300.2.2-2138 Tahun 2025")
        self.assertIsNone(update.extract_decree("no header"))

    def test_level_and_parent_come_from_the_code(self):
        self.assertEqual(update.level_of("11"), 1)
        self.assertEqual(update.level_of("11.01"), 2)
        self.assertEqual(update.level_of("11.01.02"), 3)
        self.assertEqual(update.level_of("11.01.02.2018"), 4)
        self.assertIsNone(update.parent_of("11"))
        self.assertEqual(update.parent_of("11.01.02.2018"), "11.01.02")


class ValidateTests(unittest.TestCase):
    def check(self, data, **kw):
        update.validate(data, min_total=1, **kw)

    def test_a_clean_set_passes(self):
        self.check(GOOD)

    def test_too_few_rows_is_refused_by_default(self):
        with self.assertRaises(update.ValidationError):
            update.validate(GOOD)  # the real minimum is tens of thousands

    def test_rejects_a_duplicate_code(self):
        with self.assertRaisesRegex(update.ValidationError, "duplicate"):
            self.check(GOOD + [("11.01", "Again")])

    def test_rejects_a_malformed_code(self):
        with self.assertRaisesRegex(update.ValidationError, "code"):
            self.check(GOOD + [("1.2", "Bad")])

    def test_rejects_a_row_whose_parent_is_missing(self):
        with self.assertRaisesRegex(update.ValidationError, "parent"):
            self.check(GOOD + [("12.05.03.2001", "Orphan")])

    def test_rejects_unsafe_or_empty_names(self):
        for bad in ["", "   ", "back\\slash", "new\nline", "x" * 101]:
            with self.subTest(name=bad):
                with self.assertRaises(update.ValidationError):
                    self.check(GOOD + [("11.01.03", bad)])

    def test_a_big_drop_against_the_previous_snapshot_needs_force(self):
        previous = [(f"{i:02d}", f"P{i}") for i in range(1, 51)]
        shrunk = previous[:40]
        with self.assertRaisesRegex(update.ValidationError, "dropped"):
            self.check(shrunk, previous=previous)
        self.check(shrunk, previous=previous, allow_shrink=True)

    def test_a_small_change_against_the_previous_snapshot_passes(self):
        previous = [(f"{i:02d}", f"P{i}") for i in range(1, 51)]
        self.check(previous[:49], previous=previous)


class DiffTests(unittest.TestCase):
    def test_reports_added_removed_and_renamed(self):
        old = [("11", "Aceh"), ("12", "Sumatera Utara"), ("13", "Sumatera Barat")]
        new = [("11", "Aceh"), ("12", "Sumatra Utara"), ("14", "Riau")]
        d = update.diff(old, new)
        self.assertEqual(d.added, [("14", "Riau")])
        self.assertEqual(d.removed, [("13", "Sumatera Barat")])
        self.assertEqual(d.renamed, [("12", "Sumatera Utara", "Sumatra Utara")])

    def test_identical_sets_have_no_changes(self):
        self.assertFalse(update.diff(GOOD, GOOD).changed)


class OutputTests(unittest.TestCase):
    def test_tsv_round_trips_including_apostrophes(self):
        data = GOOD + [("11.01.02.2018", "Pasi Kuala Ba'u")]
        self.assertEqual(update.read_tsv(update.to_tsv(data)), sorted(data))

    def test_sql_is_batched_sorted_and_escaped(self):
        data = [("11", "Aceh"), ("11.01", "Kab. Aceh Selatan"), ("11.01.01", "Ba'u")]
        sql = update.to_sql(data, batch=2)
        self.assertEqual(sql.count("INSERT INTO Region"), 2)
        self.assertIn("'Ba''u'", sql)
        self.assertLess(sql.index("'11'"), sql.index("'11.01.01'"))
        self.assertIn("('11.01.01','Ba''u',3,'11.01')", sql)
        self.assertIn("('11','Aceh',1,NULL)", sql)

    def test_a_hostile_name_stays_inert_when_loaded(self):
        hostile = "x'); DROP TABLE Region; --"
        data = GOOD + [("11.01.02", hostile)]
        con = sqlite3.connect(":memory:")
        con.executescript((ROOT / "schema" / "region.sqlite.sql").read_text())
        con.executescript(update.to_sql(data))
        name = con.execute("SELECT Name FROM Region WHERE Code = '11.01.02'").fetchone()[0]
        self.assertEqual(name, hostile)
        self.assertEqual(con.execute("SELECT COUNT(*) FROM Region").fetchone()[0], len(data))


class SnapshotTests(unittest.TestCase):
    def meta(self):
        return {"source": "test", "ref": "abc123", "decree": "Kepmendagri No 1 Tahun 2025", "source_sha256": "00"}

    def test_writes_three_files_with_counts(self):
        with tempfile.TemporaryDirectory() as tmp:
            update.write_snapshot(Path(tmp), GOOD, self.meta())
            self.assertTrue((Path(tmp) / "region.tsv").exists())
            self.assertTrue((Path(tmp) / "region-data.sql").exists())
            version = json.loads((Path(tmp) / "VERSION.json").read_text())
            self.assertEqual(version["total"], 4)
            self.assertEqual(version["levels"], {"province": 1, "regency": 1, "district": 1, "village": 1})
            self.assertEqual(version["ref"], "abc123")
            self.assertRegex(version["fetched_at"], r"^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}$")

    def test_reads_back_the_previous_snapshot(self):
        with tempfile.TemporaryDirectory() as tmp:
            self.assertIsNone(update.read_snapshot(Path(tmp)))
            update.write_snapshot(Path(tmp), GOOD, self.meta())
            self.assertEqual(update.read_snapshot(Path(tmp)), sorted(GOOD))


class MainTests(unittest.TestCase):
    def run_main(self, tmp, *args, text=SAMPLE):
        calls = []

        def fake_fetch(url):
            calls.append(url)
            return text

        code = update.main(
            ["--out", str(tmp), "--min-total", "1", *args],
            fetch=fake_fetch,
            latest_ref=lambda: "deadbeef" * 5,
        )
        return code, calls

    def test_downloads_from_the_pinned_commit_and_writes_the_snapshot(self):
        with tempfile.TemporaryDirectory() as tmp:
            code, calls = self.run_main(tmp)
            self.assertEqual(code, 0)
            self.assertTrue(all("deadbeef" * 5 in c for c in calls), calls)
            version = json.loads((Path(tmp) / "VERSION.json").read_text())
            self.assertEqual(version["total"], 6)
            self.assertEqual(version["decree"], "Kepmendagri No 300.2.2-2138 Tahun 2025")

    def test_dry_run_writes_nothing(self):
        with tempfile.TemporaryDirectory() as tmp:
            code, _ = self.run_main(tmp, "--dry-run")
            self.assertEqual(code, 0)
            self.assertEqual(os.listdir(tmp), [])

    def test_bad_data_changes_nothing_and_fails(self):
        with tempfile.TemporaryDirectory() as tmp:
            self.run_main(tmp)
            before = (Path(tmp) / "region.tsv").read_text()
            bad = SAMPLE.replace("Pasi Kuala Ba''u');", "Pasi Kuala Ba''u'),\n('99.99.99.9999','Orphan');")
            code, _ = self.run_main(tmp, text=bad)
            self.assertNotEqual(code, 0)
            self.assertEqual((Path(tmp) / "region.tsv").read_text(), before)

    def test_reads_a_local_file_instead_of_the_network(self):
        with tempfile.TemporaryDirectory() as tmp:
            src = Path(tmp) / "wilayah.sql"
            src.write_text(SAMPLE)
            out = Path(tmp) / "out"
            code = update.main(["--out", str(out), "--min-total", "1", "--from-file", str(src)])
            self.assertEqual(code, 0)
            self.assertEqual(json.loads((out / "VERSION.json").read_text())["total"], 6)


if __name__ == "__main__":
    unittest.main()
