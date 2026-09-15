const assert = require("node:assert/strict");
const { test } = require("node:test");
const { execFileSync } = require("node:child_process");
const { mkdtempSync, mkdirSync, readFileSync, writeFileSync, rmSync, copyFileSync } = require("node:fs");
const { tmpdir } = require("node:os");
const { join } = require("node:path");
const { releaseSigningIdentity } = require("./check-release-signing.cjs");

test("release rejects missing or ad-hoc signing identity", () => {
  for (const value of [undefined, "-", "Wuu Dev Signing"]) {
    assert.throws(() => releaseSigningIdentity({ WUU_RELEASE_SIGN_ID: value }));
  }
});

const isolated = process.env.GITHUB_ACTIONS === "true";
test("changed signed builds retain a verifiable designated requirement", { skip: process.platform !== "darwin" || (!isolated && !process.env.WUU_RELEASE_SIGN_ID) }, async () => {
  const dir = mkdtempSync(join(tmpdir(), "wuu-release-signing-test-"));
  const keychain = join(dir, "wuu-release.keychain-db");
  const cert = join(dir, "certificate.pem");
  const key = join(dir, "private.pem");
  const p12 = join(dir, "identity.p12");
  const output = join(dir, "environment");
  const password = "temporary-test-identity";
  const run = (command, args, env = process.env) => {
    try { return execFileSync(command, args, { env, encoding: "utf8", stdio: "pipe" }); }
    catch (error) { throw new Error(`${command} failed during isolated signing test: ${error.stderr?.toString() || ""}`); }
  };
  const saved = { id: process.env.WUU_RELEASE_SIGN_ID, keychain: process.env.WUU_RELEASE_KEYCHAIN };
  try {
    if (isolated) {
      run("openssl", ["req", "-x509", "-newkey", "rsa:2048", "-keyout", key, "-out", cert,
        "-sha256", "-days", "1", "-nodes", "-subj", "/CN=Wuu Isolated Signing Test/",
        "-addext", "basicConstraints=critical,CA:TRUE", "-addext", "keyUsage=critical,digitalSignature,keyCertSign",
        "-addext", "extendedKeyUsage=codeSigning"]);
      run("openssl", ["pkcs12", "-export", "-out", p12, "-inkey", key, "-in", cert, "-keypbe", "PBE-SHA1-3DES", "-certpbe", "PBE-SHA1-3DES", "-macalg", "sha1", "-passout", `pass:${password}`]);
      const fingerprint = run("openssl", ["x509", "-in", cert, "-noout", "-fingerprint", "-sha1"]).trim().split("=")[1].replaceAll(":", "");
      run(process.execPath, [join(__dirname, "import-release-signing.cjs")], {
        ...process.env, RUNNER_TEMP: dir, GITHUB_ENV: output,
        WUU_RELEASE_SIGN_ID: fingerprint,
        WUU_RELEASE_CERTIFICATE_PASSWORD: password,
        WUU_RELEASE_CERTIFICATE_P12: readFileSync(p12).toString("base64"),
    });
    process.env.WUU_RELEASE_SIGN_ID = fingerprint;
    process.env.WUU_RELEASE_KEYCHAIN = keychain;
    }
    const sign = require("./sign-mac.cjs");
    const app = join(dir, "wuu.app");
    const contents = join(app, "Contents");
    const bin = join(contents, "Resources", "bin");
    mkdirSync(bin, { recursive: true });
    mkdirSync(join(contents, "MacOS"));
    writeFileSync(join(contents, "Info.plist"), `<?xml version="1.0"?><plist version="1.0"><dict><key>CFBundleIdentifier</key><string>com.blueberrycongee.wuu</string><key>CFBundleExecutable</key><string>wuu</string><key>CFBundlePackageType</key><string>APPL</string></dict></plist>`);
    const source = join(dir, "main.c");
    const executable = join(contents, "MacOS", "wuu");
    const requirements = [];
    for (const version of [1, 2]) {
      writeFileSync(source, `int main(void) { return ${version}; }\n`);
      run("clang", [source, "-o", executable]);
      for (const name of ["wuu-core", "wuu-cua-mac", "wuu-cua-mac-pip"]) copyFileSync(executable, join(bin, name));
      await sign({ app, platform: "darwin", version: "44.1.0" });
      const requirementFile = join(dir, `requirements-${version}`);
      run("codesign", ["-d", "-r", requirementFile, app]);
      requirements.push(readFileSync(requirementFile));
      run(process.execPath, [join(__dirname, "verify-mac-release.cjs"), app]);
    }
    assert.deepEqual(requirements[0], requirements[1]);
    run("codesign", ["--verify", "-R", "=" + requirements[0].toString().replace(/^designated =>\s*/, "").trim(), app]);
  } finally {
    if (saved.id === undefined) delete process.env.WUU_RELEASE_SIGN_ID; else process.env.WUU_RELEASE_SIGN_ID = saved.id;
    if (saved.keychain === undefined) delete process.env.WUU_RELEASE_KEYCHAIN; else process.env.WUU_RELEASE_KEYCHAIN = saved.keychain;
    if (isolated) {
      try { run("sudo", ["-n", "security", "remove-trusted-cert", "-d", join(dir, "wuu-release-signing.pem")]); } catch {}
      try { run("security", ["delete-keychain", keychain]); } catch {}
    }
    rmSync(dir, { recursive: true, force: true });
  }
});
