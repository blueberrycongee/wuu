// Expo SDK 52+ auto-configures Metro for monorepos (watchFolders and
// nodeModulesPaths are derived from the workspace layout, and package
// exports resolution is on by default). @wuu/remote-core and @wuu/protocol
// are `file:` symlinks whose exports point at TS sources; Metro transpiles
// them like app code.
const { getDefaultConfig } = require("expo/metro-config");

module.exports = getDefaultConfig(__dirname);
