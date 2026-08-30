#!/usr/bin/env python3
import hashlib
import http.client
import json
import os
import pathlib
import signal
import subprocess
import tempfile
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer


ROOT = pathlib.Path(__file__).resolve().parent.parent
BUILD = ROOT / ".build" / "matched"
PORT = int(os.environ.get("BIFROST_CANDIDATE_PORT", "11020"))
UPSTREAM_PORT = PORT + 1


class ShadowUpstream(BaseHTTPRequestHandler):
    def do_POST(self) -> None:
        length = int(self.headers.get("Content-Length", "0"))
        self.rfile.read(length)
        payload = json.dumps(
            {
                "id": "shadow-response",
                "object": "chat.completion",
                "created": 0,
                "model": "shadow-model",
                "choices": [{"index": 0, "message": {"role": "assistant", "content": "shadow ok"}, "finish_reason": "stop"}],
                "usage": {"prompt_tokens": 1, "completion_tokens": 2, "total_tokens": 3},
            }
        ).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(payload)))
        self.end_headers()
        self.wfile.write(payload)

    def log_message(self, _format: str, *_args: object) -> None:
        pass


def sha256(path: pathlib.Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as handle:
        for block in iter(lambda: handle.read(1024 * 1024), b""):
            digest.update(block)
    return digest.hexdigest()


def request(method: str, path: str, body: dict | None = None) -> tuple[int, str]:
    connection = http.client.HTTPConnection("127.0.0.1", PORT, timeout=3)
    payload = json.dumps(body).encode() if body is not None else None
    headers = {"Content-Type": "application/json"} if payload else {}
    connection.request(method, path, body=payload, headers=headers)
    response = connection.getresponse()
    content = response.read().decode(errors="replace")
    connection.close()
    return response.status, content


def main() -> None:
    required = [
        BUILD / "bifrost-http",
        BUILD / "official-llm-only.so",
        BUILD / "abi-probe.so",
        BUILD / "agent-capability-router.so",
    ]
    missing = [str(path) for path in required if not path.is_file()]
    if missing:
        raise SystemExit("Missing matched artifacts: " + ", ".join(missing))

    with tempfile.TemporaryDirectory(prefix="bifrost-matched-test.") as directory:
        app_dir = pathlib.Path(directory)
        config = {
            "config_store": {"enabled": False},
            "client": {"enable_logging": True, "log_level": "debug"},
            "providers": {
                "openai": {
                    "keys": [{"name": "shadow", "value": "env.BIFROST_SHADOW_KEY", "models": ["agent-main-auto"]}],
                    "network_config": {
                        "base_url": f"http://127.0.0.1:{UPSTREAM_PORT}/v1",
                        "allow_private_network": True,
                        "default_request_timeout_in_seconds": 3,
                    },
                }
            },
            "plugins": [
                {"name": "llm-only", "enabled": True, "path": str(required[1]), "config": {"enable_logging": True}},
                {"name": "agent-capability-router-abi-probe", "enabled": True, "path": str(required[2]), "config": {}},
                {
                    "name": "agent-capability-router",
                    "enabled": True,
                    "path": str(required[3]),
                    "config": {"shadow_mode": True, "active_roles": {"main": True, "worker": True}},
                },
            ],
        }
        (app_dir / "config.json").write_text(json.dumps(config, indent=2), encoding="utf-8")
        log_path = BUILD / "isolated-test.log"
        upstream = ThreadingHTTPServer(("127.0.0.1", UPSTREAM_PORT), ShadowUpstream)
        upstream_thread = threading.Thread(target=upstream.serve_forever, daemon=True)
        upstream_thread.start()
        with log_path.open("w", encoding="utf-8") as log:
            environment = os.environ.copy()
            environment["BIFROST_SHADOW_KEY"] = "shadow-only-key"
            process = subprocess.Popen(
                [str(required[0]), "-host", "127.0.0.1", "-port", str(PORT), "-app-dir", str(app_dir)],
                stdout=log,
                stderr=subprocess.STDOUT,
                start_new_session=True,
                text=True,
                env=environment,
            )
            try:
                for _ in range(60):
                    if process.poll() is not None:
                        raise RuntimeError("candidate exited during startup")
                    try:
                        status, _ = request("GET", "/health")
                        if status == 200:
                            break
                    except OSError:
                        pass
                    time.sleep(0.5)
                else:
                    raise RuntimeError("candidate health check timed out")

                root_status, root_response = request("GET", "/")
                version_status, version_response = request("GET", "/api/version")
                status, response = request(
                    "POST",
                    "/openai/v1/chat/completions",
                    {"model": "agent-main-auto", "messages": [{"role": "user", "content": "Summarize this short status."}]},
                )
                time.sleep(0.5)
            finally:
                if process.poll() is None:
                    os.killpg(process.pid, signal.SIGTERM)
                    try:
                        process.wait(timeout=10)
                    except subprocess.TimeoutExpired:
                        os.killpg(process.pid, signal.SIGKILL)
                        process.wait()
                upstream.shutdown()
                upstream.server_close()
                upstream_thread.join(timeout=2)

        output = log_path.read_text(encoding="utf-8", errors="replace")
        result = {
            "health": "ok",
            "root_status": root_status,
            "version_status": version_status,
            "version_response": version_response,
            "request_status": status,
            "request_response": response[:1000],
            "router_mode": "shadow",
            "shadow_request_processed": status == 200,
            "artifacts": {path.name: sha256(path) for path in required},
        }
        result_path = BUILD / "isolated-test-result.json"
        result_path.write_text(json.dumps(result, indent=2), encoding="utf-8")
        required_log_fragments = [
            "official-llm-only.so",
            "abi-probe.so",
            "agent-capability-router.so",
            "plugin status: agent-capability-router - active",
        ]
        absent = [fragment for fragment in required_log_fragments if fragment not in output]
        if absent:
            raise SystemExit(
                "Candidate log is missing: "
                + ", ".join(absent)
                + f"; request returned {status}: {response[:500]}"
            )
        if status != 200:
            raise SystemExit(f"Shadow request failed with {status}: {response[:500]}")
        if root_status != 200 or "<!doctype html>" not in root_response.lower():
            raise SystemExit(f"Embedded UI check failed with {root_status}: {root_response[:200]}")
        if version_status != 200 or json.loads(version_response) != "v2.0.0":
            raise SystemExit(f"Version check failed with {version_status}: {version_response[:200]}")

        result_path.write_text(json.dumps(result, indent=2), encoding="utf-8")
        (BUILD / "compatible-runtime.json").write_text(
            json.dumps({"host_sha256": sha256(required[0]), "plugin_sha256": sha256(required[3])}, indent=2),
            encoding="utf-8",
        )
        print(json.dumps(result, indent=2))


if __name__ == "__main__":
    main()
