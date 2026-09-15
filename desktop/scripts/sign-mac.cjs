const { signAsync } = require("@electron/osx-sign");
const { openSync, readSync, closeSync, lstatSync } = require("node:fs");
const { execFileSync } = require("node:child_process");
const { checkReleaseSigning } = require("./check-release-signing.cjs");

module.exports = async function signMac(options) {
  const identity = process.env.WUU_RELEASE_SIGN_ID ? checkReleaseSigning() : "-";
  await signAsync({
    ...options,
    identity,
    identityValidation: false,
    keychain: process.env.WUU_RELEASE_KEYCHAIN,
    // These previews are not Developer ID notarized. No provisioning profile
    // is needed; recipient Macs do not import the signing certificate.
    preAutoEntitlements: false,
    preEmbedProvisioningProfile: false,
    gatekeeperAssess: false,
    // Binary resources are sealed by the containing bundle. Signing them as
    // code adds extended attributes that may be lost when users copy the app.
    ignore: [(path) => {
      const stat = lstatSync(path);
      if (stat.isSymbolicLink()) return true;
      if (!stat.isFile()) return false;
      const fd = openSync(path, "r");
      try {
        const header = Buffer.alloc(4);
        readSync(fd, header, 0, 4, 0);
        return !["feedface", "cefaedfe", "feedfacf", "cffaedfe", "cafebabe", "bebafeca", "cafebabf", "bfbafeca"].includes(header.toString("hex"));
      } finally { closeSync(fd); }
    }],
    optionsForFile: () => ({ hardenedRuntime: false, timestamp: "none" }),
  });
  execFileSync("codesign", ["--verify", "--deep", "--strict", options.app]);
};
