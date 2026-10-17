import { accessSync, constants, readFileSync, readdirSync, statSync } from "node:fs";
import { delimiter, isAbsolute, join } from "node:path";
import type { EngineListResult } from "../../src/shared/protocol";

// The standalone preview has no app-server or saved settings. Resolve CLI
// availability without starting either agent. Keep the fallback locations in
// step with internal/enginecatalog/lookup.go; the preview does not load Go.
export function discoverPreviewEngines(env: NodeJS.ProcessEnv = process.env): EngineListResult {
  const directories = (env.PATH ?? "").split(delimiter).filter(isAbsolute);
  const home = (process.platform === "win32" ? env.USERPROFILE : env.HOME)?.trim();
  if (home && isAbsolute(home)) {
    directories.push(...[
      ".local/bin", "bin", ".opencode/bin", ".npm-global/bin", ".volta/bin",
      ".asdf/shims", ".proto/shims", ".nix-profile/bin", ".mise/shims",
    ].map((suffix) => join(home, suffix)));
    directories.push(join(env.XDG_DATA_HOME || join(home, ".local/share"), "mise/shims"));
    if (env.MISE_DATA_DIR) directories.push(join(env.MISE_DATA_DIR, "shims"));
    const nvm = nvmBinDirectory(env.NVM_DIR || join(home, ".nvm"));
    if (nvm) directories.push(nvm);
  }
  if (process.platform === "win32") {
    if (env.APPDATA) directories.push(join(env.APPDATA, "npm"));
    if (env.LOCALAPPDATA) directories.push(join(env.LOCALAPPDATA, "Programs"));
  } else {
    directories.push("/opt/homebrew/bin", "/home/linuxbrew/.linuxbrew/bin", "/opt/local/bin");
  }
  const engines = ["codex", "claude"].map((id) => {
    const override = env[`WUU_${id.toUpperCase()}_BINARY`]?.trim();
    const extensions = process.platform === "win32"
      ? (env.PATHEXT ?? ".COM;.EXE;.BAT;.CMD").split(";")
      : [""];
    const candidates = override ? [override] : directories.filter(isAbsolute)
      .flatMap((directory) => extensions.map((extension) => join(directory, id + extension)));
    const binary = candidates.find((candidate) => {
      try {
        if (!isAbsolute(candidate) || !statSync(candidate).isFile()) return false;
        accessSync(candidate, constants.X_OK);
        return true;
      } catch {
        return false;
      }
    });
    return { id, enabled: Boolean(binary), binary_ok: Boolean(binary), binary_path: binary };
  });
  return { engines: [{ id: "wuu", enabled: true, binary_ok: true }, ...engines] };
}

// Follow the selected nvm default, never an arbitrary installed Node version.
function nvmBinDirectory(root: string): string | undefined {
  let alias = "default";
  for (let depth = 0; depth <= 8; depth++) {
    if (!/^(?:lts\/)?[\w.*-]+$/.test(alias) || alias.includes("..")) return;
    try {
      alias = readFileSync(join(root, "alias", alias), "utf8").trim();
      continue;
    } catch {
      const prefix = alias.replace(/^v/, "");
      if (!/^\d+(?:\.\d+){0,2}$/.test(prefix)) return;
      try {
        const versions = readdirSync(join(root, "versions/node"), { withFileTypes: true })
          .filter((entry) => entry.isDirectory() && /^v\d+\.\d+\.\d+$/.test(entry.name))
          .map((entry) => entry.name)
          .filter((name) => name === `v${prefix}` || name.startsWith(`v${prefix}.`))
          .sort((a, b) => b.localeCompare(a, "en", { numeric: true }));
        if (versions[0]) return join(root, "versions/node", versions[0], "bin");
      } catch {
        // nvm has not installed a matching Node version.
      }
      return;
    }
  }
}
