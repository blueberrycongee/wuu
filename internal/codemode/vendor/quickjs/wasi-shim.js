/**
 * Minimal WASI shim for running QuickJS in any WebAssembly environment.
 *
 * Implements the subset of WASI snapshot_preview1 that QuickJS needs:
 *   - clock_time_get (for Date.now() and Math.random() PRNG seeding)
 *   - fd_write (for QuickJS runtime internal logging)
 *   - fd_close (stub)
 *   - fd_fdstat_get (stub)
 *   - fd_seek (stub)
 *   - random_get (for crypto extension and WASI libc init)
 *
 * Users can override any of these by providing their own implementations
 * in the `wasi` option when creating a VM. Overrides are applied to both
 * the main module and all loaded extensions.
 */
/** Returns a wasi_snapshot_preview1 import object with built-in defaults. */
export function createWasiShim(memoryAccessor) {
    // WASI error codes
    const ERRNO_SUCCESS = 0;
    const ERRNO_BADF = 8;
    const ERRNO_NOSYS = 52;
    // Clock IDs
    const CLOCK_REALTIME = 0;
    const CLOCK_MONOTONIC = 1;
    return {
        clock_time_get(clockId, _precision, resultPtr) {
            const mem = memoryAccessor();
            const view = new DataView(mem.buffer);
            if (clockId === CLOCK_REALTIME || clockId === CLOCK_MONOTONIC) {
                const timeNs = BigInt(Date.now()) * 1000000n;
                view.setBigUint64(resultPtr, timeNs, true);
                return ERRNO_SUCCESS;
            }
            return ERRNO_NOSYS;
        },
        fd_write(fd, iovsPtr, iovsLen, nwrittenPtr) {
            const mem = memoryAccessor();
            const view = new DataView(mem.buffer);
            const bytes = new Uint8Array(mem.buffer);
            let totalWritten = 0;
            if (fd !== 1 && fd !== 2) {
                return ERRNO_BADF;
            }
            for (let i = 0; i < iovsLen; i++) {
                const bufPtr = view.getUint32(iovsPtr + i * 8, true);
                const bufLen = view.getUint32(iovsPtr + i * 8 + 4, true);
                const chunk = bytes.slice(bufPtr, bufPtr + bufLen);
                const text = new TextDecoder().decode(chunk);
                if (fd === 1) {
                    if (typeof process !== 'undefined' && process.stdout) {
                        process.stdout.write(text);
                    }
                    else {
                        console.log(text);
                    }
                }
                else {
                    if (typeof process !== 'undefined' && process.stderr) {
                        process.stderr.write(text);
                    }
                    else {
                        console.error(text);
                    }
                }
                totalWritten += bufLen;
            }
            view.setUint32(nwrittenPtr, totalWritten, true);
            return ERRNO_SUCCESS;
        },
        fd_close(_fd) {
            return ERRNO_NOSYS;
        },
        fd_fdstat_get(fd, statPtr) {
            const mem = memoryAccessor();
            const view = new DataView(mem.buffer);
            if (fd === 1 || fd === 2) {
                view.setUint8(statPtr, 2); // fs_filetype = CHARACTER_DEVICE
                view.setUint16(statPtr + 2, 0, true); // fs_flags
                view.setBigUint64(statPtr + 8, 0n, true); // fs_rights_base
                view.setBigUint64(statPtr + 16, 0n, true); // fs_rights_inheriting
                return ERRNO_SUCCESS;
            }
            return ERRNO_BADF;
        },
        fd_seek(_fd, _offset, _whence, _resultPtr) {
            return ERRNO_NOSYS;
        },
        random_get(bufPtr, bufLen) {
            const mem = memoryAccessor();
            const bytes = new Uint8Array(mem.buffer, bufPtr, bufLen);
            if (typeof crypto !== 'undefined' && crypto.getRandomValues) {
                crypto.getRandomValues(bytes);
            }
            else {
                for (let i = 0; i < bufLen; i++) {
                    bytes[i] = Math.floor(Math.random() * 256);
                }
            }
            return ERRNO_SUCCESS;
        },
    };
}
//# sourceMappingURL=wasi-shim.js.map