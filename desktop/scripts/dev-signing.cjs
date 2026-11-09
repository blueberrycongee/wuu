const { randomBytes } = require("node:crypto");
const { chmodSync, mkdtempSync, rmSync } = require("node:fs");
const { execFileSync, spawnSync } = require("node:child_process");
const { tmpdir } = require("node:os");
const { join } = require("node:path");

const DEFAULT_DEV_SIGNING_ID = "Wuu Dev Signing";

function ensureDevSigningIdentity(env = process.env) {
  if (process.platform !== "darwin") {
    return { identity: "-", fingerprint: "adhoc", name: "ad-hoc" };
  }

  const requested = env.WUU_DEV_SIGN_ID?.trim();
  if (requested === "-") {
    return { identity: "-", fingerprint: "adhoc", name: "ad-hoc" };
  }

  let match = findIdentity(requested || DEFAULT_DEV_SIGNING_ID);
  if (!match && requested) {
    throw new Error(
      `WUU_DEV_SIGN_ID=${requested} is not a valid macOS Code Signing identity. `
      + "Create or import that identity in the login keychain, then run npm run dev again.",
    );
  }
  if (!match) {
    console.log(`creating local ${DEFAULT_DEV_SIGNING_ID} certificate for stable macOS development permissions...`);
    createDefaultIdentity();
    match = findIdentity(DEFAULT_DEV_SIGNING_ID);
  }
  if (!match) {
    throw new Error(
      `macOS did not register ${DEFAULT_DEV_SIGNING_ID} as a valid Code Signing identity. `
      + "Open Keychain Access and verify that the certificate is trusted for Code Signing.",
    );
  }
  return { identity: match.sha1, fingerprint: match.sha1, name: match.name };
}

function parseCodeSigningIdentities(output) {
  return Array.from(output.matchAll(/^\s*\d+\)\s+([A-F0-9]{40})\s+"(.+)"$/gm), ([, sha1, name]) => ({
    sha1,
    name,
  }));
}

function matchingIdentity(identities, requested) {
  const normalized = requested.toUpperCase();
  return identities.find(({ sha1, name }) => sha1 === normalized || name === requested);
}

function findIdentity(requested) {
  const output = execFileSync("security", ["find-identity", "-v", "-p", "codesigning"], {
    encoding: "utf8",
  });
  return matchingIdentity(parseCodeSigningIdentities(output), requested);
}

function createDefaultIdentity() {
  const workspace = mkdtempSync(join(tmpdir(), "wuu-dev-signing-"));
  const key = join(workspace, "identity.key");
  const certificate = join(workspace, "identity.pem");
  const archive = join(workspace, "identity.p12");
  const password = randomBytes(24).toString("hex");
  try {
    runQuiet("openssl", [
      "req", "-x509", "-newkey", "rsa:2048", "-keyout", key, "-out", certificate,
      "-sha256", "-days", "3650", "-nodes",
      "-subj", "/CN=Wuu Dev Signing/O=Wuu Development/",
      "-addext", "basicConstraints=critical,CA:TRUE",
      "-addext", "keyUsage=critical,digitalSignature,keyCertSign",
      "-addext", "extendedKeyUsage=codeSigning",
    ]);
    chmodSync(key, 0o600);
    runQuiet("openssl", [
      "pkcs12", "-export", "-out", archive, "-inkey", key, "-in", certificate,
      "-passout", `pass:${password}`,
    ]);
    const keychain = defaultKeychain();
    runQuiet("security", [
      "import", archive, "-k", keychain, "-f", "pkcs12", "-P", password,
      "-T", "/usr/bin/codesign", "-T", "/usr/bin/security",
    ]);
    runQuiet("security", [
      "add-trusted-cert", "-r", "trustRoot", "-p", "codeSign", "-k", keychain, certificate,
    ]);
  } finally {
    rmSync(workspace, { recursive: true, force: true });
  }
}

function defaultKeychain() {
  return execFileSync("security", ["default-keychain"], { encoding: "utf8" })
    .trim()
    .replace(/^"|"$/g, "");
}

function runQuiet(command, args) {
  const result = spawnSync(command, args, { encoding: "utf8" });
  if (result.status === 0) return;
  const detail = [result.stdout, result.stderr].filter(Boolean).join("\n").trim();
  throw new Error(`${command} failed${detail ? `: ${detail}` : ""}`);
}

module.exports = {
  DEFAULT_DEV_SIGNING_ID,
  ensureDevSigningIdentity,
  matchingIdentity,
  parseCodeSigningIdentities,
};
