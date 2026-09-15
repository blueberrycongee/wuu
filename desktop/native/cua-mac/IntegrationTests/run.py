#!/usr/bin/env python3
"""Opt-in macOS acceptance test using a disposable app, never a user's document."""
import json
import os
import pathlib
import plistlib
import re
import select
import signal
import subprocess
import tempfile
import time

ROOT = pathlib.Path(__file__).resolve().parents[1]
HELPER = pathlib.Path(os.environ.get("CUA_MAC_TEST_HELPER", ROOT / ".build/debug/wuu-cua-mac"))


class Client:
    def __init__(self):
        self.process = subprocess.Popen([str(HELPER)], stdin=subprocess.PIPE, stdout=subprocess.PIPE, text=True)
        self.request_id = 0

    def send(self, args):
        self.request_id += 1
        request = {"id": self.request_id, "method": "tools/call", "params": {"name": "computer", "arguments": args}}
        self.process.stdin.write(json.dumps(request) + "\n")
        self.process.stdin.flush()
        return self.request_id

    def receive(self):
        assert select.select([self.process.stdout], [], [], 30)[0], "native response timed out"
        response = json.loads(self.process.stdout.readline())
        assert "result" in response, response
        return response["result"]

    def call(self, args):
        self.send(args)
        return self.receive()

    def cancel(self, request_id):
        self.process.stdin.write(json.dumps({"method": "notifications/cancelled", "params": {"requestId": request_id}}) + "\n")
        self.process.stdin.flush()

    def close(self):
        if self.process.poll() is None:
            self.process.stdin.close()
            try:
                self.process.wait(timeout=5)
            except subprocess.TimeoutExpired:
                self.process.kill()
                self.process.wait()


def text(result):
    return result["content"][0]["text"]


def field(result):
    return int(re.search(r'\[(\d+)\] AXTextField[^\n]*title="Name"', text(result)).group(1))


def frame(result):
    return tuple(int(value) for value in re.search(r"frame=\(([^)]*)\)", text(result)).group(1).split(","))


def main():
    client = Client()
    pid = None
    try:
        permissions = client.call({"action": "permission_status"})["structuredContent"]
        if not permissions.get("accessibility") or not permissions.get("screen_recording"):
            print("SKIP: fixture acceptance needs existing Accessibility and Screen Recording grants")
            return 77
        with tempfile.TemporaryDirectory(prefix="wuu-cua-fixture-") as temporary:
            bundle = pathlib.Path(temporary) / "Fixture.app"
            executable = bundle / "Contents/MacOS/fixture"
            executable.parent.mkdir(parents=True)
            with (bundle / "Contents/Info.plist").open("wb") as output:
                plistlib.dump({"CFBundleIdentifier": "dev.wuu.cua.integration", "CFBundleName": "Wuu CUA Fixture",
                              "CFBundleExecutable": "fixture", "CFBundlePackageType": "APPL"}, output)
            subprocess.run(["swiftc", str(ROOT / "IntegrationTests/fixture.swift"), "-o", str(executable)], check=True, timeout=60)

            def args(action, **values):
                return dict(app=str(bundle), control_epoch="fixture", action=action, **values)

            state = client.call(args("observe", mode="ax"))
            assert not state["isError"], state
            assert all(part["type"] != "image" for part in state["content"])
            pid = state["structuredContent"]["process_id"]
            snapshot = state["structuredContent"]["snapshot_id"]
            result = client.call(args("set_value", snapshot_id=snapshot, element_id=field(state), value="Ada",
                                      expect={"title": "Name", "value": "Ada"}, after="ax"))
            assert result["structuredContent"]["delivery"] == "delivered", result
            assert result["structuredContent"]["verification"] == "matched", result
            stale = client.call(args("set_value", snapshot_id=snapshot, element_id=field(state), value="Must not be written"))
            assert stale["structuredContent"]["error_code"] == "stale_snapshot", stale
            assert stale["structuredContent"]["delivery"] == "not_delivered", stale

            state = client.call(args("observe", mode="ax"))
            assert 'value="Ada"' in text(state)
            os.kill(pid, signal.SIGUSR1)
            time.sleep(0.15)  # Real AppKit/WindowServer propagation, outside merge-gate tests.
            moved = client.call(args("set_value", snapshot_id=state["structuredContent"]["snapshot_id"], element_id=field(state), value="Wrong window"))
            assert moved["structuredContent"]["error_code"] == "stale_snapshot", moved
            visual = client.call(args("observe", mode="both"))
            assert any(part["type"] == "image" for part in visual["content"]), visual
            geometry = visual["structuredContent"]["screenshot"]["window_frame"]
            assert (geometry["width"], geometry["height"]) == frame(visual)[2:], geometry

            request = client.send(args("type_text", snapshot_id=visual["structuredContent"]["snapshot_id"], element_id=field(visual), text="abc" * 100000))
            time.sleep(0.1)
            client.cancel(request)
            interrupted = client.receive()["structuredContent"]
            assert interrupted["error_code"] == "cancelled", interrupted
            assert 0 < interrupted["input_units_attempted"] < 18000, interrupted
            assert interrupted["delivery"] == "unknown", interrupted
            state = client.call(args("observe", mode="ax"))
            assert not state["isError"], state
            original = frame(state)
            hidden = client.call(args("conceal_app"))
            assert hidden["structuredContent"].get("concealed_windows") == 1, hidden
            client.process.kill()
            client.process.wait(timeout=5)
            client = Client()
            restored = client.call(args("observe", mode="ax"))
            assert frame(restored) == original, (original, frame(restored))
            print("PASS: AX-only observation, verified write, stale and moved-window rejection, matched capture, partial cancellation, crash restoration")
        return 0
    finally:
        client.close()
        if pid:
            try:
                os.kill(pid, signal.SIGTERM)
            except ProcessLookupError:
                pass


if __name__ == "__main__":
    raise SystemExit(main())
