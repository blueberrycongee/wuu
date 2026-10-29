import { mkdirSync } from "node:fs";
import { join } from "node:path";
import type { RuntimeContext } from "../shared/protocol";
import { wuuHomePath } from "./projects";

/** User hosts are addressed by identity, never by a renderer-supplied directory. */
export function resolveUserHostContext(userSpaceId: string, productDomain: string, compositionProfile?: string): Extract<RuntimeContext, {kind: "user"}> {
  if (!/^[a-zA-Z0-9][a-zA-Z0-9_-]{0,95}$/.test(userSpaceId)) throw new Error("Invalid user space identity");
  if (productDomain !== "personal" && productDomain !== "coding") throw new Error("Unknown product domain");
  if (compositionProfile !== undefined && !/^[a-zA-Z0-9][a-zA-Z0-9_-]{0,95}$/.test(compositionProfile)) throw new Error("Invalid composition profile");
  const cwd = join(wuuHomePath(), "user-spaces", userSpaceId, productDomain, "files");
  mkdirSync(cwd, { recursive: true, mode: 0o700 });
  return { kind: "user", user_space_id: userSpaceId, product_domain: productDomain, ...(compositionProfile ? {composition_profile: compositionProfile} : {}), cwd };
}
