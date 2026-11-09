import { lstat, readlink, unlink } from "node:fs/promises";
import { homedir } from "node:os";
import { dirname, join, resolve } from "node:path";

export type LegacyCliLinkEnv = {
  homeDir?: string;
  resourcesPath?: string;
  platform?: NodeJS.Platform;
};

export async function removeLegacyDesktopCliLink(
  env: LegacyCliLinkEnv = {},
): Promise<boolean> {
  const platform = env.platform ?? process.platform;
  if (platform === "win32") {
    return false;
  }

  const target = join(env.homeDir ?? homedir(), ".local", "bin", "wuu");
  let stat;
  try {
    stat = await lstat(target);
  } catch {
    return false;
  }
  if (!stat.isSymbolicLink()) {
    return false;
  }

  const rawSource = await readlink(target);
  const source = resolve(dirname(target), rawSource);
  const resourcesPath =
    env.resourcesPath ?? (process as { resourcesPath?: string }).resourcesPath ?? "";
  const currentAppTargets = resourcesPath
    ? [resolve(resourcesPath, "bin", "wuu"), resolve(resourcesPath, "wuu")]
    : [];
  const packagedAppTarget =
    /(?:^|[\\/])wuu\.app[\\/]Contents[\\/]Resources[\\/](?:bin[\\/])?wuu$/i.test(source);
  if (!currentAppTargets.includes(source) && !packagedAppTarget) {
    return false;
  }

  await unlink(target);
  return true;
}
