// Cross-runtime bundle contract. Keep these rules byte-identical with the Go
// implementation in internal/pluginhost/bundle: a bundle's generation must be
// the same string no matter which runtime computes it.

export const BUNDLE_MANIFEST_SCHEMA_VERSION = 2;
export const BUNDLE_CONTRACT_VERSION = 1;

export type CanonicalValue =
  | null
  | boolean
  | string
  | number
  | CanonicalValue[]
  | { [key: string]: CanonicalValue };

export interface AgentDeclaration {
  command: string;
  args?: string[];
  env?: Record<string, string>;
}

export interface DesktopDeclaration {
  entry: string;
}

/** A package dependency; omitted version accepts any installed version. */
export interface PluginDependency {
  id: string;
  /** Semantic-version constraint checked by the host during activation. */
  version?: string;
  /** Missing or incompatible optional dependencies do not block activation. */
  optional?: boolean;
}

export interface BundleManifest {
  schema_version: number;
  id: string;
  version: string;
  name?: string;
  description?: string;
  agent?: AgentDeclaration;
  desktop?: DesktopDeclaration;
  requires?: string[];
  dependencies?: PluginDependency[];
}

export interface GenerationInput {
  contract_version: number;
  manifest: CanonicalValue;
  content: Record<string, string>;
}

const encoder = new TextEncoder();

export function validateBundleManifest(manifest: unknown): string[] {
  const errors: string[] = [];
  if (typeof manifest !== "object" || manifest === null) {
    errors.push("manifest must be an object");
    return errors;
  }
  const m = manifest as Record<string, unknown>;
  if (m.schema_version !== BUNDLE_MANIFEST_SCHEMA_VERSION) {
    errors.push(`schema_version must be ${BUNDLE_MANIFEST_SCHEMA_VERSION}`);
  }
  if (typeof m.id !== "string" || m.id.trim() === "") {
    errors.push("manifest.id is required (string)");
  } else if (containsNul(m.id) || !isAscii(m.id)) {
    errors.push("manifest.id must be ASCII without NUL");
  }
  if (typeof m.version !== "string" || m.version.trim() === "") {
    errors.push("manifest.version is required (string)");
  }
  const agent = m.agent as AgentDeclaration | undefined;
  const desktop = m.desktop as DesktopDeclaration | undefined;
  if (!agent && !desktop) {
    errors.push("manifest must declare at least one of agent or desktop");
  }
  if (agent !== undefined) {
    if (typeof agent !== "object" || agent === null || typeof agent.command !== "string" || agent.command.trim() === "") {
      errors.push("manifest.agent.command is required when agent is declared");
    } else if (containsNul(agent.command) || !isAscii(agent.command)) {
      errors.push("manifest.agent.command must be ASCII without NUL");
    }
    for (const arg of agent.args ?? []) {
      if (containsNul(arg) || !isAscii(arg)) errors.push("manifest.agent.args must be ASCII without NUL");
    }
    for (const [key, value] of Object.entries(agent.env ?? {})) {
      if (containsNul(key) || !isAscii(key)) errors.push("manifest.agent.env keys must be ASCII without NUL");
      if (containsNul(value) || !isAscii(value)) errors.push("manifest.agent.env values must be ASCII without NUL");
    }
  }
  if (desktop !== undefined) {
    if (typeof desktop !== "object" || desktop === null || typeof desktop.entry !== "string" || desktop.entry.trim() === "") {
      errors.push("manifest.desktop.entry is required when desktop is declared");
    } else if (!isRelativePath(desktop.entry)) {
      errors.push("manifest.desktop.entry must be package-relative");
    }
  }
  return errors;
}

export function canonicalize(value: CanonicalValue): Uint8Array {
  const bytes: number[] = [];
  appendValue(bytes, value);
  return Uint8Array.from(bytes);
}

export async function generation(
  input: GenerationInput,
  sha256Hex: (bytes: Uint8Array) => string | Promise<string>,
): Promise<string> {
  const doc: CanonicalValue = {
    contract_version: input.contract_version,
    manifest: input.manifest,
    content: input.content,
  };
  return await sha256Hex(canonicalize(doc));
}

function appendValue(dst: number[], value: CanonicalValue): void {
  if (value === null) {
    pushAscii(dst, "null");
    return;
  }
  switch (typeof value) {
    case "boolean":
      pushAscii(dst, value ? "true" : "false");
      return;
    case "number":
      if (!Number.isSafeInteger(value)) {
        throw new Error(`bundle: canonical number must be a safe integer, got ${value}`);
      }
      pushAscii(dst, String(value));
      return;
    case "string":
      appendString(dst, value);
      return;
    case "object":
      if (Array.isArray(value)) {
        dst.push(0x5b); // [
        for (let i = 0; i < value.length; i++) {
          if (i > 0) dst.push(0x2c); // ,
          appendValue(dst, value[i]);
        }
        dst.push(0x5d); // ]
        return;
      }
      appendObject(dst, value as Record<string, CanonicalValue>);
      return;
    default:
      throw new Error(`bundle: unsupported canonical value type ${typeof value}`);
  }
}

function appendObject(dst: number[], value: Record<string, CanonicalValue>): void {
  const keys = Object.keys(value)
    .filter((key) => isIncludedObjectValue(value[key]))
    .sort();
  dst.push(0x7b); // {
  for (let i = 0; i < keys.length; i++) {
    if (i > 0) dst.push(0x2c); // ,
    appendString(dst, keys[i]);
    dst.push(0x3a); // :
    appendValue(dst, value[keys[i]]);
  }
  dst.push(0x7d); // }
}

function isIncludedObjectValue(value: CanonicalValue): boolean {
  if (value === null) return false;
  if (typeof value === "string") return value !== "";
  if (Array.isArray(value)) return value.length > 0;
  if (typeof value === "object") return Object.keys(value).length > 0;
  return true;
}

function appendString(dst: number[], value: string): void {
  dst.push(0x22); // "
  const bytes = encoder.encode(value);
  for (const b of bytes) {
    switch (b) {
      case 0x22: dst.push(0x5c, 0x22); break; // \"
      case 0x5c: dst.push(0x5c, 0x5c); break; // \\
      case 0x08: dst.push(0x5c, 0x62); break; // \b
      case 0x0c: dst.push(0x5c, 0x66); break; // \f
      case 0x0a: dst.push(0x5c, 0x6e); break; // \n
      case 0x0d: dst.push(0x5c, 0x72); break; // \r
      case 0x09: dst.push(0x5c, 0x74); break; // \t
      default:
        if (b < 0x20) {
          dst.push(0x5c, 0x75); // \u
          pushAscii(dst, b.toString(16).padStart(4, "0"));
        } else {
          dst.push(b);
        }
    }
  }
  dst.push(0x22); // "
}

function pushAscii(dst: number[], value: string): void {
  for (let i = 0; i < value.length; i++) {
    dst.push(value.charCodeAt(i));
  }
}

function containsNul(value: string): boolean {
  return value.includes("\u0000");
}

function isAscii(value: string): boolean {
  for (let i = 0; i < value.length; i++) {
    if (value.charCodeAt(i) > 0x7f) return false;
  }
  return true;
}

function isRelativePath(value: string): boolean {
  const trimmed = value.trim();
  if (trimmed === "") return false;
  if (trimmed.startsWith("/")) return false;
  const cleaned = cleanSlashPath(trimmed);
  return cleaned !== ".." && !cleaned.startsWith("../");
}

function cleanSlashPath(value: string): string {
  const segments = value.split("/").filter((segment) => segment !== "" && segment !== ".");
  const out: string[] = [];
  for (const segment of segments) {
    if (segment === "..") {
      if (out.length > 0 && out[out.length - 1] !== "..") {
        out.pop();
      } else {
        out.push("..");
      }
    } else {
      out.push(segment);
    }
  }
  return out.join("/");
}
