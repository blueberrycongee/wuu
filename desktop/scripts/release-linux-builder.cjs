const { build } = require("../package.json");

if (process.platform !== "linux" || process.arch !== "x64") {
  throw new Error("Linux releases require a native Linux x64 builder");
}
module.exports = {
  ...build,
  publish: { provider: "github", owner: "blueberrycongee", repo: "wuu", releaseType: "release" },
};
