#!/usr/bin/env python3
"""Own the real account/computer fixture and run UI tests on isolated test apps."""
import argparse
import json
import os
import pathlib
import signal
import subprocess
import sys

root = pathlib.Path(__file__).resolve().parents[2]
os.chdir(root)
parser = argparse.ArgumentParser()
parser.add_argument("platform", choices=["ios", "android"])
parser.add_argument("--device", help="Android emulator serial (iOS always creates a disposable simulator)")
parser.add_argument("--ios-runtime", help="Installed iOS version, e.g. 26.2; defaults to newest available")
args = parser.parse_args()
if not os.environ.get("WUU_NATIVE_PRIVATE_DATABASE"):
    os.environ["WUU_NATIVE_PRIVATE_DATABASE"] = "1"
    os.execvp("bash", ["bash", "clients/native/with-postgres.sh", sys.executable, __file__, *sys.argv[1:]])

pathlib.Path("clients/native/.build").mkdir(exist_ok=True)
subprocess.run(["go", "build", "-o", "clients/native/.build/testaccount", "./clients/native/testaccount"], check=True)
fixture = subprocess.Popen(["clients/native/.build/testaccount", "-live"], stdin=subprocess.PIPE, stdout=subprocess.PIPE, text=True)
created_device = None
adb = pathlib.Path(os.environ.get("ANDROID_HOME", "")) / "platform-tools/adb"
signal.signal(signal.SIGTERM, lambda *_: sys.exit(143))
try:
    first_line = fixture.stdout.readline()
    if not first_line:
        raise RuntimeError("UI fixture exited before becoming ready; inspect its stderr above")
    settings = json.loads(first_line)
    if args.platform == "ios":
        if args.device:
            raise SystemExit("iOS UI tests create a disposable simulator to isolate Keychain and app state")
        devices = json.loads(subprocess.check_output(["xcrun", "simctl", "list", "devices", "available", "--json"]))["devices"]
        runtimes = sorted((key for key in devices if ".iOS-" in key),
                          key=lambda key: tuple(map(int, key.rsplit(".iOS-", 1)[1].split("-"))), reverse=True)
        if args.ios_runtime:
            runtimes = [key for key in runtimes if key.endswith(".iOS-" + args.ios_runtime.replace(".", "-"))]
        choices = [(key, device) for key in runtimes for device in devices[key] if "iPhone" in device["name"]]
        if not choices:
            raise SystemExit("No matching iPhone simulator is installed; install the requested iOS runtime in Xcode")
        runtime, template = choices[0]
        created_device = subprocess.check_output(["xcrun", "simctl", "create", "Wuu Native UI Tests", template["deviceTypeIdentifier"], runtime], text=True).strip()
        subprocess.run(["xcrun", "simctl", "boot", created_device], check=True)
        subprocess.run(["xcrun", "simctl", "bootstatus", created_device, "-b"], check=True)
        subprocess.run(["xcodebuild", "test", "-project", "clients/native/ios/Wuu.xcodeproj", "-scheme", "Wuu",
            "-destination", "id=" + created_device, "-parallel-testing-enabled", "NO",
            "-derivedDataPath", "clients/native/ios/.build-xcode", "CODE_SIGN_IDENTITY=-", "CODE_SIGNING_ALLOWED=YES", "ONLY_ACTIVE_ARCH=YES", "WUU_NATIVE_UI_SERVER=" + settings["server"]], check=True)
    else:
        serial = args.device or os.environ.get("ANDROID_SERIAL")
        if not serial or not serial.startswith("emulator-"):
            raise SystemExit("Pass --device emulator-NNNN; physical devices are not test targets")
        port = settings["server"].rsplit(":", 1)[1]
        subprocess.run([str(adb), "-s", serial, "reverse", "tcp:" + port, "tcp:" + port], check=True)
        # The uitest variant has a separate package; no normal app data is touched.
        subprocess.run([str(adb), "-s", serial, "uninstall", "ai.wuu.nativeapp.uitest"], stdout=subprocess.DEVNULL)
        env = dict(os.environ, ANDROID_SERIAL=serial)
        subprocess.run(["clients/native/android/gradlew", "-p", "clients/native/android", "-PnativeUiTest",
            "-Pandroid.testInstrumentationRunnerArguments.server=" + settings["server"], ":app:connectedUitestAndroidTest"], env=env, check=True)
finally:
    if created_device:
        subprocess.run(["xcrun", "simctl", "shutdown", created_device], stdout=subprocess.DEVNULL)
        subprocess.run(["xcrun", "simctl", "delete", created_device], stdout=subprocess.DEVNULL)
    if args.platform == "android" and "serial" in locals() and serial and "port" in locals():
        subprocess.run([str(adb), "-s", serial, "reverse", "--remove", "tcp:" + port], stdout=subprocess.DEVNULL)
    if fixture.poll() is None:
        try:
            fixture.stdin.write("quit\n"); fixture.stdin.flush()
        except BrokenPipeError:
            pass
        try: fixture.wait(timeout=15)
        except subprocess.TimeoutExpired:
            fixture.terminate(); fixture.wait(timeout=10)
