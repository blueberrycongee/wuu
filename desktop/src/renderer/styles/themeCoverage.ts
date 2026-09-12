import { readdirSync } from "node:fs";
import { resolve } from "node:path";

import {
  PUBLIC_SYNTAX_TOKEN_NAMES,
  PUBLIC_THEME_TOKEN_NAMES,
} from "../../shared/themeContract.generated";

/*
 * Theme token coverage analyzer.
 *
 * Builds the custom-property dependency graph of the host stylesheets and
 * checks every color-kind paint declaration: each custom property referenced
 * in its value must either be a public --wuu-* token (the contract set) or
 * have a resolution chain, through its own definitions' var() references,
 * that reaches one.
 *
 * Methodology constraints (must stay in sync with the audit and the test):
 * - Multiple definitions of a variable are a UNION: if any definition's
 *   chain reaches a token, the variable counts as covered.
 * - Shorthand paint properties count as their color family
 *   (border -> border-color family, outline -> outline-color family, ...).
 * - Variables that resolve to geometry/coordination values (lengths,
 *   durations, z-index, timing, stagger indices) are not violations; only
 *   color-kind paint is checked. Variables with no definition inside the
 *   stylesheets (JS inline styles, shadow DOM) are out of scope.
 * - Literal colors written directly in paint declarations are styleAudit's
 *   concern, not this analyzer's.
 */

export interface CssFile {
  name: string;
  source: string;
}

export interface Violation {
  file: string;
  line: number;
  selector: string;
  prop: string;
  variable: string;
}

export const BASELINE_FILE = "themeCoverage.baseline.txt";

const PUBLIC_TOKENS = new Set<string>([...PUBLIC_THEME_TOKEN_NAMES, ...PUBLIC_SYNTAX_TOKEN_NAMES]);
const isPublicToken = (name: string): boolean => PUBLIC_TOKENS.has(name);

/* ------------------------------------------------------------------ */
/* CSS scanning: declarations with 1-based line numbers                */
/* ------------------------------------------------------------------ */

interface Declaration {
  prop: string;
  value: string;
  line: number;
}

/** A declaration with the header text of the rule block that directly contains it. */
export interface ScopedDeclaration {
  selector: string;
  declaration: Declaration;
}

export function scanScopedDeclarations(source: string): ScopedDeclaration[] {
  const scoped: ScopedDeclaration[] = [];
  let pos = 0;
  let line = 1;

  const skipWhitespaceAndComments = (): void => {
    for (;;) {
      if (pos >= source.length) return;
      const ch = source[pos];
      if (ch === "\n") {
        pos += 1;
        line += 1;
        continue;
      }
      if (ch === " " || ch === "\t" || ch === "\r") {
        pos += 1;
        continue;
      }
      if (ch === "/" && source[pos + 1] === "*") {
        pos += 2;
        while (pos < source.length && !(source[pos] === "*" && source[pos + 1] === "/")) {
          if (source[pos] === "\n") line += 1;
          pos += 1;
        }
        pos += 2;
        continue;
      }
      return;
    }
  };

  const skipQuoted = (quote: string): void => {
    pos += 1;
    while (pos < source.length) {
      const ch = source[pos];
      if (ch === "\\") {
        pos += 2;
        continue;
      }
      if (ch === "\n") line += 1;
      pos += 1;
      if (ch === quote) return;
    }
  };

  /** Read raw text until one of `stops` at paren/bracket depth zero. */
  const readUntil = (stops: readonly string[]): string => {
    const start = pos;
    let depth = 0;
    while (pos < source.length) {
      const ch = source[pos];
      if (ch === "\n") line += 1;
      if (ch === '"' || ch === "'") {
        skipQuoted(ch);
        continue;
      }
      if (ch === "(" || ch === "[") depth += 1;
      if (ch === ")" || ch === "]") depth = Math.max(0, depth - 1);
      if (depth === 0 && stops.includes(ch)) break;
      pos += 1;
    }
    return source.slice(start, pos);
  };

  /** Read a declaration value up to `;` or `}` at depth zero. */
  const readValue = (): string => {
    const start = pos;
    let depth = 0;
    while (pos < source.length) {
      const ch = source[pos];
      if (ch === "\n") line += 1;
      if (ch === '"' || ch === "'") {
        skipQuoted(ch);
        continue;
      }
      if (ch === "(" || ch === "[") depth += 1;
      if (ch === ")" || ch === "]") depth = Math.max(0, depth - 1);
      if (depth === 0 && (ch === ";" || ch === "}" || ch === "{")) break;
      pos += 1;
    }
    const raw = source.slice(start, pos);
    return raw.replace(/\/\*[\s\S]*?\*\//g, " ").trim();
  };

  const parseBlock = (selector: string): void => {
    for (;;) {
      skipWhitespaceAndComments();
      if (pos >= source.length) return;
      const ch = source[pos];
      if (ch === "}") {
        pos += 1;
        return;
      }
      if (ch === "@") {
        const atRule = readUntil(["{", ";"]);
        if (pos >= source.length) return;
        if (source[pos] === "{") {
          pos += 1;
          parseBlock(atRule.trim());
        } else {
          pos += 1;
        }
        continue;
      }
      const propLine = line;
      const beforeColon = readUntil([":", "{", ";", "}"]);
      if (pos >= source.length) return;
      const delim = source[pos];
      if (delim === "{") {
        pos += 1;
        parseBlock(beforeColon.trim());
        continue;
      }
      if (delim === ";") {
        pos += 1;
        continue;
      }
      if (delim === "}") {
        pos += 1;
        return;
      }
      const prop = beforeColon.trim().toLowerCase();
      pos += 1; // skip the ':' separating property from value
      const value = readValue();
      if (pos < source.length && source[pos] === ";") pos += 1;
      else if (pos < source.length && source[pos] === "{") {
        // invalid CSS (nested rule after a colon); skip the block
        pos += 1;
        parseBlock(selector);
        continue;
      }
      if (prop.length > 0) scoped.push({ selector, declaration: { prop, value, line: propLine } });
    }
  };

  for (;;) {
    skipWhitespaceAndComments();
    if (pos >= source.length) break;
    if (source[pos] === "@") {
      const atRule = readUntil(["{", ";"]);
      if (pos >= source.length) break;
      if (source[pos] === "{") {
        pos += 1;
        parseBlock(atRule.trim());
      } else {
        pos += 1;
      }
      continue;
    }
    const header = readUntil(["{", ";"]);
    if (pos >= source.length) break;
    if (source[pos] === "{") {
      pos += 1;
      parseBlock(header.trim());
    } else {
      pos += 1;
    }
  }

  return scoped;
}

export function scanDeclarations(source: string): Declaration[] {
  return scanScopedDeclarations(source).map((scoped) => scoped.declaration);
}

/* ------------------------------------------------------------------ */
/* var() reference extraction (handles nesting and quoted strings)     */
/* ------------------------------------------------------------------ */

interface VarCall {
  name: string | null;
  start: number;
  comma: number;
  close: number;
}

export function findVarCalls(value: string): VarCall[] {
  const starts: number[] = [];
  let i = 0;
  while (i < value.length) {
    const ch = value[i];
    if (ch === '"' || ch === "'") {
      const quote = ch;
      i += 1;
      while (i < value.length) {
        if (value[i] === "\\") {
          i += 2;
          continue;
        }
        if (value[i] === quote) break;
        i += 1;
      }
      i += 1;
      continue;
    }
    if (value.startsWith("var(", i)) {
      starts.push(i);
      i += 4;
      continue;
    }
    i += 1;
  }

  return starts.map((start) => {
    let depth = 1;
    let nameStart = -1;
    let nameEnd = -1;
    let comma = -1;
    let close = -1;
    let j = start + 4;
    for (; j < value.length; j += 1) {
      const c = value[j];
      if (c === '"' || c === "'") {
        const quote = c;
        j += 1;
        while (j < value.length) {
          if (value[j] === "\\") {
            j += 2;
            continue;
          }
          if (value[j] === quote) break;
          j += 1;
        }
        continue;
      }
      if (c === "(") depth += 1;
      else if (c === ")") {
        depth -= 1;
        if (depth === 0) {
          close = j;
          break;
        }
      } else if (depth === 1 && nameStart < 0 && !/\s/.test(c)) {
        nameStart = j;
        let k = j;
        while (k < value.length && /[^,()\s]/.test(value[k])) k += 1;
        nameEnd = k;
        j = k - 1;
      } else if (depth === 1 && comma < 0 && nameEnd >= 0 && c === ",") {
        comma = j;
      }
    }
    const name = nameStart >= 0 ? value.slice(nameStart, nameEnd) : null;
    return { name, start, comma, close };
  });
}

function varRefs(value: string): string[] {
  return findVarCalls(value)
    .map((call) => call.name)
    .filter((name): name is string => name !== null && name.startsWith("--"));
}

/* ------------------------------------------------------------------ */
/* Paint property families                                             */
/* ------------------------------------------------------------------ */

const COLOR_PAINT_PROPS = new Set([
  "color",
  "background",
  "background-color",
  "background-image",
  "border",
  "border-top",
  "border-right",
  "border-bottom",
  "border-left",
  "border-block",
  "border-inline",
  "border-block-start",
  "border-block-end",
  "border-inline-start",
  "border-inline-end",
  "border-color",
  "border-top-color",
  "border-right-color",
  "border-bottom-color",
  "border-left-color",
  "border-block-color",
  "border-inline-color",
  "border-block-start-color",
  "border-block-end-color",
  "border-inline-start-color",
  "border-inline-end-color",
  "border-image",
  "border-image-source",
  "outline",
  "outline-color",
  "fill",
  "stroke",
  "stop-color",
  "box-shadow",
  "text-shadow",
  "caret-color",
  "accent-color",
  "text-decoration",
  "text-decoration-color",
  "column-rule",
  "column-rule-color",
  "-webkit-text-fill-color",
  "text-emphasis-color",
]);

function isColorPaint(prop: string): boolean {
  const base = prop.startsWith("-") ? prop.replace(/^-\w+-/, "") : prop;
  return COLOR_PAINT_PROPS.has(base) || COLOR_PAINT_PROPS.has(prop);
}

/* ------------------------------------------------------------------ */
/* Dependency graph analysis                                           */
/* ------------------------------------------------------------------ */

const COLOR_LITERAL =
  /#[0-9a-fA-F]{3,8}\b|(?:rgba?|hsla?)\([^)]*\)|\b(?:transparent|currentColor|black|blue|gray|grey|green|red|white)\b/;

interface Resolver {
  reachable: (name: string, seen?: Set<string>) => boolean;
  isColorKind: (name: string, seen?: Set<string>) => boolean;
  reachingTokens: (name: string, seen?: Set<string>) => string[];
}

function createResolver(defs: Map<string, string[]>): Resolver {
  const reachMemo = new Map<string, boolean>();
  const kindMemo = new Map<string, boolean>();
  const tokenMemo = new Map<string, string[]>();

  const reachable = (name: string, seen: Set<string> = new Set()): boolean => {
    if (isPublicToken(name)) return true;
    const cached = reachMemo.get(name);
    if (cached !== undefined) return cached;
    const list = defs.get(name);
    if (!list || seen.has(name)) return false;
    seen.add(name);
    let ok = false;
    for (const value of list) {
      for (const ref of varRefs(value)) {
        if (isPublicToken(ref) || reachable(ref, seen)) {
          ok = true;
          break;
        }
      }
      if (ok) break;
    }
    seen.delete(name);
    reachMemo.set(name, ok);
    return ok;
  };

  const isColorKind = (name: string, seen: Set<string> = new Set()): boolean => {
    if (isPublicToken(name)) return true;
    const cached = kindMemo.get(name);
    if (cached !== undefined) return cached;
    const list = defs.get(name);
    if (!list || seen.has(name)) return false;
    seen.add(name);
    let color = false;
    for (const value of list) {
      if (COLOR_LITERAL.test(value)) {
        color = true;
        break;
      }
      for (const ref of varRefs(value)) {
        if (isColorKind(ref, seen)) {
          color = true;
          break;
        }
      }
      if (color) break;
    }
    seen.delete(name);
    kindMemo.set(name, color);
    return color;
  };

  const reachingTokens = (name: string, seen: Set<string> = new Set()): string[] => {
    const cached = tokenMemo.get(name);
    if (cached !== undefined) return cached;
    const list = defs.get(name);
    if (isPublicToken(name)) {
      // A public token is its own knob; its definitions may reach further
      // public tokens (legacy aliases, syntax slot bridges).
      const tokens = new Set<string>([name]);
      if (list && !seen.has(name)) {
        seen.add(name);
        for (const value of list) {
          for (const ref of varRefs(value)) {
            for (const token of reachingTokens(ref, seen)) tokens.add(token);
          }
        }
        seen.delete(name);
      }
      const sorted = [...tokens].sort();
      tokenMemo.set(name, sorted);
      return sorted;
    }
    if (!list || seen.has(name)) return [];
    seen.add(name);
    const tokens = new Set<string>();
    for (const value of list) {
      for (const ref of varRefs(value)) {
        for (const token of reachingTokens(ref, seen)) tokens.add(token);
      }
    }
    seen.delete(name);
    const sorted = [...tokens].sort();
    tokenMemo.set(name, sorted);
    return sorted;
  };

  return { reachable, isColorKind, reachingTokens };
}

export function analyzeCoverage(files: CssFile[]): Violation[] {
  const defs = new Map<string, string[]>();
  const paintDecls: Array<{
    file: string;
    line: number;
    selector: string;
    prop: string;
    value: string;
  }> = [];

  for (const file of files) {
    for (const scoped of scanScopedDeclarations(file.source)) {
      const declaration = scoped.declaration;
      if (declaration.prop.startsWith("--")) {
        const list = defs.get(declaration.prop) ?? [];
        list.push(declaration.value);
        defs.set(declaration.prop, list);
      } else if (isColorPaint(declaration.prop)) {
        paintDecls.push({
          file: file.name,
          line: declaration.line,
          selector: normalizeSelector(scoped.selector),
          prop: declaration.prop,
          value: declaration.value,
        });
      }
    }
  }

  const { reachable, isColorKind } = createResolver(defs);

  const violations: Violation[] = [];
  for (const paint of paintDecls) {
    const seenInDecl = new Set<string>();
    for (const ref of varRefs(paint.value)) {
      if (seenInDecl.has(ref)) continue;
      seenInDecl.add(ref);
      if (isPublicToken(ref)) continue;
      if (!defs.has(ref)) continue; // defined outside the stylesheets: out of scope
      if (reachable(ref)) continue;
      if (!isColorKind(ref)) continue; // geometry / coordination variable
      violations.push({
        file: paint.file,
        line: paint.line,
        selector: paint.selector,
        prop: paint.prop,
        variable: ref,
      });
    }
  }
  return violations;
}

/* ------------------------------------------------------------------ */
/* Baseline rendering                                                  */
/* ------------------------------------------------------------------ */

export function sortViolations(violations: Violation[]): Violation[] {
  return [...violations].sort(
    (a, b) =>
      a.file.localeCompare(b.file) ||
      a.selector.localeCompare(b.selector) ||
      a.variable.localeCompare(b.variable) ||
      a.prop.localeCompare(b.prop) ||
      a.line - b.line,
  );
}

export function formatViolation(violation: Violation): string {
  return `${violation.file} ${violation.selector} ${violation.prop} ${violation.variable}`;
}

export function formatBaseline(files: CssFile[]): string[] {
  return sortViolations(analyzeCoverage(files)).map(formatViolation);
}

/* ------------------------------------------------------------------ */
/* Surface matrix: anchor x state x paint property                     */
/* ------------------------------------------------------------------ */

export type SurfaceState = "default" | "hover" | "selected" | "disabled";

export function normalizeSelector(selector: string): string {
  return selector.replace(/\s+/g, " ").trim();
}

const ANCHOR_ATTR = /\[data-wuu-component=(?:"([^"]+)"|'([^']+)')\]/;

/** The first data-wuu-component anchor referenced by the selector, if any. */
export function anchorOf(selector: string): string | null {
  const match = selector.match(ANCHOR_ATTR);
  return match ? (match[1] ?? match[2]) : null;
}

/** State derived from the selector's pseudo-classes and state attributes. */
export function stateOf(selector: string): SurfaceState {
  if (/:hover(?![a-zA-Z-])/.test(selector)) return "hover";
  if (/:active(?![a-zA-Z-])|\[aria-selected|\[data-state=/.test(selector)) return "selected";
  if (/:disabled(?![a-zA-Z-])|\[aria-disabled/.test(selector)) return "disabled";
  return "default";
}

/** data-wuu-component anchor names found in the given sources (sorted, unique). */
export function extractAnchorsFromSources(sources: string[]): string[] {
  const anchors = new Set<string>();
  const pattern = /data-wuu-component=(?:"([^"]+)"|'([^']+)')/g;
  for (const source of sources) {
    for (const match of source.matchAll(pattern)) {
      anchors.add(match[1] ?? match[2]);
    }
  }
  return [...anchors].sort();
}

/** Production .ts/.tsx files under a directory (test files excluded), sorted. */
export function listProductionSources(dir: string): string[] {
  const out: string[] = [];
  const entries = readdirSync(dir, { withFileTypes: true }).sort((a, b) =>
    a.name.localeCompare(b.name),
  );
  for (const entry of entries) {
    const full = resolve(dir, entry.name);
    if (entry.isDirectory()) {
      out.push(...listProductionSources(full));
    } else if (
      (entry.name.endsWith(".ts") || entry.name.endsWith(".tsx")) &&
      !/\.test\.(ts|tsx)$/.test(entry.name)
    ) {
      out.push(full);
    }
  }
  return out;
}

export interface MatrixRow {
  anchor: string;
  state: SurfaceState;
  file: string;
  selector: string;
  prop: string;
  variable: string;
  status: "bridged" | "unbridged";
  tokens: string[];
}

export interface SurfaceMatrix {
  schemaVersion: 2;
  generatedBy: string;
  anchors: Array<{ name: string; synthetic: boolean; rows: number }>;
  tokenSet: string[];
  rows: MatrixRow[];
}

/**
 * Builds the anchor x state x paint-property matrix from the stylesheets.
 *
 * Each row is one var() reference of a color-kind paint declaration: the
 * variable is either a public token (status bridged), a defined private
 * variable with at least one reaching public token (bridged), or a defined
 * private variable with no reaching token (unbridged - exactly the U1
 * baseline entries). Declarations whose selector does not reference a
 * data-wuu-component anchor fall into the synthetic `unanchored:<file>`
 * bucket. Geometry-only and undefined variables are excluded.
 */
export function analyzeSurfaceMatrix(
  files: CssFile[],
  anchorSources: string[],
): SurfaceMatrix {
  const defs = new Map<string, string[]>();
  const scopedPaint: Array<{
    file: string;
    selector: string;
    line: number;
    prop: string;
    value: string;
  }> = [];

  for (const file of files) {
    for (const scoped of scanScopedDeclarations(file.source)) {
      if (scoped.declaration.prop.startsWith("--")) {
        const list = defs.get(scoped.declaration.prop) ?? [];
        list.push(scoped.declaration.value);
        defs.set(scoped.declaration.prop, list);
      } else if (isColorPaint(scoped.declaration.prop)) {
        scopedPaint.push({
          file: file.name,
          selector: scoped.selector,
          line: scoped.declaration.line,
          prop: scoped.declaration.prop,
          value: scoped.declaration.value,
        });
      }
    }
  }

  const { isColorKind, reachingTokens } = createResolver(defs);

  const rows: MatrixRow[] = [];
  for (const paint of scopedPaint) {
    const anchor = anchorOf(paint.selector) ?? `unanchored:${paint.file}`;
    const state = stateOf(paint.selector);
    const seenInDecl = new Set<string>();
    for (const ref of varRefs(paint.value)) {
      if (seenInDecl.has(ref)) continue;
      seenInDecl.add(ref);
      if (isPublicToken(ref)) {
        rows.push({
          anchor,
          state,
          file: paint.file,
          selector: normalizeSelector(paint.selector),
          prop: paint.prop,
          variable: ref,
          status: "bridged",
          tokens: reachingTokens(ref),
        });
        continue;
      }
      if (!defs.has(ref)) continue; // defined outside the stylesheets: out of scope
      if (!isColorKind(ref)) continue; // geometry / coordination variable
      const tokens = reachingTokens(ref);
      rows.push({
        anchor,
        state,
        file: paint.file,
        selector: normalizeSelector(paint.selector),
        prop: paint.prop,
        variable: ref,
        status: tokens.length > 0 ? "bridged" : "unbridged",
        tokens,
      });
    }
  }

  rows.sort(
    (a, b) =>
      a.anchor.localeCompare(b.anchor) ||
      a.state.localeCompare(b.state) ||
      a.file.localeCompare(b.file) ||
      a.selector.localeCompare(b.selector) ||
      a.prop.localeCompare(b.prop) ||
      a.variable.localeCompare(b.variable),
  );

  const anchorSet = new Set<string>();
  for (const source of anchorSources) {
    for (const anchor of extractAnchorsFromSources([source])) anchorSet.add(anchor);
  }
  const rowCountByAnchor = new Map<string, number>();
  for (const row of rows) {
    rowCountByAnchor.set(row.anchor, (rowCountByAnchor.get(row.anchor) ?? 0) + 1);
  }
  const anchors = [...anchorSet]
    .sort()
    .map((name) => ({ name, synthetic: false, rows: rowCountByAnchor.get(name) ?? 0 }));
  for (const name of [...rowCountByAnchor.keys()].sort()) {
    if (!anchorSet.has(name)) {
      anchors.push({ name, synthetic: true, rows: rowCountByAnchor.get(name) ?? 0 });
    }
  }

  const tokenSet = [...new Set(rows.flatMap((row) => row.tokens))].sort();

  return {
    schemaVersion: 2,
    generatedBy: "scripts/generate-theme-surface-matrix.ts",
    anchors,
    tokenSet,
    rows,
  };
}
