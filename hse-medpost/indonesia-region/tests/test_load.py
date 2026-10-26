import sqlite3
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
sys.path.insert(0, str(ROOT))
import update  # noqa: E402

LOAD = ROOT / "load.sh"

DATA = [
    ("11", "Aceh"),
    ("11.01", "Kab. Aceh Selatan"),
    ("11.01.01", "Bakongan"),
    ("11.01.01.2001", "Keude Bakongan"),
]


def load(*args: str) -> subprocess.CompletedProcess:
    return subprocess.run(["bash", str(LOAD), *args], capture_output=True, text=True)


def count(db: Path) -> int:
    con = sqlite3.connect(db)
    try:
        return con.execute("SELECT COUNT(*) FROM Region").fetchone()[0]
    finally:
        con.close()


class LoadSqliteTests(unittest.TestCase):
    def setUp(self) -> None:
        self.tmp = tempfile.TemporaryDirectory()
        self.dir = Path(self.tmp.name)
        self.db = self.dir / "t.db"
        self.data = self.dir / "region-data.sql"
        self.data.write_text(update.to_sql(DATA))

    def tearDown(self) -> None:
        self.tmp.cleanup()

    def test_creates_the_table_and_loads_every_row(self):
        result = load("--sqlite", str(self.db), "--data", str(self.data))
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(count(self.db), 4)
        con = sqlite3.connect(self.db)
        row = con.execute("SELECT Name, Level, ParentCode FROM Region WHERE Code = '11.01.01.2001'").fetchone()
        con.close()
        self.assertEqual(row, ("Keude Bakongan", 4, "11.01.01"))

    def test_loading_twice_replaces_rather_than_duplicates(self):
        load("--sqlite", str(self.db), "--data", str(self.data))
        result = load("--sqlite", str(self.db), "--data", str(self.data))
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(count(self.db), 4)

    def test_a_newer_file_replaces_the_old_rows(self):
        load("--sqlite", str(self.db), "--data", str(self.data))
        newer = self.dir / "newer.sql"
        newer.write_text(update.to_sql(DATA[:2] + [("12", "Sumatera Utara")]))
        result = load("--sqlite", str(self.db), "--data", str(newer))
        self.assertEqual(result.returncode, 0, result.stderr)
        con = sqlite3.connect(self.db)
        codes = sorted(r[0] for r in con.execute("SELECT Code FROM Region"))
        con.close()
        self.assertEqual(codes, ["11", "11.01", "12"])

    def test_a_failure_halfway_keeps_the_old_rows(self):
        load("--sqlite", str(self.db), "--data", str(self.data))
        broken = self.dir / "broken.sql"
        broken.write_text(update.to_sql([("13", "Sumatera Barat")]) + "\nTHIS IS NOT SQL;\n")
        result = load("--sqlite", str(self.db), "--data", str(broken))
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(count(self.db), 4)
        con = sqlite3.connect(self.db)
        self.assertIsNone(con.execute("SELECT 1 FROM Region WHERE Code = '13'").fetchone())
        con.close()

    def test_a_missing_data_file_fails_without_touching_the_database(self):
        result = load("--sqlite", str(self.db), "--data", str(self.dir / "nope.sql"))
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse(self.db.exists())

    def test_no_target_is_a_usage_error(self):
        result = load("--data", str(self.data), "--env", str(self.dir / "missing.env"))
        self.assertNotEqual(result.returncode, 0)


if __name__ == "__main__":
    unittest.main()
