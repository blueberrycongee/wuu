const { test } = require("node:test");
const assert = require("node:assert/strict");
const { mkdtempSync, writeFileSync, readFileSync, rmSync } = require("node:fs");
const { tmpdir } = require("node:os");
const { join } = require("node:path");
const { createHash } = require("node:crypto");
const yaml = require("js-yaml");

test("update metadata binds downloads to final artifacts and rejects unexpected files", () => {
  const { finalizeUpdateMetadata } = require("./update-metadata.cjs");
  const dir = mkdtempSync(join(tmpdir(), "wuu-update-metadata-"));
  const version = "2026.10.12";
  const zip = "wuu-mac-arm64.zip";
  const dmg = "wuu-mac-arm64.dmg";
  const metadata = { version, files: [{ url: zip, sha512: "old" }, { url: dmg, sha512: "old" }], path: zip, sha512: "old" };
  const path = join(dir, "latest-mac.yml");
  try {
    writeFileSync(join(dir, zip), "stapled app archive");
    writeFileSync(join(dir, dmg), "signed and stapled dmg");
    writeFileSync(path, yaml.dump(metadata));
    finalizeUpdateMetadata(dir, version);
    const result = yaml.load(readFileSync(path, "utf8"));
    for (const file of result.files) {
      const bytes = readFileSync(join(dir, file.url));
      assert.equal(file.size, bytes.length);
      assert.equal(file.sha512, createHash("sha512").update(bytes).digest("base64"));
    }
    assert.equal(result.sha512, result.files[0].sha512);
    writeFileSync(path, yaml.dump({ ...metadata, version: "2026.10.11" }));
    assert.throws(() => finalizeUpdateMetadata(dir, version), /version/);
    writeFileSync(path, yaml.dump({ ...metadata, files: [{ url: "../other.zip" }] }));
    assert.throws(() => finalizeUpdateMetadata(dir, version), /files/);
    writeFileSync(path, yaml.dump({ ...metadata, files: [{ url: dmg }] }));
    assert.throws(() => finalizeUpdateMetadata(dir, version), /files/);
  } finally { rmSync(dir, { recursive: true, force: true }); }
});
