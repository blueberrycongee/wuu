export type Scalar = string | number | boolean;
export type Value = Scalar | Record<string, Scalar>[];
export type State = Record<string, Value>;
export type Expr = Scalar | { get: string } | { item: string } | { op: "add" | "sub" | "mul" | "div" | "eq" | "gt" | "and" | "or" | "not" | "if" | "length"; args: Expr[] };
export type ScalarField = { type: "string" | "number" | "boolean"; initial: Scalar; min?: number; max?: number; maxLength?: number; options?: string[] };
export type Field = (ScalarField | { type: "list"; initial: Record<string, Scalar>[]; item: Record<string, ScalarField>; maxItems: number }) & { scope: "persistent" | "temporary" };
type NodeBase = { id: string; visible?: Expr };
type Binding = { field: string; itemField?: never } | { itemField: string; field?: never };
export type Node = NodeBase & (
  | { type: "stack" | "row" | "card"; children: Node[] }
  | { type: "repeat"; field: string; children: Node[] }
  | { type: "text"; value: Expr }
  | ({ type: "input" | "textarea" | "checkbox" | "slider" | "select"; label: string } & Binding)
  | ({ type: "button"; label: string } & ({ action: string; local?: never; field?: never } | { local: "append" | "remove"; field: string; action?: never }))
);
export type Spec = { schemaVersion: 1; title: string; fallbackText: string; fields: Record<string, Field>; tree: Node; actions: { id: string; kind: "save" | "submit"; label: string }[] };
export const LIMITS = { bytes: 65536, nodes: 200, depth: 12, fields: 32, rows: 50, expressions: 1000, expressionDepth: 8 } as const;
const names = /^[a-zA-Z][a-zA-Z0-9_-]{0,63}$/;
const unsafe = new Set(["__proto__", "prototype", "constructor"]);
export function invalid(message: string): never { throw new Error(`invalid_genui: ${message}`); }
export function record(value: unknown): Record<string, unknown> {
  if (!value || typeof value !== "object" || Array.isArray(value) || ![null, Object.prototype].includes(Object.getPrototypeOf(value))) invalid("expected plain object");
  return value as Record<string, unknown>;
}
export function exact(value: Record<string, unknown>, keys: string[]): void {
  for (const key of Object.keys(value)) if (unsafe.has(key) || !keys.includes(key)) invalid(`unknown field ${key}`);
}
export function identifier(value: unknown): string {
  if (typeof value !== "string" || !names.test(value) || unsafe.has(value)) invalid("invalid identifier");
  return value;
}
export function boundedJSON(value: unknown, bytes: number = LIMITS.bytes): string {
  let count = 0;
  const visit = (v: unknown, depth: number): void => {
    if (++count > 10000 || depth > 32) invalid("JSON complexity exceeded");
    if (v === null || typeof v === "boolean" || typeof v === "string") return;
    if (typeof v === "number" && Number.isFinite(v)) return;
    if (Array.isArray(v)) { for (const item of v) visit(item, depth + 1); return; }
    const object = record(v);
    for (const [key, item] of Object.entries(object)) { if (unsafe.has(key)) invalid("unsafe key"); visit(item, depth + 1); }
  };
  visit(value, 0);
  const json = JSON.stringify(value);
  if (new TextEncoder().encode(json).length > bytes) invalid("size exceeded");
  return json;
}
function text(value: unknown, max: number): string {
  if (typeof value !== "string" || !value.trim() || value.length > max) invalid("expected bounded text");
  return value;
}
function scalar(field: ScalarField, value: unknown): void {
  if (typeof value !== field.type) invalid(`expected ${field.type}`);
  if (typeof value === "number" && (!Number.isFinite(value) || value < (field.min ?? -1e9) || value > (field.max ?? 1e9))) invalid("number out of range");
  if (typeof value === "string" && (value.length > (field.maxLength ?? 1000) || (field.options && !field.options.includes(value)))) invalid("string out of range");
}
function scalarDefinition(input: unknown): ScalarField {
  const f = record(input); exact(f, ["type", "initial", "min", "max", "maxLength", "options"]);
  if (!["string", "number", "boolean"].includes(f.type as string)) invalid("unknown scalar type");
  for (const k of ["min", "max", "maxLength"]) if (f[k] !== undefined && (typeof f[k] !== "number" || !Number.isFinite(f[k]))) invalid("invalid bound");
  if (f.type !== "number" && (f.min !== undefined || f.max !== undefined)) invalid("numeric bounds on non-number");
  if (f.type !== "string" && (f.maxLength !== undefined || f.options !== undefined)) invalid("text bounds on non-string");
  if ((f.min as number ?? -1e9) > (f.max as number ?? 1e9) || (f.maxLength !== undefined && (!Number.isInteger(f.maxLength) || (f.maxLength as number) < 1 || (f.maxLength as number) > 4000))) invalid("invalid bounds");
  if (f.options !== undefined && (!Array.isArray(f.options) || !f.options.length || f.options.length > 30 || f.options.some(v => typeof v !== "string" || v.length > 100) || new Set(f.options).size !== f.options.length)) invalid("invalid options");
  scalar(f as ScalarField, f.initial); return f as ScalarField;
}
export function initialState(spec: Spec, saved: State = {}): State {
  return Object.fromEntries(Object.entries(spec.fields).map(([name, f]) => [name, structuredClone(saved[name] ?? f.initial)]));
}
export function validateState(spec: Spec, input: unknown): State {
  boundedJSON(input); const state = record(input); exact(state, Object.keys(spec.fields));
  for (const [key, f] of Object.entries(spec.fields)) {
    if (!(key in state)) { if (f.scope === "temporary") continue; invalid(`missing state ${key}`); }
    if (f.type !== "list") { scalar(f, state[key]); continue; }
    const rows = state[key]; if (!Array.isArray(rows) || rows.length > f.maxItems) invalid("list out of range");
    for (const row of rows) {
      const item = record(row); exact(item, Object.keys(f.item));
      for (const [name, itemField] of Object.entries(f.item)) scalar(itemField, item[name]);
    }
  }
  return structuredClone(state) as State;
}
export function persistentState(spec: Spec, state: State): State {
  return Object.fromEntries(Object.entries(state).filter(([key]) => spec.fields[key].scope === "persistent"));
}
const arity: Record<string, number> = { add: 2, sub: 2, mul: 2, div: 2, eq: 2, gt: 2, and: 2, or: 2, not: 1, if: 3, length: 1 };
function checkExpr(value: unknown, spec: Spec, item: Record<string, ScalarField> | undefined, budget: { remaining: number }, depth = 0): Field["type"] {
  if (--budget.remaining < 0 || depth > LIMITS.expressionDepth) invalid("expression complexity exceeded");
  if (typeof value === "string") return "string";
  if (typeof value === "number") return "number";
  if (typeof value === "boolean") return "boolean";
  const expr = record(value);
  if ("get" in expr) { exact(expr, ["get"]); const key = identifier(expr.get); if (!Object.hasOwn(spec.fields, key)) invalid("unknown state binding"); return spec.fields[key].type; }
  if ("item" in expr) { exact(expr, ["item"]); const key = identifier(expr.item); if (!item || !Object.hasOwn(item, key)) invalid("unknown item binding"); return item[key].type; }
  exact(expr, ["op", "args"]);
  if (typeof expr.op !== "string" || !Object.hasOwn(arity, expr.op) || !Array.isArray(expr.args) || expr.args.length !== arity[expr.op]) invalid("unknown expression");
  const types = expr.args.map(arg => checkExpr(arg, spec, item, budget, depth + 1));
  if (expr.op === "if") { if (types[0] !== "boolean" || types[1] !== types[2]) invalid("incompatible conditional expression"); return types[1]; }
  if (["and", "or", "not"].includes(expr.op)) { if (types.some(type => type !== "boolean")) invalid("expected boolean expression"); return "boolean"; }
  if (expr.op === "length") { if (!["string", "list"].includes(types[0])) invalid("expected text or list"); return "number"; }
  if (expr.op === "eq") { if (types[0] !== types[1] || types[0] === "list") invalid("incompatible comparison"); return "boolean"; }
  if (types.some(type => type !== "number")) invalid("expected numeric expression");
  return expr.op === "gt" ? "boolean" : "number";
}
export function evaluate(expr: Expr, state: State, item?: Record<string, Scalar>, depth = 0): Value {
  if (depth > LIMITS.expressionDepth) invalid("expression depth exceeded");
  if (typeof expr !== "object") return expr;
  if ("get" in expr) { identifier(expr.get); if (!Object.hasOwn(state, expr.get)) invalid("missing value"); return state[expr.get]; }
  if ("item" in expr) { identifier(expr.item); if (!item || !Object.hasOwn(item, expr.item)) invalid("missing item"); return item[expr.item]; }
  if (!Object.hasOwn(arity, expr.op) || expr.args.length !== arity[expr.op]) invalid("unknown expression");
  const ev = (i: number) => evaluate(expr.args[i], state, item, depth + 1);
  const bool = (v: Value) => { if (typeof v !== "boolean") invalid("expected boolean expression"); return v; };
  const num = (v: Value) => { if (typeof v !== "number" || !Number.isFinite(v)) invalid("expected numeric expression"); return v; };
  const a = ev(0);
  if (expr.op === "if") return ev(bool(a) ? 1 : 2);
  if (expr.op === "and") return bool(a) && bool(ev(1));
  if (expr.op === "or") return bool(a) || bool(ev(1));
  if (expr.op === "not") return !bool(a);
  if (expr.op === "length") { if (typeof a !== "string" && !Array.isArray(a)) invalid("expected list or text"); return a.length; }
  const b = ev(1); if (expr.op === "eq") return a === b;
  const x = num(a), y = num(b);
  if (expr.op === "gt") return x > y;
  const result = expr.op === "add" ? x + y : expr.op === "sub" ? x - y : expr.op === "mul" ? x * y : y !== 0 ? x / y : NaN;
  if (!Number.isFinite(result) || Math.abs(result) > 1e12) invalid("calculation out of range");
  return result;
}
export function validateSpec(input: unknown): Spec {
  boundedJSON(input); const s = record(input); exact(s, ["schemaVersion", "title", "fallbackText", "fields", "tree", "actions"]);
  if (s.schemaVersion !== 1) invalid("unsupported schema version"); text(s.title, 160); text(s.fallbackText, 4000);
  const fields = record(s.fields); if (Object.keys(fields).length > LIMITS.fields) invalid("too many fields");
  for (const [key, value] of Object.entries(fields)) {
    identifier(key); const f = record(value), { scope, ...definition } = f;
    if (scope !== "persistent" && scope !== "temporary") invalid("invalid state scope");
    if (f.type !== "list") { scalarDefinition(definition); continue; }
    exact(definition, ["type", "initial", "item", "maxItems"]);
    if (!Number.isInteger(f.maxItems) || (f.maxItems as number) < 1 || (f.maxItems as number) > LIMITS.rows) invalid("invalid list bound");
    const item = record(f.item); if (!Object.keys(item).length || Object.keys(item).length > 8) invalid("invalid row fields");
    for (const [name, itemField] of Object.entries(item)) { identifier(name); scalarDefinition(itemField); }
  }
  const spec = s as Spec;
  validateState(spec, initialState(spec));
  if (!Array.isArray(s.actions) || s.actions.length > 8) invalid("invalid actions");
  const actions = new Set<string>();
  for (const value of s.actions) {
    const a = record(value); exact(a, ["id", "kind", "label"]); identifier(a.id); text(a.label, 100);
    if (!["save", "submit"].includes(a.kind as string) || actions.has(a.id as string)) invalid("invalid action"); actions.add(a.id as string);
  }
  let count = 0, expandedCount = 0; const ids = new Set<string>(), budget = { remaining: LIMITS.expressions };
  const visit = (value: unknown, depth: number, repeated?: string, multiplier = 1): void => {
    if (++count > LIMITS.nodes || depth > LIMITS.depth || (expandedCount += multiplier) > 1000) invalid("tree complexity exceeded");
    const n = record(value); identifier(n.id); if (ids.has(n.id as string)) invalid("duplicate node id"); ids.add(n.id as string);
    const type = n.type; const common = ["id", "type", "visible"];
    const item = repeated ? (spec.fields[repeated] as Extract<Field, { type: "list" }>).item : undefined;
    if (n.visible !== undefined && checkExpr(n.visible, spec, item, budget) !== "boolean") invalid("visibility must be boolean");
    if (["stack", "row", "card", "repeat"].includes(type as string)) {
      exact(n, [...common, "children", ...(type === "repeat" ? ["field"] : [])]);
      if (!Array.isArray(n.children) || n.children.length > 50) invalid("invalid children");
      if (type === "repeat") { const f = spec.fields[identifier(n.field)]; if (repeated || f?.type !== "list") invalid("invalid repeat"); repeated = n.field as string; multiplier *= f.maxItems; }
      for (const child of n.children) visit(child, depth + 1, repeated, multiplier); return;
    }
    if (type === "text") { exact(n, [...common, "value"]); if (checkExpr(n.value, spec, item, budget) === "list") invalid("text requires a scalar expression"); return; }
    if (type === "button") {
      exact(n, [...common, "label", "action", "local", "field"]); text(n.label, 100);
      if (n.action !== undefined) { if (n.local !== undefined || n.field !== undefined || !actions.has(n.action as string)) invalid("unknown action binding"); }
      else { const f = spec.fields[identifier(n.field)]; if (f?.type !== "list" || !["append", "remove"].includes(n.local as string) || (n.local === "remove" && repeated !== n.field)) invalid("invalid list action"); }
      return;
    }
    if (!["input", "textarea", "checkbox", "slider", "select"].includes(type as string)) invalid("unknown component");
    exact(n, [...common, "label", "field", "itemField"]); text(n.label, 160);
    if ((n.field === undefined) === (n.itemField === undefined)) invalid("one binding required");
    const f = n.itemField !== undefined ? item?.[identifier(n.itemField)] : spec.fields[identifier(n.field)];
    if (!f || f.type === "list" || (type === "checkbox" ? f.type !== "boolean" : type === "slider" ? f.type !== "number" || f.min === undefined || f.max === undefined : f.type !== "string") || (type === "select" && !f.options)) invalid("incompatible control binding");
  };
  visit(s.tree, 0); return structuredClone(spec);
}
