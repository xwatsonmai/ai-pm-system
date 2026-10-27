import importlib.util
import json
import os
from pathlib import Path
import sqlite3
import tarfile
import tempfile
import unittest
from unittest.mock import patch
from types import SimpleNamespace

spec = importlib.util.spec_from_file_location("update", str(Path(__file__).with_name("update.py")))
update = importlib.util.module_from_spec(spec)
spec.loader.exec_module(update)


class UpdateTest(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory(prefix="ai-pm update ")
        self.root = Path(self.temporary.name)
        (self.root / "shared/data/pi").mkdir(parents=True)
        (self.root / "shared/data/pi/session.jsonl").write_text("saved session\n")
        (self.root / "shared/data/repository.key").write_bytes(b"test-key")
        (self.root / "shared/auth.json").write_text('{"test": true}')
        (self.root / ".env").write_text("AI_PM_IMAGE=old-tag\nAI_PM_BIND_IP=127.0.0.1\n")
        (self.root / "compose.yaml").write_text("services: {}\n")
        self.db = sqlite3.connect(str(self.root / "shared/data/ai-pm.db"))
        self.db.executescript("pragma journal_mode=wal; create table records(value text); insert into records values('committed in WAL');")

    def tearDown(self):
        self.db.close()
        self.temporary.cleanup()

    def test_online_snapshot_keeps_wal_commits_and_live_writer(self):
        snapshot = self.root / "snapshot.db"
        source = self.root / "shared/data/ai-pm.db"
        if os.geteuid() == 0:
            os.chown(str(source), 1000, 1000)
        update.online_backup(self.root / "shared/data/ai-pm.db", snapshot)
        self.db.execute("insert into records values('later write')")
        self.db.commit()
        with sqlite3.connect(str(snapshot)) as db:
            self.assertEqual(db.execute("select value from records").fetchall(), [("committed in WAL",)])
            self.assertEqual(db.execute("pragma quick_check").fetchone()[0], "ok")
        self.assertEqual(self.db.execute("select count(*) from records").fetchone()[0], 2)
        self.assertEqual(snapshot.stat().st_mode & 0o777, 0o600)
        self.assertEqual((snapshot.stat().st_uid, snapshot.stat().st_gid),
                         (source.stat().st_uid, source.stat().st_gid))
        with self.assertRaises(RuntimeError):
            update.online_backup(self.root / "shared/data/ai-pm.db", snapshot)
        with self.assertRaises(RuntimeError):
            update.online_backup(self.root / "missing.db", self.root / "other.db")
        self.assertFalse((self.root / "missing.db").exists())

    def prepared(self):
        updater = update.Update(self.root, "test-release", "http://unused/api/health")
        self.calls = []
        self.running = "old-id"

        def compose(*args, **kwargs):
            self.calls.append(args)
            if args[0] == "up":
                env = (self.root / ".env").read_text()
                self.running = "new-id" if "new-id" in env else "old-id"

        updater.compose = compose
        updater.current = lambda: {"Image": self.running}
        updater.image = lambda name: "new-id"
        updater.wait_http = lambda: None
        with patch.object(update, "idle"), patch.object(update, "healthy", return_value=True):
            updater.prepare("new-tag")
        return updater

    def test_archive_is_ready_before_stop_and_retains_all_rollback_data(self):
        updater = self.prepared()
        self.assertFalse(any(call[0] == "stop" for call in self.calls))
        archive = self.root / "backups/pre-test-release.tar.gz"
        with tarfile.open(str(archive)) as tar:
            self.assertNotIn("shared/data/ai-pm.db-wal", tar.getnames())
            for name in ("shared/data/repository.key", "shared/auth.json", "shared/data/pi/session.jsonl", ".env", "compose.yaml"):
                self.assertIn(name, tar.getnames())
        # All expensive backup work has finished. Apply cannot run rsync/tar or
        # SQLite backup, and must retain writes made after the snapshot.
        self.db.execute("insert into records values('new business data')")
        self.db.commit()
        with patch.object(update, "idle"), patch.object(update, "healthy", return_value=True), patch.object(update, "online_backup", side_effect=AssertionError("late backup")):
            updater.apply()
        self.assertEqual(self.running, "new-id")
        self.assertEqual(self.db.execute("select count(*) from records").fetchone()[0], 2)
        self.assertEqual(json.loads((updater.directory / "result.json").read_text())["outcome"], "deployed")

    def test_start_failure_rolls_back_image_without_restoring_database(self):
        updater = self.prepared()
        self.db.execute("insert into records values('must survive failed release')")
        self.db.commit()
        checks = iter([RuntimeError("new image not ready"), None])

        def wait():
            error = next(checks)
            if error:
                raise error

        updater.wait_http = wait
        with patch.object(update, "idle"), patch.object(update, "healthy", return_value=True):
            with self.assertRaisesRegex(RuntimeError, "new image not ready"):
                updater.apply()
        self.assertEqual(self.running, "old-id")
        self.assertIn("AI_PM_IMAGE=old-tag", (self.root / ".env").read_text())
        self.assertEqual(self.db.execute("select count(*) from records").fetchone()[0], 2)
        self.assertEqual(json.loads((updater.directory / "result.json").read_text())["outcome"], "rolled_back")

    def test_changed_configuration_blocks_before_stop(self):
        updater = self.prepared()
        (self.root / ".env").write_text("AI_PM_IMAGE=someone-elses-release\n")
        with self.assertRaisesRegex(RuntimeError, "configuration changed"):
            updater.apply()
        self.assertFalse(any(call[0] == "stop" for call in self.calls))

    def test_probe_bound_includes_time_to_receive_recovery_response(self):
        probe = update.Probe("http://unused")
        probe.thread = SimpleNamespace(join=lambda: None)
        probe.samples = [(1.0, 1.01, True), (1.1, 1.11, False), (1.2, 1.3, True)]
        report = probe.finish()
        self.assertEqual(report["unavailable_upper_bound_seconds"], 0.3)
        self.assertEqual(report["failed_samples"], 1)


if __name__ == "__main__":
    unittest.main()
