"""Optional cloud transports for the private execution worker protocol."""
import contextlib
import fcntl
import json
import math
import os
import pathlib
import selectors
import shlex
import subprocess
import sys
import threading

profile = json.loads(os.environ["WUU_EXECUTION_PROFILE"])
identity = os.environ["WUU_EXECUTION_ID"]
session = os.environ["WUU_EXECUTION_SESSION"]
state = pathlib.Path(os.environ["WUU_EXECUTION_STATE"])
state.mkdir(mode=0o700, parents=True, exist_ok=True)
worker = profile.get("worker") or "wuu"
lifetime = profile.get("lifetime_seconds") or 600
command = [worker, "execution-connect", "--socket", identity + "-" + session, "--idle-seconds", str(lifetime)]
persistent = profile.get("persistent", False)
shared = profile.get("shared", False)


def load():
    try:
        return json.loads((state / "cloud.json").read_text())
    except FileNotFoundError:
        return {}


def save(data):
    temporary = state / "cloud.json.tmp"
    with open(temporary, "w", encoding="utf-8") as output:
        os.chmod(temporary, 0o600)
        json.dump(data, output)
        output.flush()
        os.fsync(output.fileno())
    os.replace(temporary, state / "cloud.json")


@contextlib.contextmanager
def provisioning_lock():
    with open(state / "cloud.lock", "a", encoding="utf-8") as lock:
        os.chmod(state / "cloud.lock", 0o600)
        fcntl.flock(lock, fcntl.LOCK_EX)
        try:
            yield
        finally:
            fcntl.flock(lock, fcntl.LOCK_UN)


def pipe_process(argv):
    child = subprocess.Popen(argv, stdin=sys.stdin.buffer, stdout=sys.stdout.buffer, stderr=sys.stderr.buffer)
    try:
        status = child.wait()
        if status:
            raise RuntimeError("cloud execution transport exited unsuccessfully")
    finally:
        if child.poll() is None:
            child.terminate()
            child.wait(timeout=10)


def modal_backend():
    import modal
    with provisioning_lock():
        saved = load()
        sandbox = None
        if saved.get("sandbox"):
            try:
                candidate = modal.Sandbox.from_id(saved["sandbox"])
                if candidate.poll() is None:
                    sandbox = candidate
            except modal.exception.NotFoundError:
                if persistent and not saved.get("snapshot"):
                    raise RuntimeError("saved environment is unavailable and has no filesystem snapshot")
        if sandbox is None:
            if persistent and saved.get("sandbox") and not saved.get("snapshot"):
                raise RuntimeError("saved environment expired without a filesystem snapshot")
            image = modal.Image.from_id(saved["snapshot"]) if saved.get("snapshot") else modal.Image.from_registry(profile["image"])
            app = modal.App.lookup("wuu-execution", create_if_missing=True)
            sandbox = modal.Sandbox.create(
                image=image, app=app, timeout=lifetime,
                cpu=(profile.get("cpus") or 2, profile.get("cpus") or 2),
                memory=(profile.get("memory_mb") or 2048, profile.get("memory_mb") or 2048),
                block_network=profile.get("network") == "none",
            )
            saved["sandbox"] = sandbox.object_id
            save(saved)
    process = sandbox.exec(*command)
    errors = []
    def input_stream():
        try:
            for line in sys.stdin.buffer:
                process.stdin.write(line)
                process.stdin.drain()
            process.stdin.write_eof()
            process.stdin.drain()
        except Exception as error:
            errors.append(error)
    threading.Thread(target=input_stream, daemon=True).start()
    def error_stream():
        for chunk in process.stderr:
            sys.stderr.write(chunk)
    threading.Thread(target=error_stream, daemon=True).start()
    try:
        for chunk in process.stdout:
            sys.stdout.write(chunk)
            sys.stdout.flush()
        process.wait()
        if process.returncode or errors:
            raise RuntimeError("cloud execution stream failed")
    finally:
        if persistent or not shared:
            with provisioning_lock():
                if persistent:
                    saved["snapshot"] = sandbox.snapshot_filesystem(ttl=None).object_id
                    save(saved)
                if not shared:
                    sandbox.terminate()
                    saved.pop("sandbox", None)
                    save(saved)


def daytona_backend():
    from daytona import Daytona, CreateSandboxFromImageParams, Resources
    client = Daytona()
    with provisioning_lock():
        saved = load()
        if saved.get("sandbox"):
            sandbox = client.get(saved["sandbox"])
            if str(sandbox.state).lower().endswith("stopped"):
                sandbox.start()
        else:
            sandbox = client.create(CreateSandboxFromImageParams(
                image=profile["image"],
                resources=Resources(cpu=profile.get("cpus") or 2, memory=max(1, math.ceil((profile.get("memory_mb") or 2048) / 1024))),
                network_block_all=profile.get("network") == "none",
                auto_stop_interval=max(1, math.ceil(lifetime / 60)),
            ))
            saved["sandbox"] = sandbox.id
            save(saved)
        access = sandbox.create_ssh_access(expires_in_minutes=max(1, math.ceil(lifetime / 60)))
    try:
        ssh = shlex.split(access.ssh_command)
        if not ssh or ssh[0] != "ssh":
            raise RuntimeError("backend returned an invalid SSH transport")
        pipe_process(["ssh", "-T", "-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=accept-new"] + ssh[1:] + [shlex.join(command)])
    finally:
        sandbox.revoke_ssh_access(token=access.token)
        if not shared:
            with provisioning_lock():
                if persistent:
                    sandbox.stop()
                else:
                    client.delete(sandbox)
                    save({})


def terminal_transport(argv):
    # The CLI's interactive channel requires a terminal. Protocol frames are
    # the only bytes forwarded; terminal banners and echoed requests are dropped.
    import pty
    import termios
    master, slave = pty.openpty()
    attrs = termios.tcgetattr(slave)
    attrs[3] &= ~(termios.ECHO | termios.ICANON)
    termios.tcsetattr(slave, termios.TCSANOW, attrs)
    child = subprocess.Popen(argv, stdin=slave, stdout=slave, stderr=subprocess.DEVNULL, start_new_session=True)
    os.close(slave)
    selector = selectors.DefaultSelector()
    selector.register(master, selectors.EVENT_READ)
    selector.register(sys.stdin.fileno(), selectors.EVENT_READ)
    pending = b""
    try:
        while child.poll() is None:
            for key, _ in selector.select(timeout=1):
                if key.fd == sys.stdin.fileno():
                    data = os.read(key.fd, 65536)
                    if not data:
                        return
                    view = memoryview(data)
                    while view:
                        count = os.write(master, view)
                        view = view[count:]
                else:
                    try:
                        data = os.read(master, 65536)
                    except OSError:
                        data = b""
                    if not data:
                        return
                    pending += data
                    if len(pending) > 16 * 1024 * 1024:
                        raise RuntimeError("cloud transport frame exceeds limit")
                    while b"\n" in pending:
                        line, pending = pending.split(b"\n", 1)
                        try:
                            message = json.loads(line.strip())
                        except (ValueError, UnicodeDecodeError):
                            continue
                        if isinstance(message, dict) and "token" not in message and ("data" in message or "error" in message):
                            if message.get("method") not in (None, "tool", "process"):
                                continue
                            sys.stdout.write(json.dumps(message) + "\n")
                            sys.stdout.flush()
    finally:
        selector.close()
        os.close(master)
        if child.poll() is None:
            child.terminate()
        child.wait(timeout=10)


def vercel_backend():
    with provisioning_lock():
        saved = load()
        if not saved.get("sandbox"):
            argv = ["sandbox", "create", "--name", identity, "--silent", "--image", profile["image"],
                    "--timeout", str(lifetime) + "s", "--network-policy", "deny-all" if profile.get("network") == "none" else "allow-all"]
            if not persistent and not shared:
                argv.append("--non-persistent")
            if profile.get("cpus"):
                argv += ["--vcpus", str(int(profile["cpus"]))]
            subprocess.run(argv, check=True, stdout=subprocess.DEVNULL)
            save({"sandbox": identity})
    try:
        terminal_transport(["sandbox", "exec", "--interactive", identity, "--", "/bin/sh", "-c", "stty raw -echo; exec " + shlex.join(command)])
    finally:
        if not shared:
            subprocess.run(["sandbox", "stop", identity], check=True, stdout=subprocess.DEVNULL)
            if not persistent:
                with provisioning_lock():
                    subprocess.run(["sandbox", "remove", identity], check=True, stdout=subprocess.DEVNULL)
                    save({})


try:
    {"modal": modal_backend, "daytona": daytona_backend, "vercel_sandbox": vercel_backend}[profile["backend"]]()
except ImportError as error:
    sys.stderr.write("Install the selected cloud backend SDK in the configured Python environment: " + str(error) + "\n")
    sys.exit(1)
except Exception as error:
    sys.stderr.write("Execution environment failed: " + str(error) + "\n")
    sys.exit(1)
