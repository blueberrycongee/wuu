#!/usr/bin/env python3
"""Opt-in macOS acceptance test using a disposable app, never a user's document."""
import base64
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


AGENT_DISPLAY_PROBE = """
import CoreGraphics
var count: UInt32 = 0
CGGetActiveDisplayList(0, nil, &count)
var ids = [CGDirectDisplayID](repeating: 0, count: Int(count))
CGGetActiveDisplayList(count, &ids, &count)
for id in ids where CGDisplayVendorNumber(id) == 0x7775 && CGDisplayModelNumber(id) == 0xC0A1 {
    let b = CGDisplayBounds(id)
    print(Int(b.minX), Int(b.minY), Int(b.width), Int(b.height))
}
"""


def agent_display_bounds(temporary):
    probe = pathlib.Path(temporary) / "agent-display.swift"
    probe.write_text(AGENT_DISPLAY_PROBE)
    output = subprocess.run(["swift", str(probe)], capture_output=True, text=True, timeout=60).stdout.split()
    return tuple(int(value) for value in output[:4]) if len(output) >= 4 else None


def save_artifact(result, name):
    """Keeps the captured frame for review when CUA_MAC_TEST_ARTIFACTS names a directory."""
    directory = os.environ.get("CUA_MAC_TEST_ARTIFACTS")
    image = next((part for part in result["content"] if part["type"] == "image"), None)
    if not directory or not image:
        return
    pathlib.Path(directory).mkdir(parents=True, exist_ok=True)
    (pathlib.Path(directory) / name).write_bytes(base64.b64decode(image["data"]))


def set_app_visible(pid, visible):
    script = f'tell application "System Events" to set visible of (first process whose unix id is {pid}) to {str(visible).lower()}'
    subprocess.run(["osascript", "-e", script], check=True, capture_output=True, timeout=20)


def app_visible(pid):
    script = f'tell application "System Events" to get visible of (first process whose unix id is {pid})'
    return subprocess.run(["osascript", "-e", script], check=True, capture_output=True, text=True, timeout=20).stdout.strip() == "true"


def preview_events(preview, seconds):
    events = []
    deadline = time.time() + seconds
    while time.time() < deadline:
        if not select.select([preview.stdout], [], [], 0.2)[0]:
            continue
        line = preview.stdout.readline()
        if not line:
            break
        event = json.loads(line)
        events.append(event)
        if event["event"] == "frame":
            preview.stdin.write(json.dumps({"type": "ack", "sequence": event["sequence"]}) + "\n")
            preview.stdin.flush()
    return events


def check_preview_leaves_hidden_app_alone(pid, target, original, current_frame):
    """A hidden app stays hidden and in place while previewed; frames resume when the user shows it."""
    set_app_visible(pid, False)
    time.sleep(0.5)
    preview = subprocess.Popen([str(HELPER), "--native-preview", "fixture", target, str(pid), "0", str(os.getpid())],
                               stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, text=True)
    try:
        events = preview_events(preview, 3)
        assert not any(event["event"] == "frame" for event in events), events
        assert any(event.get("status") == "suspended" for event in events), events
        assert not app_visible(pid), "the preview revealed a hidden app"
        set_app_visible(pid, True)
        events = preview_events(preview, 8)
        assert any(event["event"] == "frame" for event in events), "no frames after the user showed the app"
        assert current_frame() == original, (original, current_frame())
    finally:
        if preview.poll() is None:
            preview.stdin.write(json.dumps({"type": "close"}) + "\n")
            preview.stdin.flush()
            preview.wait(timeout=10)


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
            # The parked window lives on the invisible HiDPI agent display and is
            # captured whole, not as a sliver clamped onto a physical display.
            parked = client.call(args("observe", mode="both"))
            shot = parked["structuredContent"]["screenshot"]
            window = shot["window_frame"]
            assert (window["width"], window["height"]) == original[2:], (window, original)
            agent = agent_display_bounds(temporary)
            assert agent, "agent display is not online while a window is concealed"
            assert (agent[0] <= window["x"] and agent[1] <= window["y"]
                    and window["x"] + window["width"] <= agent[0] + agent[2]
                    and window["y"] + window["height"] <= agent[1] + agent[3]), (agent, window)
            assert shot["width"] == 2 * window["width"], shot
            visible = shot["visible_image_frame"]
            assert visible["width"] * visible["height"] >= 0.95 * shot["width"] * shot["height"], shot
            save_artifact(parked, "concealed-window.png")
            client.process.kill()
            client.process.wait(timeout=5)
            client = Client()
            restored = client.call(args("observe", mode="ax"))
            assert frame(restored) == original, (original, frame(restored))
            check_preview_leaves_hidden_app_alone(pid, "dev.wuu.cua.integration", original, lambda: frame(client.call(args("observe", mode="ax"))))
            print("PASS: AX-only observation, verified write, stale and moved-window rejection, matched capture, partial cancellation, agent display concealment, crash restoration, preview of a hidden app")
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
