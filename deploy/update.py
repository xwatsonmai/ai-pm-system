#!/usr/bin/env python3
"""Prepare a live backup, then briefly restart one data-compatible app instance.

Uses the sqlite3 CLI so the server's Python 3.6 also gets SQLite's online backup
API. Never tar/copy a live database or restore an old database automatically.
"""
import argparse
from contextlib import closing
import datetime
import fcntl
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import signal
import sqlite3
import subprocess
import tarfile
import threading
import time
import urllib.request


def run(args, **kwargs):
    return subprocess.check_output(args, universal_newlines=True, **kwargs).strip()


def digest(path):
    value = hashlib.sha256()
    with open(str(path), "rb") as source:
        for block in iter(lambda: source.read(1024 * 1024), b""):
            value.update(block)
    return value.hexdigest()


def save(path, value):
    with open(str(path), "w") as target:
        os.chmod(str(path), 0o600)
        json.dump(value, target, indent=2)
        target.write("\n")


def online_backup(source, destination):
    if not source.is_file() or destination.exists():
        raise RuntimeError("Backup source missing or destination already exists")
    # Dot commands accept C-style quoted strings. Pass stdin, never a shell.
    commands = '.timeout 5000\n.backup ' + json.dumps(str(destination)) + '\n'
    subprocess.run(["sqlite3", str(source)], input=commands,
                   universal_newlines=True, check=True)
    os.chmod(str(destination), 0o600)
    # Backup copies the source journal mode too. Make the verified destination
    # standalone so restoring it never requires a sidecar WAL.
    with closing(sqlite3.connect(str(destination))) as db:
        db.execute("pragma journal_mode=delete")
        if db.execute("pragma quick_check").fetchone()[0] != "ok":
            raise RuntimeError("Backup database integrity check failed")
    # The helper commonly runs as root, while the app runs as UID 1000. Keep
    # the source ownership so restoring a 0600 snapshot remains readable.
    owner = source.stat()
    if (destination.stat().st_uid, destination.stat().st_gid) != (owner.st_uid, owner.st_gid):
        os.chown(str(destination), owner.st_uid, owner.st_gid)


def idle(root):
    with closing(sqlite3.connect("file:" + str(root / "shared/data/ai-pm.db") + "?mode=ro", uri=True)) as db:
        for table in ("documents", "requirement_reviews", "reply_jobs"):
            query = "select payload from documents where kind='run'" if table == "documents" else "select payload from " + table
            if any(json.loads(row[0]).get("status") in ("running", "queued", "pending")
                   for row in db.execute(query)):
                raise RuntimeError("Active Agent work; retry after it finishes")
    label = hashlib.sha256(str(root / "shared/data").encode()).hexdigest()[:24]
    if run(["docker", "ps", "-q", "--filter", "label=ai-pm.runner=" + label]):
        raise RuntimeError("Active sandbox worker; retry after it finishes")


def healthy(url):
    try:
        with urllib.request.urlopen(url, timeout=0.5) as response:
            return response.status == 200 and json.loads(response.read()).get("status") == "ok"
    except Exception:
        return False


class Probe:
    def __init__(self, url):
        self.url, self.samples = url, []
        self.stop = threading.Event()
        self.thread = threading.Thread(target=self.loop, daemon=True)

    def loop(self):
        while not self.stop.is_set():
            started = time.monotonic()
            ok = healthy(self.url)
            self.samples.append((started, time.monotonic(), ok))
            self.stop.wait(0.1)

    def finish(self):
        self.stop.set()
        self.thread.join()
        failures = [i for i, (_, _, ok) in enumerate(self.samples) if not ok]
        upper = None
        if failures:
            first, last = failures[0], failures[-1]
            if first > 0 and last + 1 < len(self.samples):
                upper = self.samples[last + 1][1] - self.samples[first - 1][0]
        return {"sample_interval_seconds": 0.1, "failed_samples": len(failures),
                "unavailable_upper_bound_seconds": round(upper, 3) if upper is not None else None,
                "samples": [{"started": start, "finished": end, "healthy": ok} for start, end, ok in self.samples]}


class Update:
    def __init__(self, root, release, url):
        self.root, self.directory, self.url = root, root / "releases" / release, url
        self.compose_files = [root / "compose.yaml"]
        if (root / "compose.sandbox.yaml").exists():
            self.compose_files.append(root / "compose.sandbox.yaml")

    def compose(self, *args, **kwargs):
        command = ["docker", "compose"]
        for path in self.compose_files:
            command.extend(["-f", str(path)])
        if "env" not in kwargs:
            kwargs["env"] = dict(os.environ)
            kwargs["env"].pop("AI_PM_IMAGE", None)
        return run(command + list(args), cwd=str(self.root), **kwargs)

    def current(self):
        return json.loads(run(["docker", "inspect", "ai-pm"]))[0]

    def image(self, name):
        return json.loads(run(["docker", "image", "inspect", name]))[0]["Id"]

    def config_files(self):
        paths = [self.root / ".env"] + self.compose_files
        runner = Path("/etc/ai-pm-sandbox.env")
        if runner.exists():
            paths.append(runner)
        return paths

    def prepare(self, image):
        self.directory.mkdir(mode=0o700, parents=True, exist_ok=True)
        if (self.directory / "prepared.json").exists() or (self.directory / "backup").exists():
            raise RuntimeError("Use a fresh release name; existing backup is preserved")
        current, target = self.current(), self.image(image)
        if not healthy(self.url):
            raise RuntimeError("Current HTTP health check failed")
        idle(self.root)
        configuration = {str(p): digest(p) for p in self.config_files()}
        environment = dict(os.environ, AI_PM_IMAGE=target)
        self.compose("config", "--quiet", env=environment)
        stage = self.directory / "backup"
        stage.mkdir(mode=0o700)
        # Repos and Pi state are copied while the app is available. SQLite/WAL
        # are excluded and replaced by a verified, consistent online snapshot.
        copy = ["rsync", "-a", "--exclude=/data/ai-pm.db", "--exclude=/data/ai-pm.db-wal",
                "--exclude=/data/ai-pm.db-shm", str(self.root / "shared") + "/", str(stage / "shared") + "/"]
        run(copy)
        idle(self.root)
        online_backup(self.root / "shared/data/ai-pm.db", stage / "shared/data/ai-pm.db")
        snapshot_at = datetime.datetime.now(datetime.timezone.utc).isoformat()
        for path in self.config_files():
            shutil.copy2(str(path), str(stage / path.name))
        if configuration != {str(p): digest(p) for p in self.config_files()}:
            raise RuntimeError("Configuration changed during backup; prepare again")
        before = self.directory / "env.before"
        shutil.copy2(str(self.root / ".env"), str(before))
        lines = before.read_text().splitlines()
        if sum(line.startswith("AI_PM_IMAGE=") for line in lines) != 1:
            raise RuntimeError("Expected exactly one AI_PM_IMAGE in .env")
        next_env = self.directory / "env.next"
        next_env.write_text("\n".join("AI_PM_IMAGE=" + target if line.startswith("AI_PM_IMAGE=") else line for line in lines) + "\n")
        os.chmod(str(next_env), before.stat().st_mode & 0o777)
        archive = self.root / "backups" / ("pre-" + self.directory.name + ".tar.gz")
        archive.parent.mkdir(mode=0o700, exist_ok=True)
        # Exclusive creation protects earlier rollback material.
        with open(str(archive), "xb") as output:
            with tarfile.open(fileobj=output, mode="w:gz") as tar:
                for path in sorted(stage.iterdir()):
                    tar.add(str(path), arcname=path.name)
        with tarfile.open(str(archive), "r:gz") as tar:
            for member in tar:
                if member.isfile():
                    content = tar.extractfile(member)
                    for _ in iter(lambda: content.read(1024 * 1024), b""):
                        pass
            # Read past tar's end markers to verify gzip's CRC and trailer too.
            for _ in iter(lambda: tar.fileobj.read(1024 * 1024), b""):
                pass
        prepared = {"created_at": time.time(), "database_snapshot_at": snapshot_at,
                    "previous_image": current["Image"], "target_image": target,
                    "target_reference": image, "configuration": configuration,
                    "backup": str(archive), "backup_sha256": digest(archive),
                    "env_before_sha256": digest(before), "env_next_sha256": digest(next_env)}
        save(self.directory / "prepared.json", prepared)
        print("Online backup compressed and verified; app is still serving")

    def wait_http(self):
        deadline = time.monotonic() + 60
        while time.monotonic() < deadline:
            if healthy(self.url):
                return
            time.sleep(0.1)
        raise RuntimeError("HTTP health did not recover within 60 seconds")

    def apply(self):
        if (self.directory / "result.json").exists():
            raise RuntimeError("This release has already been applied")
        prepared = json.loads((self.directory / "prepared.json").read_text())
        if not 0 <= time.time() - prepared["created_at"] <= 900:
            raise RuntimeError("Prepared backup expired; prepare a fresh release")
        for path, expected in prepared["configuration"].items():
            if digest(Path(path)) != expected:
                raise RuntimeError("Deployment configuration changed since preparation")
        if self.current()["Image"] != prepared["previous_image"] or self.image(prepared["target_reference"]) != prepared["target_image"]:
            raise RuntimeError("Deployment baseline or target image changed")
        for path, expected in ((Path(prepared["backup"]), prepared["backup_sha256"]),
                               (self.directory / "env.before", prepared["env_before_sha256"]),
                               (self.directory / "env.next", prepared["env_next_sha256"])):
            if digest(path) != expected:
                raise RuntimeError("Prepared artifact changed")
        idle(self.root)
        if not healthy(self.url):
            raise RuntimeError("Current HTTP health check failed")
        self.compose("config", "--quiet", env=dict(os.environ, AI_PM_IMAGE=prepared["target_image"]))
        probe = Probe(self.url)
        probe.thread.start()
        time.sleep(0.3)
        started = time.monotonic()
        result = {"backup": prepared["backup"], "database_snapshot_at": prepared["database_snapshot_at"]}
        try:
            # The only work during interruption: stop, select image, recreate.
            self.compose("stop", "app")
            os.replace(str(self.directory / "env.next"), str(self.root / ".env"))
            self.compose("up", "-d", "--no-deps", "--force-recreate", "--pull", "never", "app")
            self.wait_http()
            if self.current()["Image"] != prepared["target_image"]:
                raise RuntimeError("Unexpected running image")
            result.update(outcome="deployed", image=prepared["target_image"],
                          stop_to_http_seconds=round(time.monotonic() - started, 3))
        except BaseException:
            # Retain all current business data; only undo image selection.
            result["outcome"] = "rollback_failed"
            self.compose("stop", "app")
            shutil.copy2(str(self.directory / "env.before"), str(self.root / ".env"))
            self.compose("up", "-d", "--no-deps", "--force-recreate", "--pull", "never", "app")
            self.wait_http()
            if self.current()["Image"] != prepared["previous_image"]:
                raise RuntimeError("Unexpected rollback image")
            result["outcome"] = "rolled_back"
            raise
        finally:
            time.sleep(0.3)
            result["http_probe"] = probe.finish()
            save(self.directory / "result.json", result)
        print(json.dumps({k: v for k, v in result.items() if k != "http_probe"}))


def terminate(signum, frame):
    raise SystemExit(128 + signum)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("mode", choices=("prepare", "apply"))
    parser.add_argument("--root", default="/data/ai-pm")
    parser.add_argument("--release", required=True)
    parser.add_argument("--image", help="Already built and tested local image, required for prepare")
    parser.add_argument("--health-url", default="http://127.0.0.1:8090/api/health")
    args = parser.parse_args()
    if not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9._-]*", args.release):
        parser.error("Invalid release name")
    if args.mode == "prepare" and not args.image:
        parser.error("prepare requires --image")
    os.umask(0o077)
    signal.signal(signal.SIGTERM, terminate)
    root = Path(args.root).resolve()
    with open(str(root / ".deploy.lock"), "a") as lock:
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        update = Update(root, args.release, args.health_url)
        update.prepare(args.image) if args.mode == "prepare" else update.apply()


if __name__ == "__main__":
    main()
