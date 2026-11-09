import { randomUUID } from "node:crypto";
import fs from "node:fs";
import { basename, dirname, join } from "node:path";

type AtomicWriteOptions = {
  defaultMode?: number;
};

// Shared crash-safe write core: mkdir -p, create an O_EXCL temp sibling,
// fsync, then rename over the target. Text and binary variants only differ
// in the payload handed to writeFileSync, so both funnel through here to keep
// the durability guarantees (exclusive temp, fsync-before-rename) identical.
function writeFileAtomicSyncCore(
  path: string,
  payload: string | Buffer,
  encoding: BufferEncoding | undefined,
  options: AtomicWriteOptions,
): void {
  const directory = dirname(path);
  fs.mkdirSync(directory, { recursive: true });

  let mode = options.defaultMode ?? 0o600;
  try {
    mode = fs.statSync(path).mode & 0o777;
  } catch (error) {
    if (!isMissingFileError(error)) {
      throw error;
    }
  }

  const temporaryPath = join(
    directory,
    `.${basename(path)}.${process.pid}.${randomUUID()}.tmp`,
  );
  let descriptor: number | undefined;
  try {
    descriptor = fs.openSync(
      temporaryPath,
      fs.constants.O_CREAT | fs.constants.O_EXCL | fs.constants.O_WRONLY,
      mode,
    );
    fs.fchmodSync(descriptor, mode);
    if (typeof payload === "string") {
      fs.writeFileSync(descriptor, payload, encoding ?? "utf8");
    } else {
      fs.writeFileSync(descriptor, payload);
    }
    fs.fsyncSync(descriptor);
    fs.closeSync(descriptor);
    descriptor = undefined;
    fs.renameSync(temporaryPath, path);
  } catch (error) {
    if (descriptor !== undefined) {
      try {
        fs.closeSync(descriptor);
      } catch {
        // Preserve the write failure below.
      }
    }
    try {
      fs.rmSync(temporaryPath, { force: true });
    } catch {
      // Preserve the write failure below.
    }
    throw error;
  }
}

export function writeTextFileAtomicSync(
  path: string,
  text: string,
  options: AtomicWriteOptions = {},
): void {
  writeFileAtomicSyncCore(path, text, "utf8", options);
}

// Binary variant used by the embedded browser CDP bridge to spill PNG
// screenshots (and >1MB CDP result overflow) to a core-designated dest_path.
// A partially written screenshot must never be observable, so it reuses the
// same exclusive-temp + fsync + rename path as the text writer.
export function writeBufferFileAtomicSync(
  path: string,
  data: Buffer,
  options: AtomicWriteOptions = {},
): void {
  writeFileAtomicSyncCore(path, data, undefined, options);
}

function isMissingFileError(error: unknown): boolean {
  return (
    typeof error === "object" &&
    error !== null &&
    "code" in error &&
    error.code === "ENOENT"
  );
}
