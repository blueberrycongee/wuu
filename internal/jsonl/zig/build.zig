const std = @import("std");

pub fn build(b: *std.Build) void {
    const target = b.standardTargetOptions(.{});
    const optimize = b.standardOptimizeOption(.{ .preferred_optimize_mode = .ReleaseFast });
    _ = optimize; // unused - we force ReleaseFast below
    const forced_optimize = std.builtin.OptimizeMode.ReleaseFast;

    const mod = b.createModule(.{
        .root_source_file = b.path("jsonl.zig"),
        .target = target,
        .optimize = forced_optimize,
    });

    const lib = b.addLibrary(.{
        .name = "jsonl",
        .root_module = mod,
        .linkage = .static,
    });

    b.installArtifact(lib);
}
