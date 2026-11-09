const { execFileSync } = require("node:child_process");

function releaseSigningIdentity(env = process.env) {
  const identity = env.WUU_RELEASE_SIGN_ID?.trim();
  // Pin a certificate fingerprint, never a name that could select a new key.
  if (!identity || !/^[A-Fa-f0-9]{40}$/.test(identity)) {
    throw new Error("Set WUU_RELEASE_SIGN_ID to the persistent certificate SHA-1 fingerprint for certificate-backed signing.");
  }
  return identity.toUpperCase();
}

function checkReleaseSigning(env = process.env) {
  const identity = releaseSigningIdentity(env);
  const args = ["find-identity", "-v", "-p", "codesigning"];
  if (env.WUU_RELEASE_KEYCHAIN) args.push(env.WUU_RELEASE_KEYCHAIN);
  const identities = execFileSync("security", args, { encoding: "utf8" });
  if (!identities.split("\n").some((line) => line.toUpperCase().includes(identity) && line.includes("Developer ID Application:"))) {
    throw new Error("The pinned Developer ID Application identity is not available in the release keychain.");
  }
  return identity;
}

function releaseCredentials(env = process.env) {
  releaseSigningIdentity(env);
  for (const name of ["WUU_RELEASE_TEAM_ID", "APPLE_API_KEY", "APPLE_API_KEY_ID", "APPLE_API_ISSUER"]) {
    if (!env[name]?.trim()) throw new Error(`Missing release credential: ${name}`);
  }
  if (!/^[A-Z0-9]{10}$/.test(env.WUU_RELEASE_TEAM_ID)) throw new Error("Invalid release Team ID");
}

module.exports = { releaseSigningIdentity, checkReleaseSigning, releaseCredentials };
if (require.main === module) { releaseCredentials(); checkReleaseSigning(); }
