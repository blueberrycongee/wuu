const { releaseCredentials, checkReleaseSigning } = require("./check-release-signing.cjs");

releaseCredentials();
const identity = checkReleaseSigning();
if (process.env.WUU_RELEASE !== "1") throw new Error("Use npm run release:desktop");

const { build } = require(require("node:path").resolve("package.json"));
const publish = { provider: "github", owner: "blueberrycongee", repo: "wuu", releaseType: "release" };
module.exports = {
  ...build,
  publish,
  // electron-builder omits the feed for dir targets; persist it before signing.
  async afterPack(context) {
    await require("./after-pack-linux.cjs")(context);
    require("node:fs").writeFileSync(
      require("node:path").join(context.packager.getResourcesDir(context.appOutDir), "app-update.yml"),
      require("js-yaml").dump(publish),
    );
  },
  forceCodeSigning: true,
  mac: { ...build.mac, identity, hardenedRuntime: true, notarize: false },
};
