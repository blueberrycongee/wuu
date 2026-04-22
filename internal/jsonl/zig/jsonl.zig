const std = @import("std");

/// Single-pass scan: finds all line start positions in one traversal.
/// Returns the number of line-start offsets written.
/// offsets[0] is always 0 (start of first line).
/// For each '\n' at position i, offsets[N] = i+1 (start of next line).
/// If data doesn't end with '\n', the last line's start is the last offset.
export fn jsonl_scan_lines(ptr: [*]const u8, len: usize, offsets: [*]usize, max_count: usize) usize {
    if (len == 0 or max_count == 0) return 0;

    const Vec16 = @Vector(16, u8);
    const nl: Vec16 = @splat('\n');

    var count: usize = 0;
    offsets[count] = 0;
    count += 1;

    var i: usize = 0;

    // SIMD fast path: process 16 bytes at a time.
    // Only fall back to per-byte scan when the chunk contains a newline.
    while (i + 16 <= len) : (i += 16) {
        const chunk: Vec16 = ptr[i..][0..16].*;
        const eq: @Vector(16, u8) = @intFromBool(chunk == nl);

        // Quick reject: if no newline in this chunk, skip entirely.
        if (@reduce(.Add, eq) == 0) {
            continue;
        }

        // Slow path: at least one newline found in this chunk.
        inline for (0..16) |j| {
            if (ptr[i + j] == '\n') {
                if (count < max_count) {
                    offsets[count] = i + j + 1;
                    count += 1;
                }
            }
        }
    }

    // Scalar tail.
    while (i < len) : (i += 1) {
        if (ptr[i] == '\n') {
            if (count < max_count) {
                offsets[count] = i + 1;
                count += 1;
            }
        }
    }

    return count;
}

// ── Legacy exports (kept for ABI compatibility, not used by new Go code) ──

export fn jsonl_count_lines(ptr: [*]const u8, len: usize) usize {
    if (len == 0) return 0;

    const Vec16 = @Vector(16, u8);
    const nl: Vec16 = @splat('\n');

    var count: usize = 0;
    var i: usize = 0;

    while (i + 16 <= len) : (i += 16) {
        const chunk: Vec16 = ptr[i..][0..16].*;
        const eq: @Vector(16, u8) = @intFromBool(chunk == nl);
        count += @reduce(.Add, eq);
    }

    while (i < len) : (i += 1) {
        if (ptr[i] == '\n') count += 1;
    }

    if (ptr[len - 1] != '\n') count += 1;
    return count;
}

export fn jsonl_fill_offsets(ptr: [*]const u8, len: usize, offsets: [*]usize, max_count: usize) usize {
    if (len == 0 or max_count == 0) return 0;

    const Vec16 = @Vector(16, u8);
    const nl: Vec16 = @splat('\n');

    var count: usize = 0;
    offsets[count] = 0;
    count += 1;

    var i: usize = 0;

    while (i + 16 <= len) : (i += 16) {
        const chunk: Vec16 = ptr[i..][0..16].*;
        const eq: @Vector(16, u8) = @intFromBool(chunk == nl);

        if (@reduce(.Add, eq) == 0) {
            continue;
        }

        inline for (0..16) |j| {
            if (ptr[i + j] == '\n') {
                if (count < max_count) {
                    offsets[count] = i + j + 1;
                    count += 1;
                }
            }
        }
    }

    while (i < len) : (i += 1) {
        if (ptr[i] == '\n') {
            if (count < max_count) {
                offsets[count] = i + 1;
                count += 1;
            }
        }
    }

    return count;
}

export fn jsonl_find_newlines(ptr: [*]const u8, len: usize, positions: [*]usize, max_count: usize) usize {
    if (len == 0 or max_count == 0) return 0;

    const Vec16 = @Vector(16, u8);
    const nl: Vec16 = @splat('\n');

    var written: usize = 0;
    var i: usize = 0;

    while (i + 16 <= len) : (i += 16) {
        const chunk: Vec16 = ptr[i..][0..16].*;
        const eq: @Vector(16, u8) = @intFromBool(chunk == nl);

        if (@reduce(.Add, eq) == 0) {
            continue;
        }

        inline for (0..16) |j| {
            if (ptr[i + j] == '\n') {
                if (written < max_count) {
                    positions[written] = i + j;
                    written += 1;
                }
            }
        }
    }

    while (i < len) : (i += 1) {
        if (ptr[i] == '\n') {
            if (written < max_count) {
                positions[written] = i;
                written += 1;
            }
        }
    }

    return written;
}
