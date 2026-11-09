const { resolve } = require("node:path");

// Keep experimental settings and runtime state local to this checkout.
// An explicit WUU_HOME opts into a different (including shared) data directory.
function devHome(env, repoRoot) {
  return resolve(repoRoot, env.WUU_HOME?.trim() || ".wuu-dev");
}

module.exports = { devHome };
