const { writeFileSync, appendFileSync } = require("node:fs");
const { join } = require("node:path");
const { createPrivateKey } = require("node:crypto");
const { releaseCredentials } = require("./check-release-signing.cjs");

if (process.env.GITHUB_ACTIONS !== "true" || !process.env.RUNNER_TEMP || !process.env.GITHUB_ENV) {
  throw new Error("Notary key installation requires an ephemeral GitHub Actions runner");
}
const path = join(process.env.RUNNER_TEMP, "wuu-notary.p8");
releaseCredentials({ ...process.env, APPLE_API_KEY: path });
try {
  const key = createPrivateKey(process.env.APPLE_API_KEY_P8 || "");
  if (key.asymmetricKeyType !== "ec") throw new Error("Unexpected key type");
} catch {
  throw new Error("Missing or invalid App Store Connect team API private key");
}
writeFileSync(path, process.env.APPLE_API_KEY_P8, { mode: 0o600 });
appendFileSync(process.env.GITHUB_ENV, `APPLE_API_KEY=${path}\n`);
