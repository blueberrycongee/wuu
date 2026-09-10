# wuu code-mode host

Sandboxed V8 runtime host for Wuu code mode. It speaks the version-1 code-mode
protocol (4-byte little-endian length-prefixed JSON) over stdio and implements
session cells with resource limits, tool delegation, and terminate.

Derived from the OpenAI Codex code-mode crates (Apache-2.0); see `NOTICE`.

## Build

Requires a Rust toolchain and the V8 from-source build prerequisites
(Clang 19+ on `LIBCLANG_PATH` for bindgen, Python 3, Ninja). The V8 prebuilt
sandbox archives do not exist for the pinned version, so the build must use
`V8_FROM_SOURCE=1`:

```sh
V8_FROM_SOURCE=1 cargo build --release -p codex-code-mode-host
```

`.cargo/config.toml` pins the GN args this build needs. The crates.io v8
package does not ship `third_party/icu/common/icudtl.dat`, so GN's default
`icu_use_data_file=false` path fails; `GN_ARGS` forces V8 to load ICU data
from `deno_core_icudata` at runtime, matching the verified Codex build
(`icu_use_data_file=true`, `icu_use_stub_data=true`,
`v8_enable_temporal_support=false`). With those args no ninja rule produces
`icudtl.dat`, but V8's `mksnapshot` still initializes ICU from a data file
next to the binary, so the build fails at `run_mksnapshot_default` unless
`icudtl.dat` exists in `target/{debug,release}/gn_out` before ninja reaches
that step. `scripts/seed-icu-data.sh` copies the exact file the runtime loads
at startup (the `deno_core_icudata-0.77.0` crate ships the same bytes via
`include_bytes!`) into both gn_out dirs; run it before the first build, or use
the Makefile target which does this for you:

```sh
make codemode-host
```

A from-source V8 build takes 30-60 minutes the first time; the static archive
lands in `target/debug/gn_out/obj/librusty_v8.a` (or `target/release/gn_out`).
The workspace edition is 2024 (the codex sources use let-chains), and
`crates/runtime/build.rs` emits the Apple framework links
(CoreFoundation/Foundation/CFNetwork/Security/SystemConfiguration + objc)
that the static V8 archive needs on macOS — the crates.io v8 build script
stops at `librusty_v8` and the final link fails without them.

macOS-specific fix in `v8_init.rs`: the first `IsolateGroup` creation races
on partition-alloc pool init when two cells start concurrently in a fresh
process (`[FATAL:address_pool_manager.cc(62)] Check failed:
!pool->IsInitialized()`). Creating one throwaway isolate inside the
process-wide init `OnceLock` initializes the pool before any cell can spawn;
the regression tests `concurrent_first_isolates_do_not_trap` and
`concurrent_spawn_runtime_does_not_trap` in `crates/runtime/src/runtime/mod.rs`
cover it. This is a wuu deviation from upstream codex and should be revisited
when the codex port is rebased onto a newer V8.

The binary is written to `target/release/wuu-code-mode-host`.

## Desktop integration

Desktop packaging commands always use `desktop/scripts/build-codemode-host.cjs`.
For day-to-day development, `npm run dev` skips this expensive build; use
`npm run dev:codemode` when working on or testing code mode. On macOS/Linux it
runs the locked, incremental Cargo build and copies the result next to
`wuu-core` in `desktop/build/bin`. Existing staged binaries do not bypass the
build. Windows requires a separately built Windows host staged in that
directory.

Wuu defaults to `code_mode.mode: "direct"`: models invoke ordinary tools
directly, with the code-mode runtime disabled. Set `code_mode.mode` to
`"code_only"` to invoke ordinary tools through `exec` and `wait`, or `"code"`
to offer both entry points and ordinary tools. Inside a cell, filter `ALL_TOOLS` by name or
description to discover tools and their input schemas, and print matches with
`text()`. In code-only mode, `exec` also describes the current tools and their
input schemas so the model can identify and call them without a discovery round trip.
Context switching remains a top-level control. When enabled, the host is started on
the first `exec` call and shared by conversations in the workspace session. Plain text replies
do not start a host process.

The core resolves the host from `code_mode.host`, then `WUU_CODE_MODE_HOST`,
then the binary next to its own executable. Both dev and packaged desktop
layouts supply that sibling binary. A standalone core without a host uses
direct tools.

## Run

```sh
target/release/wuu-code-mode-host --listen stdio
```

## Test

```sh
cargo test -p codex-code-mode-protocol -p codex-code-mode-runtime -p codex-code-mode-host
```

The Wuu Go client contract test can be pointed at the built binary:

```sh
WUU_CODE_MODE_HOST=$(pwd)/target/release/wuu-code-mode-host \
  go test ./internal/codemode -run TestHostIntegration -v
```
