# Embedded interpreter distribution

Pinned npm package: `quickjs-wasi@3.6.2` (MIT).
Upstream source: https://github.com/vercel-labs/quickjs-wasi
Package: https://registry.npmjs.org/quickjs-wasi/-/quickjs-wasi-3.6.2.tgz
SHA-512 (base64): `FCqGtGOrMgzUiIrMNMA2YnsOxCNwo31dzqXvclXUC6xeT35NJLKXQJsvbeCTjvoFAwZgEAPg8U6+KAPDGXn8Mg==`

The JavaScript files are unmodified published runtime assets. The original
WASM image is split into numbered chunks of at most 64 KiB, in byte order.
`wasm.json` records its exact size and SHA-256 digest; the host checks chunk
count, individual lengths, and the reconstructed digest before execution.
The ten chunks reconstruct the original 637405-byte image without modification. Regenerate with
`python3 internal/codemode/update_runtime.py`. Runtime assets are embedded in the
Go binary; installation and execution require no npm download or writable asset
cache. The host loads only these trusted modules. No module loader, filesystem,
network API, or native extension is provided to the guest interpreter.

THIRD_PARTY_NOTICES retains the engine and linked toolchain/library notices,
with upstream sources pinned to the engine revision and SDK 32 used by the
package build. Notices are also embedded in the distributed Go binary.
