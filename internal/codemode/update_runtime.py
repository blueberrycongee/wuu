"""Refresh the pinned, integrity-verified interpreter distribution."""
import base64
import hashlib
import io
import json
from pathlib import Path
import tarfile
import urllib.request

URL = "https://registry.npmjs.org/quickjs-wasi/-/quickjs-wasi-3.6.2.tgz"
INTEGRITY = "FCqGtGOrMgzUiIrMNMA2YnsOxCNwo31dzqXvclXUC6xeT35NJLKXQJsvbeCTjvoFAwZgEAPg8U6+KAPDGXn8Mg=="
data = urllib.request.urlopen(URL, timeout=60).read()
if base64.b64encode(hashlib.sha512(data).digest()).decode() != INTEGRITY:
    raise RuntimeError("Runtime package integrity mismatch")
destination = Path(__file__).parent / "vendor" / "quickjs"
with tarfile.open(fileobj=io.BytesIO(data), mode="r:gz") as archive:
    for name in ("dist/index.js", "dist/extensions.js", "dist/wasi-shim.js", "dist/version.js", "LICENSE"):
        (destination / Path(name).name).write_bytes(archive.extractfile("package/" + name).read())

    wasm = archive.extractfile("package/quickjs.wasm").read()
    chunk_size = 64 * 1024
    chunk_names = set()
    for index, offset in enumerate(range(0, len(wasm), chunk_size)):
        name = f"quickjs.wasm.{index:03d}"
        chunk_names.add(name)
        (destination / name).write_bytes(wasm[offset:offset + chunk_size])
    (destination / "wasm.json").write_text(json.dumps({
        "size": len(wasm), "sha256": hashlib.sha256(wasm).hexdigest(),
    }, indent=2) + "\n")
    for old in destination.glob("quickjs.wasm*"):
        if old.name == "quickjs.wasm" or (
            old.name.startswith("quickjs.wasm.")
            and old.name.removeprefix("quickjs.wasm.").isdigit()
            and old.name not in chunk_names
        ):
            old.unlink()

NOTICES = [
    ('QuickJS-NG engine', 'https://raw.githubusercontent.com/quickjs-ng/quickjs/6d46d07d04041b40f4f49eaa7fdebe44c314c699/LICENSE', '96f73f9d2a16c21a36b418f06073be26e7d6d5e7c1bc99756b21a4f2c74ef171'),
    ('WASI SDK / compiler runtime', 'https://raw.githubusercontent.com/WebAssembly/wasi-sdk/wasi-sdk-32/LICENSE', '268872b9816f90fd8e85db5a28d33f8150ebb8dd016653fb39ef1f94f2686bc5'),
    ('WASI libc: LICENSE', 'https://raw.githubusercontent.com/WebAssembly/wasi-libc/2fc32bc81b9f07f8d9525edea59bfbaf760c06d6/LICENSE', '2711a8b5a5cdfef0e639f96c1aca12ae23d7d64a02d0507f1bdf14d2b27bbc3a'),
    ('WASI libc: LICENSE-APACHE-LLVM', 'https://raw.githubusercontent.com/WebAssembly/wasi-libc/2fc32bc81b9f07f8d9525edea59bfbaf760c06d6/LICENSE-APACHE-LLVM', '268872b9816f90fd8e85db5a28d33f8150ebb8dd016653fb39ef1f94f2686bc5'),
    ('WASI libc: LICENSE-APACHE', 'https://raw.githubusercontent.com/WebAssembly/wasi-libc/2fc32bc81b9f07f8d9525edea59bfbaf760c06d6/LICENSE-APACHE', 'a60eea817514531668d7e00765731449fe14d059d3249e0bc93b36de45f759f2'),
    ('WASI libc: LICENSE-MIT', 'https://raw.githubusercontent.com/WebAssembly/wasi-libc/2fc32bc81b9f07f8d9525edea59bfbaf760c06d6/LICENSE-MIT', '23f18e03dc49df91622fe2a76176497404e46ced8a715d9d2b67a7446571cca3'),
    ('WASI libc: libc-bottom-half/cloudlibc/LICENSE', 'https://raw.githubusercontent.com/WebAssembly/wasi-libc/2fc32bc81b9f07f8d9525edea59bfbaf760c06d6/libc-bottom-half/cloudlibc/LICENSE', 'c8b789cf5a746611e6300a0cc7750dbf92b61912a709d04e639245f7290656d0'),
    ('WASI libc: libc-top-half/musl/COPYRIGHT', 'https://raw.githubusercontent.com/WebAssembly/wasi-libc/2fc32bc81b9f07f8d9525edea59bfbaf760c06d6/libc-top-half/musl/COPYRIGHT', 'f9bc4423732350eb0b3f7ed7e91d530298476f8fec0c6c427a1c04ade22655af'),
    ('WASI libc: dlmalloc/src/malloc.c', 'https://raw.githubusercontent.com/WebAssembly/wasi-libc/2fc32bc81b9f07f8d9525edea59bfbaf760c06d6/dlmalloc/src/malloc.c', '9e7ce51d813331ae87ef235f6ca2d5ce2167692e30bb51a173513e3754a67f8f'),
 ]
notices = []
for title, url, digest in NOTICES:
    content = urllib.request.urlopen(url, timeout=60).read()
    if hashlib.sha256(content).hexdigest() != digest:
        raise RuntimeError("Runtime notice integrity mismatch")
    text = content.decode()
    if title.endswith("malloc.c"):
        text = text.split("* Quickstart")[0].removeprefix("/*").strip() + "\n"
    text = "\n".join(line.rstrip() for line in text.splitlines()) + "\n"
    notices.append(title + "\nSource: " + url + "\n\n" + text)
(destination / "THIRD_PARTY_NOTICES").write_text("\n\n".join(notices))
