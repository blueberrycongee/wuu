import type { SkillProjectIdentity } from "../shared/protocol";

export type DraftSkillReference = {
  start: number;
  end: number;
  token: string;
  name: string;
  source: string;
  path?: string;
  project?: SkillProjectIdentity;
};

/**
 * An explicit skill selection is stored as `/skill {catalog identity}` followed
 * by the user's arguments. Keep that raw form as the draft contract, but expose
 * the identity header as one atomic unit so the composer can present it as a
 * reference instead of editable JSON.
 */
export function draftSkillReference(value: string): DraftSkillReference | undefined {
  const command = "/skill";
  if (!value.startsWith(command)) {
    return undefined;
  }
  let cursor = command.length;
  if (cursor >= value.length || !isWhitespace(value[cursor])) {
    return undefined;
  }
  while (cursor < value.length && isWhitespace(value[cursor])) {
    cursor += 1;
  }
  if (value[cursor] !== "{") {
    return undefined;
  }
  const end = jsonObjectEnd(value, cursor);
  if (end === undefined) {
    return undefined;
  }
  let identity: unknown;
  try {
    identity = JSON.parse(value.slice(cursor, end));
  } catch {
    return undefined;
  }
  if (!isSkillIdentity(identity)) {
    return undefined;
  }
  return {
    start: 0,
    end,
    token: value.slice(0, end),
    name: identity.name,
    source: identity.source,
    ...(typeof identity.path === "string" ? { path: identity.path } : {}),
    ...(identity.project ? { project: identity.project } : {}),
  };
}

export function skillReferenceArguments(value: string, reference: DraftSkillReference): string {
  return value.slice(reference.end).replace(/^\s+/, "");
}

export function skillReferenceTitle(reference: DraftSkillReference): string {
  return [reference.source, reference.project?.path || reference.path].filter(Boolean).join(" · ");
}

function isSkillIdentity(value: unknown): value is {
  name: string;
  source: string;
  path?: unknown;
  project?: SkillProjectIdentity;
} {
  if (!value || typeof value !== "object" || Array.isArray(value)) {
    return false;
  }
  const identity = value as Record<string, unknown>;
  if (typeof identity.name !== "string" || !identity.name || typeof identity.source !== "string" || !identity.source) {
    return false;
  }
  if (identity.path !== undefined && typeof identity.path !== "string") {
    return false;
  }
  if (identity.project !== undefined) {
    if (!identity.project || typeof identity.project !== "object" || Array.isArray(identity.project)) {
      return false;
    }
    const project = identity.project as Record<string, unknown>;
    if (typeof project.root !== "string" || typeof project.path !== "string") {
      return false;
    }
  }
  return true;
}

function jsonObjectEnd(value: string, start: number): number | undefined {
  let depth = 0;
  let inString = false;
  let escaped = false;
  for (let index = start; index < value.length; index += 1) {
    const character = value[index];
    if (inString) {
      if (escaped) {
        escaped = false;
      } else if (character === "\\") {
        escaped = true;
      } else if (character === "\"") {
        inString = false;
      }
      continue;
    }
    if (character === "\"") {
      inString = true;
    } else if (character === "{") {
      depth += 1;
    } else if (character === "}") {
      depth -= 1;
      if (depth === 0) {
        return index + 1;
      }
    }
  }
  return undefined;
}

function isWhitespace(value: string): boolean {
  return /\s/.test(value);
}
