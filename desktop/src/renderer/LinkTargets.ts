export type WorkspaceFileSelection = {
  startLineNumber: number;
  startColumn: number;
  endLineNumber?: number;
  endColumn?: number;
};

export type ExternalLinkTarget = {
  kind: "external";
  url: string;
};

export type WorkspaceFileLinkTarget = {
  kind: "workspace-file";
  path: string;
  selection?: WorkspaceFileSelection;
  anchor?: string;
};

export type AnchorLinkTarget = {
  kind: "anchor";
  id: string;
};

export type InvalidLinkTarget = {
  kind: "invalid";
};

export type LinkTarget =
  | ExternalLinkTarget
  | WorkspaceFileLinkTarget
  | AnchorLinkTarget
  | InvalidLinkTarget;

const EXTERNAL_PROTOCOLS = new Set(["http:", "https:", "mailto:"]);
const BLOCKED_PROTOCOL_PATTERN = /^(?:javascript|data|vbscript):/i;
const URI_SCHEME_PATTERN = /^[a-z][a-z0-9+.-]*:/i;
const WINDOWS_ABSOLUTE_PATH_PATTERN = /^[a-z]:[\\/]/i;

export function parseLinkTarget(value: string | undefined): LinkTarget {
  const raw = value?.trim();
  if (!raw || BLOCKED_PROTOCOL_PATTERN.test(raw)) {
    return { kind: "invalid" };
  }

  if (raw.startsWith("#")) {
    const id = decodeLinkFragment(raw.slice(1));
    return id ? { kind: "anchor", id } : { kind: "invalid" };
  }

  const external = parseExternalTarget(raw);
  if (external) {
    return external;
  }

  if (/^file:/i.test(raw)) {
    return parseFileURLTarget(raw);
  }

  const workspaceTarget = parseWorkspaceFileTarget(raw);
  if (workspaceTarget) {
    return workspaceTarget;
  }

  return { kind: "invalid" };
}

export function parseWorkspaceFileTarget(value: string): WorkspaceFileLinkTarget | undefined {
  // Filesystem callers supply exact names; parseLinkTarget trims link text.
  const raw = value;
  if (!raw || BLOCKED_PROTOCOL_PATTERN.test(raw)) {
    return undefined;
  }

  const hashIndex = raw.indexOf("#");
  const rawPath = hashIndex < 0 ? raw : raw.slice(0, hashIndex);
  const rawFragment = hashIndex < 0 ? undefined : raw.slice(hashIndex + 1);
  let path = rawPath;
  let selection = rawFragment ? parseSelectionFragment(rawFragment) : undefined;
  const anchor = rawFragment && !selection ? decodeLinkFragment(rawFragment) : undefined;

  if (!rawFragment) {
    const legacy = parseLegacyLineSuffix(path);
    if (legacy) {
      path = legacy.path;
      selection = legacy.selection;
    }
  }

  if (!path || (URI_SCHEME_PATTERN.test(path) && !WINDOWS_ABSOLUTE_PATH_PATTERN.test(path))) {
    return undefined;
  }

  return {
    kind: "workspace-file",
    path,
    ...(selection ? { selection } : {}),
    ...(anchor ? { anchor } : {}),
  };
}

export function formatWorkspaceFileTarget(target: WorkspaceFileLinkTarget): string {
  if (target.selection) {
    return `${target.path}#${formatSelectionFragment(target.selection)}`;
  }
  if (target.anchor) {
    return `${target.path}#${encodeURIComponent(target.anchor)}`;
  }
  return target.path;
}

export function resolveWorkspaceFileTarget(
  baseFilePath: string,
  target: WorkspaceFileLinkTarget,
): WorkspaceFileLinkTarget {
  if (
    target.path.startsWith("/") ||
    target.path.startsWith("\\\\") ||
    WINDOWS_ABSOLUTE_PATH_PATTERN.test(target.path)
  ) {
    return target;
  }

  const baseSegments = baseFilePath.replace(/\\/g, "/").split("/");
  baseSegments.pop();
  const segments = [...baseSegments, ...target.path.replace(/\\/g, "/").split("/")];
  const normalized: string[] = [];
  for (const segment of segments) {
    if (!segment || segment === ".") {
      continue;
    }
    if (segment === "..") {
      normalized.pop();
      continue;
    }
    normalized.push(segment);
  }
  return { ...target, path: normalized.join("/") };
}

function parseExternalTarget(value: string): ExternalLinkTarget | undefined {
  let url: URL;
  try {
    url = new URL(value);
  } catch {
    return undefined;
  }
  if (!EXTERNAL_PROTOCOLS.has(url.protocol.toLowerCase())) {
    return undefined;
  }
  return { kind: "external", url: url.toString() };
}

function parseFileURLTarget(value: string): LinkTarget {
  let url: URL;
  try {
    url = new URL(value);
  } catch {
    return { kind: "invalid" };
  }
  if (url.protocol.toLowerCase() !== "file:") {
    return { kind: "invalid" };
  }

  let path: string;
  try {
    const decodedPath = decodeURIComponent(url.pathname);
    path = /^\/[a-z]:\//i.test(decodedPath) ? decodedPath.slice(1) : decodedPath;
    if (url.hostname && url.hostname !== "localhost") {
      path = `//${url.hostname}${path}`;
    }
  } catch {
    return { kind: "invalid" };
  }
  if (!path) {
    return { kind: "invalid" };
  }

  const selection = parseSelectionFragment(url.hash.slice(1));
  const anchor = url.hash && !selection ? decodeLinkFragment(url.hash.slice(1)) : undefined;
  return {
    kind: "workspace-file",
    path,
    ...(selection ? { selection } : {}),
    ...(anchor ? { anchor } : {}),
  };
}

function parseSelectionFragment(fragment: string): WorkspaceFileSelection | undefined {
  const match = /^L?(\d+)(?:,(\d+))?(?:-L?(\d+)(?:,(\d+))?)?$/.exec(fragment);
  if (!match) {
    return undefined;
  }
  return selectionFromMatch(match[1], match[2], match[3], match[4]);
}

function parseLegacyLineSuffix(value: string): {
  path: string;
  selection: WorkspaceFileSelection;
} | undefined {
  const match = /^(.*?):(\d+)(?::(\d+))?(?:-(\d+)(?::(\d+))?)?$/.exec(value);
  if (!match?.[1]) {
    return undefined;
  }
  const selection = selectionFromMatch(match[2], match[3], match[4], match[5]);
  return selection ? { path: match[1], selection } : undefined;
}

function selectionFromMatch(
  startLineValue: string | undefined,
  startColumnValue: string | undefined,
  endLineValue: string | undefined,
  endColumnValue: string | undefined,
): WorkspaceFileSelection | undefined {
  const startLineNumber = Number(startLineValue);
  const startColumn = startColumnValue ? Number(startColumnValue) : 1;
  const endLineNumber = endLineValue ? Number(endLineValue) : undefined;
  const endColumn = endLineValue ? (endColumnValue ? Number(endColumnValue) : 1) : undefined;
  if (
    !Number.isInteger(startLineNumber) ||
    startLineNumber < 1 ||
    !Number.isInteger(startColumn) ||
    startColumn < 1 ||
    (endLineNumber !== undefined && (!Number.isInteger(endLineNumber) || endLineNumber < 1)) ||
    (endColumn !== undefined && (!Number.isInteger(endColumn) || endColumn < 1))
  ) {
    return undefined;
  }
  return {
    startLineNumber,
    startColumn,
    ...(endLineNumber !== undefined ? { endLineNumber } : {}),
    ...(endColumn !== undefined ? { endColumn } : {}),
  };
}

function formatSelectionFragment(selection: WorkspaceFileSelection): string {
  const start = `L${selection.startLineNumber}${selection.startColumn === 1 ? "" : `,${selection.startColumn}`}`;
  if (selection.endLineNumber === undefined) {
    return start;
  }
  const endColumn = selection.endColumn ?? 1;
  return `${start}-L${selection.endLineNumber}${endColumn === 1 ? "" : `,${endColumn}`}`;
}

function decodeLinkFragment(value: string): string {
  try {
    return decodeURIComponent(value);
  } catch {
    return value;
  }
}
