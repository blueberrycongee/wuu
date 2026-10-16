const { createHash } = require("node:crypto");
const { readFileSync, writeFileSync, readdirSync } = require("node:fs");
const { join } = require("node:path");
const yaml = require("js-yaml");

function finalizeUpdateMetadata(root, version) {
  const expected = ["zip", "dmg"].map((ext) => `wuu-mac-arm64.${ext}`);
  const manifests = readdirSync(root).filter((name) => name.endsWith("-mac.yml"));
  if (!manifests.length) throw new Error("Missing macOS update metadata");
  for (const name of manifests) {
    const path = join(root, name);
    const info = yaml.load(readFileSync(path, "utf8"));
    if (info?.version !== version) throw new Error("Update metadata version mismatch");
    if (!Array.isArray(info.files) || info.files.length !== expected.length ||
        expected.some((url) => info.files.filter((file) => file.url === url).length !== 1)) {
      throw new Error("Update metadata files must contain exactly the final ZIP and DMG");
    }
    for (const file of info.files) {
      const bytes = readFileSync(join(root, file.url));
      file.sha512 = createHash("sha512").update(bytes).digest("base64");
      file.size = bytes.length;
    }
    const zip = info.files.find((file) => file.url === expected[0]);
    info.path = zip.url;
    info.sha512 = zip.sha512;
    writeFileSync(path, yaml.dump(info));
  }
  return manifests;
}

module.exports = { finalizeUpdateMetadata };
