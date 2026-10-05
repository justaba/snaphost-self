#!/usr/bin/env python3
"""Run a built SnapHost image without AI credentials and exercise real deploys.

Build first: docker build -f snaphost-backend/docker/Dockerfile \
  -t snaphost-optional-ai:test snaphost-backend
Then: python3 infra/tests/optional_ai_integration_test.py

Requires Docker and Python 3. Uses isolated application state and BuildKit,
ephemeral loopback ports, and removes its containers and deploy image tags.
No provider credentials are read, and login credentials stay in memory.
"""

import http.cookiejar
import io
import json
import os
import subprocess
import tarfile
import time
import urllib.error
import urllib.request
import uuid


def docker(*args, check=True):
    result = subprocess.run(
        ["docker", *args], capture_output=True, text=True, check=False
    )
    if check and result.returncode:
        # Commands never contain credentials; container logs are read separately.
        raise RuntimeError(f"docker {args[0]} failed: {result.stderr.strip()}")
    if args[0] == "logs":
        return result.stdout + result.stderr
    return result.stdout.strip()


def main():
    image = os.environ.get("SNAPHOST_TEST_IMAGE", "snaphost-optional-ai:test")
    suffix = uuid.uuid4().hex[:12]
    app = "snaphost-optional-ai-" + suffix
    builder = app + "-builder"
    network = app + "-control"
    deploy_ids = []
    created_routing = False
    try:
        docker("image", "inspect", image)
        gid = docker(
            "run", "--rm", "--entrypoint", "stat",
            "-v", "/var/run/docker.sock:/var/run/docker.sock:ro",
            image, "-c", "%g", "/var/run/docker.sock",
        )
        docker("network", "create", network)
        if not docker("network", "ls", "--filter", "name=^snaphost-net$", "-q"):
            docker("network", "create", "snaphost-net")
            created_routing = True
        docker(
            "run", "-d", "--name", builder, "--network", network,
            "--security-opt", "seccomp=unconfined",
            "--security-opt", "apparmor=unconfined",
            "--security-opt", "systempaths=unconfined",
            "moby/buildkit:v0.29.0-rootless", "--addr", "tcp://0.0.0.0:1234",
        )
        for _ in range(60):
            workers = docker(
                "exec", builder, "buildctl", "--addr", "tcp://127.0.0.1:1234",
                "debug", "workers", check=False,
            )
            if "linux/" in workers:
                break
            time.sleep(1)
        else:
            raise RuntimeError("test BuildKit did not become ready")
        docker(
            "run", "-d", "--name", app, "--network", network,
            "--group-add", gid, "--memory", "512m",
            "-v", "/var/run/docker.sock:/var/run/docker.sock",
            "-p", "127.0.0.1::8080", "-e", "OPENROUTER_API_KEY=",
            "-e", "LLM_ENABLED=", "-e", "LLM_BASE_URL=http://127.0.0.1:1",
            "-e", "ALLOWED_IMAGE_PREFIXES=snaphost/",
            "-e", "SESSION_COOKIE_SECURE=false",
            "-e", "OPERATOR_EMAIL=optional-ai@test.invalid",
            "-e", f"BUILDKIT_HOST=tcp://{builder}:1234", image,
        )
        docker("network", "connect", "snaphost-net", app)
        port = docker("port", app, "8080/tcp").split(":")[-1]
        base = f"http://127.0.0.1:{port}"
        opener = urllib.request.build_opener(
            urllib.request.ProxyHandler({}),
            urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()),
        )

        def api(path, payload=None, raw=None):
            body = raw if raw is not None else (
                json.dumps(payload).encode() if payload is not None else None
            )
            request = urllib.request.Request(base + path, data=body)
            if body is not None:
                request.add_header(
                    "Content-Type", "application/gzip" if raw is not None else "application/json"
                )
            with opener.open(request, timeout=15) as response:
                return json.load(response)

        for _ in range(60):
            try:
                api("/health")
                break
            except (urllib.error.URLError, ConnectionError):
                time.sleep(1)
        else:
            raise RuntimeError("SnapHost did not start without AI credentials")
        print("PASS: application health is HTTP 200 without a provider key", flush=True)
        password = None
        for line in docker("logs", app).splitlines():
            try:
                entry = json.loads(line)
            except json.JSONDecodeError:
                continue
            if entry.get("password"):
                password = entry["password"]
        if not password:
            raise RuntimeError("bootstrap password was not found")
        api("/api/v1/auth/login", {"email": "optional-ai@test.invalid", "password": password})

        def deploy(files, want):
            archive = io.BytesIO()
            with tarfile.open(fileobj=archive, mode="w:gz") as tar:
                for name, content in files.items():
                    data = content.encode()
                    info = tarfile.TarInfo(name)
                    info.size = len(data)
                    info.mode = 0o644
                    tar.addfile(info, io.BytesIO(data))
            uploaded = api("/api/v1/deploys/upload", raw=archive.getvalue())
            created = api("/api/v1/deploys", {
                "source_type": "archive", "upload_id": uploaded["upload_id"],
            })
            deploy_id = created["deploy_id"]
            deploy_ids.append(deploy_id)
            deadline = time.monotonic() + 300
            while time.monotonic() < deadline:
                detail = api("/api/v1/deploys/" + deploy_id)
                if detail["status"] in ("running", "failed"):
                    if detail["status"] != want:
                        raise RuntimeError(f"deploy expected {want}: {json.dumps(detail)}")
                    return detail
                time.sleep(1)
            raise RuntimeError("deploy timed out")

        def assert_site(detail, port, marker):
            cid = detail["container_id"]
            inspection = json.loads(docker("inspect", cid))[0]
            address = inspection["NetworkSettings"]["Networks"]["snaphost-net"]["IPAddress"]
            body = docker("exec", app, "curl", "--noproxy", "*", "-fsS", f"http://{address}:{port}/")
            if marker not in body:
                raise RuntimeError("deployed site returned unexpected content")

        templated = deploy({"index.html": "<h1>template-without-ai</h1>"}, "running")
        assert_site(templated, 8080, "template-without-ai")
        print("PASS: built-in template deploy serves HTTP 200 without AI", flush=True)

        authored = deploy({
            "Dockerfile": 'FROM nginx:alpine\nCOPY index.html /usr/share/nginx/html/index.html\nCOPY nginx.conf /etc/nginx/conf.d/default.conf\nEXPOSE 8080\nCMD ["nginx", "-g", "daemon off;"]\n',
            "nginx.conf": "server { listen 8080; root /usr/share/nginx/html; index index.html; }\n",
            "index.html": "<h1>own-dockerfile-without-ai</h1>",
        }, "running")
        assert_site(authored, 8080, "own-dockerfile-without-ai")
        print("PASS: project Dockerfile deploy serves HTTP 200 without AI", flush=True)

        unsupported = deploy({"README.md": "No Dockerfile or supported framework."}, "failed")
        if "add a Dockerfile" not in json.dumps(unsupported):
            raise RuntimeError("unsupported project did not show the Dockerfile hint")
        retries = unsupported["saga"]["retry_count"]
        if retries != 0:
            raise RuntimeError(f"disabled AI triggered retries: {retries}")
        usage = docker(
            "exec", app, "sqlite3", "/var/snaphost/data/snaphost.db",
            "SELECT count(*) FROM ai_usage_log WHERE provider='openrouter';",
        )
        if usage != "0":
            raise RuntimeError("provider usage was recorded with AI disabled")
        print("PASS: unsupported project fails with a Dockerfile hint, zero retries and zero provider usage", flush=True)
    finally:
        docker("rm", "-f", app, builder, check=False)
        for deploy_id in deploy_ids:
            for cid in docker("ps", "-aq", "--filter", f"label=snaphost.deploy.id={deploy_id}").splitlines():
                docker("rm", "-f", cid, check=False)
            for ref in docker(
                "image", "ls", "--format", "{{.Repository}}:{{.Tag}}",
                "--filter", f"reference=snaphost/*:{deploy_id}",
            ).splitlines():
                docker("image", "rm", ref, check=False)
        docker("network", "rm", network, check=False)
        if created_routing:
            docker("network", "rm", "snaphost-net", check=False)


if __name__ == "__main__":
    main()
