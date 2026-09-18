const { execFileSync } = require("node:child_process");

function releaseSigningIdentity(env = process.env) {
  const identity = env.WUU_RELEASE_SIGN_ID?.trim();
  // Pin a certificate fingerprint, never a name that could select a new key.
  if (!identity || !/^[A-Fa-f0-9]{40}$/.test(identity)) {
    throw new Error("Set WUU_RELEASE_SIGN_ID to the persistent release certificate SHA-1 fingerprint. Ad-hoc release signing is not allowed.");
  }
  return identity.toUpperCase();
}

function checkReleaseSigning(env = process.env) {
  const identity = releaseSigningIdentity(env);
  const args = ["find-identity", "-v", "-p", "codesigning"];
  if (env.WUU_RELEASE_KEYCHAIN) args.push(env.WUU_RELEASE_KEYCHAIN);
  const identities = execFileSync("security", args, { encoding: "utf8" });
  if (!identities.toUpperCase().includes(identity)) {
    throw new Error("The persistent release signing identity is not available in the release keychain.");
  }
  return identity;
}

module.exports = { releaseSigningIdentity, checkReleaseSigning };
if (require.main === module) checkReleaseSigning();
