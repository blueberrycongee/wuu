const { releaseCredentials, checkReleaseSigning } = require("./check-release-signing.cjs");

releaseCredentials();
const identity = checkReleaseSigning();
if (process.env.WUU_RELEASE !== "1") throw new Error("Use npm run release:desktop");

const { build } = require("../package.json");
module.exports = {
  ...build,
  publish: null,
  forceCodeSigning: true,
  mac: { ...build.mac, identity, hardenedRuntime: true, notarize: false },
  afterSign: "scripts/notarize-app.cjs",
};
