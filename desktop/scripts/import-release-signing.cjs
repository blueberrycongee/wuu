// CI-only: import an existing release identity, never generate one per build.
const { execFileSync } = require("node:child_process");
const { randomBytes, X509Certificate } = require("node:crypto");
const { writeFileSync, appendFileSync, rmSync } = require("node:fs");
const { join } = require("node:path");
const { releaseSigningIdentity, checkReleaseSigning } = require("./check-release-signing.cjs");

const env = process.env;
releaseSigningIdentity(env);
if (env.GITHUB_ACTIONS !== "true") throw new Error("This import script is restricted to ephemeral GitHub Actions runners.");
if (!env.RUNNER_TEMP || !env.GITHUB_ENV || !env.WUU_RELEASE_CERTIFICATE_P12 || !env.WUU_RELEASE_CERTIFICATE_PASSWORD) {
  throw new Error("Release signing requires the persistent P12, password, fingerprint, and GitHub Actions environment.");
}
const keychain = join(env.RUNNER_TEMP, "wuu-release.keychain-db");
const archive = join(env.RUNNER_TEMP, "wuu-release.p12");
const password = randomBytes(32).toString("hex");
function security(args) {
  try { execFileSync("security", args, { stdio: "pipe", timeout: 15_000 }); }
  catch (error) { throw new Error(`Release keychain operation failed: ${args[0]} (${error.status})`); }
}
try {
  writeFileSync(archive, Buffer.from(env.WUU_RELEASE_CERTIFICATE_P12, "base64"), { mode: 0o600 });
  security(["create-keychain", "-p", password, keychain]);
  security(["set-keychain-settings", "-lut", "21600", keychain]);
  security(["unlock-keychain", "-p", password, keychain]);
  security(["import", archive, "-k", keychain, "-f", "pkcs12", "-P", env.WUU_RELEASE_CERTIFICATE_PASSWORD,
    "-T", "/usr/bin/codesign", "-T", "/usr/bin/security"]);
  security(["set-key-partition-list", "-S", "apple-tool:,apple:,codesign:", "-s", "-k", password, keychain]);
  const publicCertificate = execFileSync("security", ["find-certificate", "-p", keychain], { encoding: "utf8" });
  const imported = new X509Certificate(publicCertificate);
  if (imported.fingerprint.replaceAll(":", "") !== releaseSigningIdentity(env)) {
    throw new Error("Imported certificate does not match the pinned release identity");
  }
  if (!imported.subject.includes("CN=Developer ID Application:") || !imported.subject.split("\n").includes(`OU=${env.WUU_RELEASE_TEAM_ID}`)) {
    throw new Error("Certificate is not a Developer ID Application certificate for the configured team");
  }
  const searchList = execFileSync("security", ["list-keychains", "-d", "user"], { encoding: "utf8" })
    .split("\n").map((line) => line.trim().replace(/^"|"$/g, "")).filter(Boolean);
  security(["list-keychains", "-d", "user", "-s", keychain, ...searchList]);
  checkReleaseSigning({ ...env, WUU_RELEASE_KEYCHAIN: keychain });
  appendFileSync(env.GITHUB_ENV, `WUU_RELEASE_KEYCHAIN=${keychain}\nWUU_RELEASE_SIGN_ID=${releaseSigningIdentity(env)}\n`);
} catch (error) {
  try { security(["delete-keychain", keychain]); } catch {}
  throw new Error("Release identity import failed; check certificate, password, fingerprint, and team.");
} finally {
  rmSync(archive, { force: true });
}
