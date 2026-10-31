import { parse, parseExpression } from "@babel/parser";

const forbidden = new Set(["__proto__", "prototype", "constructor"]);
const allowed = new Set(("File Program VariableDeclaration VariableDeclarator Identifier ArrayPattern ObjectPattern ObjectProperty RestElement AssignmentPattern FunctionDeclaration BlockStatement ReturnStatement IfStatement ExpressionStatement EmptyStatement ArrowFunctionExpression FunctionExpression NumericLiteral StringLiteral BooleanLiteral NullLiteral ArrayExpression ObjectExpression SpreadElement UnaryExpression BinaryExpression LogicalExpression ConditionalExpression MemberExpression OptionalMemberExpression CallExpression OptionalCallExpression TemplateLiteral TemplateElement AssignmentExpression JSXElement JSXFragment JSXOpeningElement JSXClosingElement JSXOpeningFragment JSXClosingFragment JSXIdentifier JSXAttribute JSXExpressionContainer JSXEmptyExpression JSXText").split(" "));
const parserOptions = { sourceType: "module", plugins: ["jsx"] };
const encoder = new TextEncoder();
function check(ok, message) { if (!ok) throw new Error(message); }
function safeKey(key) { check(typeof key === "string" || Number.isInteger(key), "Expected a property name"); key = String(key); check(!forbidden.has(key), `Property ${key} is unavailable`); return key; }
function primitive(value) {
  check(value == null || ["number", "string", "boolean"].includes(typeof value), "Convert only scalar values to text or numbers");
  return value;
}
function bounded(value) {
  if (typeof value === "number") check(Number.isFinite(value) && Math.abs(value) <= 1e12, "Calculation is outside the numeric range");
  if (typeof value === "string") check(value.length <= 16384, "Text exceeds 16 KiB");
  if (Array.isArray(value)) check(value.length <= 200, "Collections support at most 200 items");
  return value;
}
export function validateSourceState(values) {
  let count = 0;
  function visit(value, depth) {
    check(++count <= 4096 && depth <= 16, "State is too complex");
    bounded(value);
    if (value === null || ["string", "number", "boolean"].includes(typeof value)) return;
    check(value && typeof value === "object", "State must contain JSON values");
    for (const [key, child] of Object.entries(value)) { safeKey(key); visit(child, depth + 1); }
  }
  visit(values, 0);
  check(encoder.encode(JSON.stringify(values)).length <= 16384, "State exceeds 16 KiB");
  return values;
}

// Scan one expression without treating braces in strings or comments as markup.
function expressionEnd(text, start) {
  let depth = 1, quote = "", comment = "";
  for (let i = start + 1; i < text.length; i++) {
    const c = text[i], next = text[i + 1];
    if (comment === "line") { if (c === "\n") comment = ""; continue; }
    if (comment === "block") { if (c === "*" && next === "/") { comment = ""; i++; } continue; }
    if (quote) { if (c === "\\") i++; else if (c === quote) quote = ""; continue; }
    if (c === "/" && next === "/") { comment = "line"; i++; }
    else if (c === "/" && next === "*") { comment = "block"; i++; }
    else if (c === '"' || c === "'" || c === "`") quote = c;
    else if (c === "{") depth++;
    else if (c === "}" && --depth === 0) return i + 1;
  }
  return -1;
}

// Only the preview tree is repaired. The final document always parses strictly.
// Complete openings retain their source offsets while text arrives inside them.
function previewMarkup(text) {
  const stack = [];
  let end = 0;
  for (let i = 0; i < text.length;) {
    if (text[i] === "{") {
      const next = expressionEnd(text, i);
      if (next < 0) break;
      i = next; end = i; continue;
    }
    if (text[i] !== "<") { end = ++i; continue; }
    const start = i;
    let quote = "", closed = false;
    for (i++; i < text.length; i++) {
      const c = text[i];
      if (quote) { if (c === quote) quote = ""; continue; }
      if (c === '"' || c === "'") { quote = c; continue; }
      if (c === "{") { const next = expressionEnd(text, i); if (next < 0) break; i = next - 1; continue; }
      if (c === ">") { i++; closed = true; break; }
    }
    if (!closed) break;
    const token = text.slice(start, i);
    const match = /^<(\/)?([A-Za-z][A-Za-z0-9]*|)(?:\s|\/?>)/.exec(token);
    check(match, "Malformed component opening");
    if (match[1]) check(stack.pop() === match[2], "Mismatched component closing");
    else if (!token.endsWith("/>")) stack.push(match[2]);
    end = i;
  }
  return text.slice(0, end) + stack.reverse().map(tag => `</${tag}>`).join("");
}

export function compileSource(source, preview = false) {
  check(typeof source === "string" && encoder.encode(source).length <= 120 * 1024, "Source exceeds 120 KiB");
  let body = "", markup = source.trimStart();
  if (markup.startsWith("{@body")) {
    let end = expressionEnd(markup, 0);
    const parsesBody = candidate => {
      try { parse("{" + markup.slice(6, candidate), parserOptions); return true; }
      catch { return false; }
    };
    // JSX text can contain quotes, and nested template literals contain braces.
    // Let the language parser resolve those cases instead of guessing a boundary.
    if (end < 0 || !parsesBody(end)) {
      end = -1;
      let parsedBytes = 0;
      for (let candidate = markup.indexOf("}", 6); candidate >= 0; candidate = markup.indexOf("}", candidate + 1)) {
        parsedBytes += candidate;
        check(parsedBytes <= 4 * 1024 * 1024, "Declaration parsing budget exceeded");
        if (parsesBody(candidate + 1)) { end = candidate + 1; break; }
      }
    }
    if (end < 0 && preview) return { declarations: [], tree: null };
    check(end > 0, "Close a valid {@body ...} declaration block");
    body = markup.slice(6, end - 1); markup = markup.slice(end);
  }
  if (preview) markup = previewMarkup(markup);
  const declarations = parse(body, parserOptions).program.body;
  const tree = parseExpression(`<>${markup}</>`, parserOptions);
  let count = 0;
  function inspect(node, depth = 0) {
    if (!node || typeof node !== "object") return;
    if (Array.isArray(node)) { for (const child of node) inspect(child, depth); return; }
    if (!node.type) return;
    check(++count <= 4096 && depth <= 80, "Source is too complex");
    check(allowed.has(node.type), `Unsupported syntax: ${node.type}`);
    if (node.type === "Identifier") safeKey(node.name);
    check(!node.async && !node.generator, "Async and generator functions are unavailable");
    if (node.type === "VariableDeclaration") check(["const", "let"].includes(node.kind), "Declare variables with const or let");
    for (const [key, child] of Object.entries(node)) if (!["loc", "extra", "comments", "leadingComments", "trailingComments", "innerComments"].includes(key)) inspect(child, depth + 1);
  }
  inspect(declarations); inspect(tree);
  return { declarations, tree };
}

// The interpreter never evaluates generated JavaScript in the host realm.
// Functions are capabilities created here; property lookup cannot reveal host APIs.
export function evaluateSource(compiled, saved = {}, options = {}) {
  const values = Object.create(null), defaults = Object.create(null), calls = new WeakSet();
  let remaining = 30000, depth = 0, inEvent = false, nodeCount = 0;
  let assignedBindings;
  class Scope {
    constructor(parent = null) { this.parent = parent; this.bindings = new Map(); }
    has(name) { return this.bindings.has(name) || Boolean(this.parent?.has(name)); }
    get(name) { return this.bindings.has(name) ? this.bindings.get(name).value : this.parent?.get(name); }
    set(name, value, mutable = false) { this.bindings.set(name, { value, mutable }); }
    assign(name, value) {
      const binding = this.bindings.get(name);
      if (!binding) { check(this.parent, `Unknown name: ${name}`); return this.parent.assign(name, value); }
      check(binding.mutable, `Cannot assign to constant ${name}`);
      if (inEvent && !assignedBindings.has(binding)) assignedBindings.set(binding, binding.value);
      binding.value = value;
    }
  }
  const root = new Scope();
  function tick() { check(--remaining >= 0, "Calculation budget exceeded"); }
  function capability(fn) { calls.add(fn); return fn; }
  function call(fn, args) { tick(); check(calls.has(fn), "This value is not callable"); check(++depth <= 48, "Function nesting limit reached"); try { return bounded(fn(...args)); } finally { depth--; } }
  function lookup(env, name) { check(env.has(name), `Unknown name: ${name}`); return env.get(name); }
  function bind(pattern, value, env, mutable = true) {
    if (pattern.type === "Identifier") { env.set(safeKey(pattern.name), value, mutable); return; }
    if (pattern.type === "AssignmentPattern") { bind(pattern.left, value === undefined ? expr(pattern.right, env) : value, env, mutable); return; }
    if (pattern.type === "ArrayPattern") { check(Array.isArray(value), "Expected an array to unpack"); pattern.elements.forEach((p, i) => { if (p) bind(p, value[i], env, mutable); }); return; }
    if (pattern.type === "ObjectPattern") { for (const p of pattern.properties) { check(p.type === "ObjectProperty", "Object rest is unavailable"); bind(p.value, member(value, p.computed ? expr(p.key, env) : p.key.name ?? p.key.value), env, mutable); } return; }
    throw new Error("Unsupported binding");
  }
  function member(value, key) {
    key = safeKey(key);
    check(value !== null && value !== undefined, `Cannot read ${key} from an empty value`);
    if (Array.isArray(value)) {
      if (key === "length") return value.length;
      if (/^(0|[1-9]\d*)$/.test(key)) return value[Number(key)];
      if (["map", "filter", "find", "some", "every"].includes(key)) return capability(fn => value[key]((item, i) => call(fn, [item, i])));
      if (key === "reduce") return capability((fn, ...initial) => value.reduce((acc, item, i) => call(fn, [acc, item, i]), ...initial));
      if (key === "join") return capability((separator = ",") => { value.forEach(primitive); return bounded(value.join(primitive(separator))); });
      if (["slice", "includes", "indexOf"].includes(key)) return capability((...args) => bounded(value[key](...args.map(primitive))));
    }
    if (typeof value === "string") {
      if (key === "length") return value.length;
      if (["slice", "substring", "includes", "startsWith", "endsWith", "indexOf", "trim", "toLowerCase", "toUpperCase", "split"].includes(key)) return capability((...args) => bounded(value[key](...args.map(primitive))));
    }
    if (typeof value === "number" && key === "toFixed") return capability((digits = 0) => { check(Number.isInteger(digits) && digits >= 0 && digits <= 8, "Precision must be 0–8"); return value.toFixed(digits); });
    check(typeof value === "object" && !Array.isArray(value), `Property ${key} is unavailable`);
    return Object.hasOwn(value, key) ? value[key] : undefined;
  }
  const math = Object.create(null);
  for (const name of ["abs", "min", "max", "round", "floor", "ceil", "sqrt", "pow", "sin", "cos", "tan", "atan2", "log", "exp", "sign"]) math[name] = capability((...args) => { check(args.every(x => typeof x === "number"), "Math expects numbers"); return bounded(Math[name](...args)); });
  math.PI = Math.PI; math.E = Math.E;
  root.set("Math", math); root.set("undefined", undefined);
  root.set("Number", capability(value => bounded(Number(primitive(value)))));
  root.set("String", capability(value => bounded(String(primitive(value)))));
  root.set("Boolean", capability(value => Boolean(value)));
  root.set("DIL", Object.assign(Object.create(null), {
    range: capability((start, end, step = 1) => { check([start, end, step].every(Number.isFinite) && step !== 0, "Invalid range"); const length = Math.max(0, Math.ceil((end - start) / step)); check(length <= 200, "Range supports at most 200 items"); return Array.from({ length }, (_, i) => start + i * step); }),
    format: capability((value, style = "number", currency = "USD") => { check(typeof value === "number" && ["number", "percent", "currency"].includes(style), "Invalid format"); return new Intl.NumberFormat(undefined, { ...(style === "number" ? {} : { style }), ...(style === "currency" ? { currency } : {}), maximumFractionDigits: 2 }).format(value); }),
    sendMessage: capability(text => { check(inEvent && typeof text === "string" && text.trim() && text.length <= 4000, "Send a nonempty message from a button or form"); options.effect?.("sendMessage", text); }),
    copy: capability(text => { check(inEvent && typeof text === "string" && text.length <= 16000, "Copy text from an interaction"); options.effect?.("copy", text); }),
  }));
  function closure(node, env) {
    return capability((...args) => {
      const local = new Scope(env);
      node.params.forEach((p, i) => bind(p, args[i], local));
      return node.body.type === "BlockStatement" ? statements(node.body.body, local)?.value : expr(node.body, local);
    });
  }
  function statements(nodes, env, top = false) {
    for (const node of nodes) if (node.type === "FunctionDeclaration") env.set(node.id.name, closure(node, env));
    for (const node of nodes) {
      tick();
      if (node.type === "VariableDeclaration") {
        for (const decl of node.declarations) {
          const init = decl.init;
          const hook = init?.type === "CallExpression" && init.callee.type === "MemberExpression" && init.callee.object.name === "DIL" && init.callee.property.name === "useState" && !init.callee.computed;
          if (hook) {
            check(top && decl.id.type === "ArrayPattern" && decl.id.elements.length === 2 && decl.id.elements.every(p => p?.type === "Identifier") && init.arguments.length === 1, "Use const [value, setValue] = DIL.useState(initial) in the body");
            const [valueName, setterName] = decl.id.elements.map(p => p.name);
            check(!Object.hasOwn(values, valueName), "Duplicate state name");
            const initial = expr(init.arguments[0], env); defaults[valueName] = initial;
            values[valueName] = Object.hasOwn(saved, valueName) ? saved[valueName] : initial;
            env.set(valueName, values[valueName]);
            env.set(setterName, capability(next => { check(inEvent, "State can only change during an interaction"); values[valueName] = typeof next === "function" ? call(next, [values[valueName]]) : next; }));
          } else bind(decl.id, init ? expr(init, env) : undefined, env, node.kind !== "const");
        }
      } else if (node.type === "FunctionDeclaration") continue;
      else if (node.type === "ReturnStatement") return { value: node.argument ? expr(node.argument, env) : undefined };
      else if (node.type === "IfStatement") { const branch = expr(node.test, env) ? node.consequent : node.alternate; if (branch) { const result = statements(branch.type === "BlockStatement" ? branch.body : [branch], new Scope(env)); if (result) return result; } }
      else if (node.type === "BlockStatement") { const result = statements(node.body, new Scope(env)); if (result) return result; }
      else if (node.type === "ExpressionStatement") expr(node.expression, env);
      else check(node.type === "EmptyStatement", `Unsupported statement: ${node.type}`);
    }
  }
  function argumentsFor(nodes, env) {
    const args = [];
    for (const node of nodes) {
      if (node.type === "SpreadElement") { const value = expr(node.argument, env); check(Array.isArray(value), "Spread expects an array"); args.push(...value); }
      else args.push(expr(node, env));
    }
    return args;
  }
  const shortCircuited = Symbol();
  function optionalChain(node, env) {
    tick();
    const receiver = node.type === "OptionalMemberExpression" ? node.object : node.callee;
    // Only adjacent optional-chain nodes share a short circuit. Parentheses
    // finish the chain, so a following ordinary property access can still fail.
    const value = ["OptionalMemberExpression", "OptionalCallExpression"].includes(receiver.type) && !receiver.extra?.parenthesized
      ? optionalChain(receiver, env) : expr(receiver, env);
    if (value === shortCircuited || node.optional && value == null) return shortCircuited;
    return node.type === "OptionalMemberExpression"
      ? member(value, node.computed ? expr(node.property, env) : node.property.name)
      : call(value, argumentsFor(node.arguments, env));
  }
  function expr(node, env) {
    tick();
    check(node, "Missing expression");
    let result;
    switch (node.type) {
      case "NumericLiteral": case "StringLiteral": case "BooleanLiteral": return bounded(node.value);
      case "NullLiteral": return null;
      case "Identifier": return lookup(env, node.name);
      case "ArrayExpression": result = []; for (const item of node.elements) { if (item?.type === "SpreadElement") { const items = expr(item.argument, env); check(Array.isArray(items), "Spread expects an array"); result.push(...items); } else result.push(item ? expr(item, env) : null); bounded(result); } return result;
      case "ObjectExpression": result = Object.create(null); for (const p of node.properties) { if (p.type === "SpreadElement") { const value = expr(p.argument, env); check(value && typeof value === "object" && !Array.isArray(value), "Spread expects an object"); for (const [key, child] of Object.entries(value)) result[safeKey(key)] = child; } else result[safeKey(p.computed ? expr(p.key, env) : p.key.name ?? p.key.value)] = expr(p.value, env); } return result;
      case "ArrowFunctionExpression": case "FunctionExpression": return closure(node, env);
      case "MemberExpression": return member(expr(node.object, env), node.computed ? expr(node.property, env) : node.property.name);
      case "CallExpression": return call(expr(node.callee, env), argumentsFor(node.arguments, env));
      case "OptionalMemberExpression": case "OptionalCallExpression": { const value = optionalChain(node, env); return value === shortCircuited ? undefined : value; }
      case "UnaryExpression": { const value = expr(node.argument, env); if (!["!", "typeof"].includes(node.operator)) primitive(value); if (node.operator === "!") return !value; if (node.operator === "-") return bounded(-value); if (node.operator === "+") return bounded(+value); if (node.operator === "typeof") return typeof value; throw new Error("Unsupported unary operator"); }
      case "BinaryExpression": { const a = expr(node.left, env), b = expr(node.right, env); if (!["===", "!=="].includes(node.operator)) { primitive(a); primitive(b); } switch (node.operator) { case "+": return bounded(a + b); case "-": return bounded(a - b); case "*": return bounded(a * b); case "/": return bounded(a / b); case "%": return bounded(a % b); case "**": return bounded(a ** b); case "===": return a === b; case "==": return a == b; case "!==": return a !== b; case "!=": return a != b; case "<": return a < b; case "<=": return a <= b; case ">": return a > b; case ">=": return a >= b; default: throw new Error("Unsupported binary operator"); } }
      case "LogicalExpression": { const a = expr(node.left, env); return node.operator === "&&" ? a && expr(node.right, env) : node.operator === "||" ? a || expr(node.right, env) : a ?? expr(node.right, env); }
      case "ConditionalExpression": return expr(expr(node.test, env) ? node.consequent : node.alternate, env);
      case "TemplateLiteral": result = node.quasis[0].value.cooked; node.expressions.forEach((n, i) => { result = bounded(result + String(primitive(expr(n, env))) + node.quasis[i + 1].value.cooked); }); return result;
      case "AssignmentExpression": check(node.operator === "=" && node.left.type === "Identifier" && env.has(node.left.name), "Assign only declared local variables"); result = expr(node.right, env); env.assign(node.left.name, result); return result;
      case "JSXFragment": return node.children.flatMap(child => jsxChild(child, env));
      case "JSXElement": {
        check(++nodeCount <= 512, "Rendered component limit reached");
        const tag = node.openingElement.name.name;
        const props = Object.create(null);
        for (const p of node.openingElement.attributes) { check(p.type === "JSXAttribute", "Use named component properties"); const name = safeKey(p.name.name); check(!Object.hasOwn(props, name), "Duplicate component property"); props[name] = p.value === null ? true : p.value.type === "JSXExpressionContainer" ? expr(p.value.expression, env) : p.value.value; }
        const children = node.children.flatMap(child => jsxChild(child, env));
        const custom = env.get(tag);
        if (custom) {
          const result = call(custom, [{ ...props, children }]);
          return { tag: "Fragment", props: props.key === undefined ? {} : { key: props.key }, children: [result], address: node.start };
        }
        return { tag, props, children, address: node.start };
      }
      default: throw new Error(`Unsupported expression: ${node.type}`);
    }
  }
  function jsxChild(node, env) {
    if (node.type === "JSXText") { const text = node.value.replace(/\s*\n\s*/g, " "); return text.trim() ? [text] : []; }
    if (node.type === "JSXExpressionContainer") return node.expression.type === "JSXEmptyExpression" ? [] : [expr(node.expression, env)];
    return [expr(node, env)];
  }
  statements(compiled.declarations, root, true);
  validateSourceState(values); validateSourceState(defaults);
  const nodes = compiled.tree ? expr(compiled.tree, root) : [];
  return { nodes, values, defaults, invoke(handler, args = []) {
    const previous = { ...values };
    remaining = 30000; nodeCount = 0; inEvent = true; assignedBindings = new Map();
    try { call(handler, args); validateSourceState(values); return { ...values }; }
    finally {
      // A handler only proposes a snapshot. Rendering/validation can still reject
      // it, and repeated failures need not cause React to render a new evaluator.
      Object.assign(values, previous);
      for (const [binding, value] of assignedBindings) binding.value = value;
      assignedBindings = undefined; inEvent = false; depth = 0;
    }
  } };
}

export function validateSourceSpec(spec) {
  check(spec && typeof spec === "object" && Object.keys(spec).every(key => ["version", "title", "fallback", "source"].includes(key)), "Unknown source spec property");
  check(spec.version === 3 && typeof spec.title === "string" && spec.title.trim() && spec.title.length <= 160, "Expected a version 3 source spec and title");
  check(typeof spec.fallback === "string" && spec.fallback.trim() && spec.fallback.length <= 8000, "Provide a useful text fallback");
  evaluateSource(compileSource(spec.source));
  return spec;
}

export function partialSourceString(raw, start) {
  check(raw[start] === '"', "Expected a source string");
  let end = start + 1;
  for (; end < raw.length; end++) {
    if (raw[end] === '"') return JSON.parse(raw.slice(start, end + 1));
    if (raw[end] === "\\") {
      if (end + 1 >= raw.length || (raw[end + 1] === "u" && end + 6 > raw.length)) break;
      end += raw[end + 1] === "u" ? 5 : 1;
    }
  }
  return JSON.parse(raw.slice(start, end) + '"');
}
