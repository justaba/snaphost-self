#!/usr/bin/env python3
"""Measure a real Node deployment on an installed Linux host with cgroup v2.

Run as root. Supply the operator password in a protected file; output contains
only deployment IDs, measurements and probe results. Both test deployments are
stopped afterwards. This does not change public domain targets.
"""

import argparse
import http.cookiejar
import io
import json
import os
from pathlib import Path
import re
import stat
import subprocess
import tarfile
import threading
import time
import urllib.parse
import urllib.request


def docker(*args):
    return subprocess.check_output(["docker", *args], text=True).strip()


def inspect(name):
    return json.loads(docker("inspect", name))[0]


def cgroup(name):
    pid = inspect(name)["State"]["Pid"]
    line = next(line for line in Path(f"/proc/{pid}/cgroup").read_text().splitlines() if line.startswith("0::"))
    return Path("/sys/fs/cgroup") / line[3:].lstrip("/")


def memory():
    return {key: int(value.split()[0]) for key, value in
            (line.split(":", 1) for line in Path("/proc/meminfo").read_text().splitlines())}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--api-url", default="http://127.0.0.1:8080")
    parser.add_argument("--email", required=True)
    parser.add_argument("--password-file", type=Path, required=True)
    parser.add_argument("--fixture", type=Path, default=Path(__file__).parent / "fixtures/node-app")
    parser.add_argument("--app", default="snaphost-snaphost-1")
    parser.add_argument("--builder", default="snaphost-buildkitd-1")
    parser.add_argument("--caddy", default="snaphost-caddy-1")
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    os.umask(0o077)
    if args.password_file.is_symlink() or not stat.S_ISREG(args.password_file.stat().st_mode):
        raise RuntimeError("password must be in a regular file")
    if args.password_file.stat().st_mode & 0o077:
        raise RuntimeError("password file must not be accessible to group or others")
    url = urllib.parse.urlparse(args.api_url)
    if url.scheme != "https" and not (url.scheme == "http" and url.hostname in ("127.0.0.1", "localhost")):
        raise RuntimeError("API URL must use HTTPS or host loopback")
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}),
                urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))

    def api(path, payload=None, raw=None):
        data = raw if raw is not None else json.dumps(payload).encode() if payload is not None else None
        request = urllib.request.Request(args.api_url.rstrip("/") + path, data=data)
        if data is not None:
            request.add_header("Content-Type", "application/gzip" if raw is not None else "application/json")
        with opener.open(request, timeout=30) as response:
            body = response.read()
            return json.loads(body) if body else None

    api("/api/v1/auth/login", {"email": args.email, "password": args.password_file.read_text().strip()})
    deploys = []

    def deploy(files):
        stream = io.BytesIO()
        with tarfile.open(fileobj=stream, mode="w:gz") as archive:
            for name, content in files.items():
                entry = tarfile.TarInfo(name)
                entry.size, entry.mode = len(content), 0o644
                archive.addfile(entry, io.BytesIO(content))
        uploaded = api("/api/v1/deploys/upload", raw=stream.getvalue())
        created = api("/api/v1/deploys", {"source_type": "archive", "upload_id": uploaded["upload_id"]})
        deploys.append(created["deploy_id"])
        return created["deploy_id"]

    def detail(ident):
        return api("/api/v1/deploys/" + ident)

    def site(container, port, path="/"):
        address = inspect(container)["NetworkSettings"]["Networks"]["snaphost-net"]["IPAddress"]
        return docker("exec", args.app, "curl", "--noproxy", "*", "-fsS", f"http://{address}:{port}{path}")

    paths, samples, sampling_errors, stop = {}, [], [], threading.Event()

    def sample():
        try:
            while not stop.is_set():
                values = memory()
                samples.append({"used_kib": values["MemTotal"] - values["MemAvailable"],
                                "swap_used_kib": values["SwapTotal"] - values["SwapFree"],
                                **{name: int((path / "memory.current").read_text()) for name, path in paths.items()}})
                stop.wait(0.25)
        except Exception as error:
            sampling_errors.append(error)

    def oom_events(path):
        return dict(line.split() for line in (path / "memory.events").read_text().splitlines())["oom_kill"]

    sampler = None
    try:
        baseline_id = deploy({"index.html": b"<h1>snaphost-existing-site-rehearsal</h1>"})
        deadline = time.monotonic() + 600
        while time.monotonic() < deadline:
            baseline = detail(baseline_id)
            if baseline["status"] in ("running", "failed"):
                break
            time.sleep(1)
        if baseline["status"] != "running":
            raise RuntimeError("baseline site did not start: " + str(baseline.get("failure_reason")))
        baseline_name = baseline["container_id"]
        containers = {"builder": args.builder, "app": args.app, "caddy": args.caddy, "existing_site": baseline_name}
        paths = {name: cgroup(container) for name, container in containers.items()}
        before = {name: inspect(container) for name, container in containers.items()}
        before_oom = {name: int(oom_events(path)) for name, path in paths.items()}
        host = memory()
        sampler = threading.Thread(target=sample, daemon=True)
        sampler.start()
        files = {name: (args.fixture / name).read_bytes() for name in
                 ("Dockerfile", "package.json", "package-lock.json", "index.html", "server.mjs", "src/main.js")}
        started = time.monotonic()
        node_id = deploy(files)
        probes = 0
        deadline = started + 600
        while time.monotonic() < deadline:
            api("/health")
            if "snaphost-existing-site-rehearsal" not in site(baseline_name, 8080):
                raise RuntimeError("existing site stopped serving")
            probes += 1
            node = detail(node_id)
            if node["status"] in ("running", "failed"):
                break
            time.sleep(1)
        if node["status"] != "running":
            raise RuntimeError("Node deploy did not start: " + str(node.get("failure_reason")))
        html = site(node["container_id"], 3000)
        asset = re.search(r'src="(/assets/[^\"]+\.js)"', html)
        if not asset or "snaphost-node-build-rehearsal" not in site(node["container_id"], 3000, asset[1]):
            raise RuntimeError("Node site did not serve its compiled JavaScript")
        stop.set()
        sampler.join()
        if sampling_errors:
            raise RuntimeError("memory sampling failed") from sampling_errors[0]
        after = {name: inspect(container) for name, container in containers.items()}
        deltas = {name: int(oom_events(path)) - before_oom[name] for name, path in paths.items()}
        if any(deltas.values()) or any(after[name]["RestartCount"] != value["RestartCount"] for name, value in before.items()):
            raise RuntimeError("a container was OOM killed or restarted during the build")
        runtime = inspect(node["container_id"])
        if runtime["State"]["OOMKilled"] or runtime["RestartCount"]:
            raise RuntimeError("Node runtime was OOM killed or restarted")
        evidence = {"host_ram_kib": host["MemTotal"], "host_swap_kib": host["SwapTotal"],
                    "buildkit_limit_bytes": before["builder"]["HostConfig"]["Memory"],
                    "node_deploy_id": node_id, "created_at": node["created_at"], "running_at": node["updated_at"],
                    "duration_seconds": round(time.monotonic() - started, 1), "samples": len(samples),
                    "host_used_peak_kib": max(row["used_kib"] for row in samples),
                    "host_swap_peak_kib": max(row["swap_used_kib"] for row in samples),
                    "cgroup_peaks_bytes": {name: max(row[name] for row in samples) for name in paths},
                    "oom_kill_deltas": deltas, "health_and_existing_site_probes": probes,
                    "html_and_javascript": "HTTP 200, compiled React marker present"}
        args.output.write_text(json.dumps(evidence, indent=2) + "\n")
        args.output.chmod(0o600)
        print(json.dumps(evidence, indent=2))
    finally:
        stop.set()
        if sampler:
            sampler.join()
        for ident in reversed(deploys):
            if detail(ident)["status"] == "running":
                api("/api/v1/deploys/" + ident + "/stop", {})


if __name__ == "__main__":
    main()
