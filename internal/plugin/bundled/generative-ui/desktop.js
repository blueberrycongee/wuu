// Model data is never spread into React props. The fixed vocabulary below is
// the complete rendering and interaction capability of each protocol version.
const MIME = "application/vnd.wuu.ui+json";
const MAX_BYTES = 128 * 1024;
const MAX_STATE_BYTES = 16 * 1024;
const ID = /^[A-Za-z][A-Za-z0-9_-]{0,47}$/;
const encoder = new TextEncoder();
const writes = new Map();
const liveViews = new Map();
let instanceSequence = 0;

function requireValue(condition, message) {
  if (!condition) throw new Error(message);
}
function object(value, keys, required = keys) {
  requireValue(value !== null && typeof value === "object" && !Array.isArray(value), "Expected an object");
  requireValue(Object.keys(value).every(key => keys.includes(key)), "Unknown property");
  requireValue(required.every(key => Object.hasOwn(value, key)), "Missing property");
}
function string(value, max, nonempty = true) {
  requireValue(typeof value === "string" && [...value].length <= max && (!nonempty || value.trim().length > 0), "Invalid text length");
}
function list(value, min, max) {
  requireValue(Array.isArray(value) && value.length >= min && value.length <= max, "Invalid list length");
}
function identifier(value) {
  requireValue(typeof value === "string" && ID.test(value), "Invalid identifier");
}
function number(value) {
  requireValue(typeof value === "number" && Number.isFinite(value) && Math.abs(value) <= 1e12, "Invalid number");
}
function scalar(value) {
  if (typeof value === "string") string(value, 500, false);
  else if (typeof value === "number") number(value);
  else requireValue(value === null || typeof value === "boolean", "Invalid cell value");
}
function unique(values) {
  requireValue(new Set(values).size === values.length, "Duplicate identifier");
}
function fieldValue(field, value) {
  if (field.kind === "text") string(value, 500, false);
  else if (field.kind === "number") number(value);
  else if (field.kind === "checkbox") requireValue(typeof value === "boolean", "Invalid checkbox value");
  else requireValue(field.options.includes(value), "Invalid selection");
}

// Validate historical content again: a stored MIME label is not a trust signal.
export function parseSpec(raw) {
  requireValue(typeof raw === "string" && raw.length <= MAX_BYTES && encoder.encode(raw).length <= MAX_BYTES, "UI exceeds 128 KiB");
  const spec = JSON.parse(raw);
  if (spec.version === 2) return validateProgram(spec);
  object(spec, ["version", "title", "fallback", "blocks"]);
  requireValue(spec.version === 1, "Unsupported UI version");
  string(spec.title, 160);
  string(spec.fallback, 8000);
  list(spec.blocks, 1, 16);
  unique(spec.blocks.map(block => block?.id));
  for (const block of spec.blocks) {
    identifier(block?.id);
    if (block.title !== undefined) string(block.title, 160);
    switch (block.type) {
      case "text":
        object(block, ["id", "type", "text"]);
        string(block.text, 4000);
        break;
      case "table":
        object(block, ["id", "type", "title", "columns", "rows"], ["id", "type", "columns", "rows"]);
        list(block.columns, 1, 8);
        unique(block.columns.map(column => column?.key));
        for (const column of block.columns) {
          object(column, ["key", "label"]);
          identifier(column.key);
          string(column.label, 80);
        }
        list(block.rows, 0, 200);
        for (const row of block.rows) {
          list(row, block.columns.length, block.columns.length);
          row.forEach(scalar);
        }
        break;
      case "chart":
        object(block, ["id", "type", "title", "chartType", "xLabel", "yLabel", "points"], ["id", "type", "chartType", "points"]);
        requireValue(block.chartType === "bar" || block.chartType === "line", "Unsupported chart type");
        for (const key of ["xLabel", "yLabel"]) if (block[key] !== undefined) string(block[key], 80);
        list(block.points, 1, 100);
        for (const point of block.points) {
          object(point, ["label", "value"]);
          string(point.label, 80);
          number(point.value);
        }
        break;
      case "form":
        object(block, ["id", "type", "title", "fields"], ["id", "type", "fields"]);
        list(block.fields, 1, 12);
        unique(block.fields.map(field => field?.id));
        for (const field of block.fields) {
          object(field, ["id", "label", "kind", "options", "initial"], ["id", "label", "kind"]);
          identifier(field.id);
          string(field.label, 80);
          requireValue(["text", "number", "select", "checkbox"].includes(field.kind), "Unsupported field kind");
          if (field.kind === "select") {
            list(field.options, 1, 20);
            field.options.forEach(option => string(option, 80));
            unique(field.options);
          } else requireValue(field.options === undefined, "Options require a select field");
          if (field.initial !== undefined) fieldValue(field, field.initial);
        }
        break;
      default: throw new Error("Unsupported UI block");
    }
  }
  return spec;
}

// Version 2 is a data-only program. No source strings, arbitrary props, URLs,
// or dynamic property access are ever passed to the host or React.
const FORBIDDEN_KEYS = new Set(["__proto__", "constructor", "prototype"]);
const PROGRAM_LIMITS = { nodes: 256, depth: 12, expressions: 2048, expressionDepth: 16, operations: 10000, rendered: 1000, iterations: 100 };
const ARITIES = {
  add: [2, 16], subtract: [2, 2], multiply: [2, 16], divide: [2, 2], mod: [2, 2],
  eq: [2, 2], ne: [2, 2], lt: [2, 2], lte: [2, 2], gt: [2, 2], gte: [2, 2],
  and: [2, 16], or: [2, 16], not: [1, 1], concat: [1, 16], length: [1, 1],
  get: [2, 2], if: [3, 3], round: [1, 2], min: [2, 16], max: [2, 16],
  map: [2, 2], filter: [2, 2], sum: [1, 1], range: [2, 3], toNumber: [1, 1],
  abs: [1, 1], pow: [2, 2], format: [1, 3], array: [0, 16], object: [2, 16],
};
function safeKey(key) {
  string(key, 80);
  requireValue(!FORBIDDEN_KEYS.has(key), "Unsafe property name");
}
function programID(value) { identifier(value); requireValue(!FORBIDDEN_KEYS.has(value), "Unsafe identifier"); }
function jsonValue(value, depth = 1, budget) {
  if (budget) consume(budget);
  requireValue(depth <= 8, "Value is nested too deeply");
  if (value === null || typeof value === "boolean") return;
  if (typeof value === "string") return string(value, 4000, false);
  if (typeof value === "number") return number(value);
  if (Array.isArray(value)) {
    list(value, 0, 200);
    value.forEach(item => jsonValue(item, depth + 1, budget));
    return;
  }
  requireValue(value && typeof value === "object" && Object.getPrototypeOf(value) === Object.prototype, "Invalid JSON value");
  requireValue(Object.keys(value).length <= 64, "Too many object fields");
  for (const [key, item] of Object.entries(value)) { safeKey(key); jsonValue(item, depth + 1, budget); }
}
function programScalar(value) {
  requireValue(value === null || ["string", "number", "boolean"].includes(typeof value), "Expected a scalar value");
  jsonValue(value);
}
function validateProgram(spec) {
  object(spec, ["version", "title", "fallback", "state", "body"]);
  string(spec.title, 160); string(spec.fallback, 8000);
  list(spec.state, 0, 32);
  const states = new Map();
  for (const state of spec.state) {
    object(state, ["id", "value"]); programID(state.id); jsonValue(state.value);
    requireValue(!states.has(state.id), "Duplicate state identifier");
    states.set(state.id, state.value);
  }
  requireValue(encoder.encode(JSON.stringify(spec.state)).length <= MAX_STATE_BYTES, "State exceeds 16 KiB");
  const ids = new Set();
  let expressionCount = 0;
  function expression(expr, context, depth = 1) {
    requireValue(++expressionCount <= PROGRAM_LIMITS.expressions && depth <= PROGRAM_LIMITS.expressionDepth, "Expression budget exceeded");
    if (expr === null || typeof expr !== "object") return programScalar(expr);
    const op = expr.op;
    if (op === "literal") { object(expr, ["op", "value"]); return jsonValue(expr.value); }
    if (op === "state") {
      object(expr, ["op", "key"]); requireValue(states.has(expr.key), "Unknown state reference"); return;
    }
    if (op === "item") {
      object(expr, ["op", "path"], ["op"]); requireValue(context.item, "Item reference outside a collection");
      if (expr.path !== undefined) { list(expr.path, 0, 8); expr.path.forEach(safeKey); } return;
    }
    if (op === "event") { object(expr, ["op"]); requireValue(context.event, "Event reference outside a change handler"); return; }
    requireValue(Object.hasOwn(ARITIES, op), "Unsupported expression");
    object(expr, ["op", "args"]); list(expr.args, ...ARITIES[op]);
    if (op === "object") requireValue(expr.args.length % 2 === 0, "Object requires key and value pairs");
    expr.args.forEach((arg, index) => expression(arg, { ...context, item: context.item || ((op === "map" || op === "filter") && index === 1) }, depth + 1));
  }
  function actions(value, context) {
    list(value, 1, 16);
    let sends = 0, copies = 0;
    for (const action of value) {
      if (action.type === "set") {
        object(action, ["type", "state", "value"]); requireValue(states.has(action.state), "Unknown state assignment"); expression(action.value, context);
      } else if (action.type === "reset") object(action, ["type"]);
      else if (action.type === "copy" || action.type === "sendMessage") {
        object(action, ["type", "text"]); expression(action.text, context);
        if (action.type === "sendMessage") requireValue(!context.event && ++sends <= 1, "Continuation requires one explicit button action");
        else requireValue(++copies <= 1, "Only one clipboard action is allowed");
      } else throw new Error("Unsupported action");
    }
  }
  function nodes(children, context, depth, root = false) {
    list(children, root ? 1 : 0, 64);
    requireValue(children.length === 0 || depth <= PROGRAM_LIMITS.depth, "UI is nested too deeply");
    for (const node of children) {
      requireValue(node && typeof node === "object", "Invalid node"); programID(node.id);
      requireValue(!ids.has(node.id) && ids.size < PROGRAM_LIMITS.nodes, "Duplicate node or node budget exceeded"); ids.add(node.id);
      const check = (keys, required = keys) => object(node, ["id", "type", ...keys], ["id", "type", ...required]);
      const expr = key => { if (Object.hasOwn(node, key)) expression(node[key], context); };
      const binding = () => requireValue(states.has(node.state), "Unknown state binding");
      switch (node.type) {
        case "stack": case "row": case "flow": case "grid": case "card": {
          const extras = node.type === "grid" ? ["columns"] : node.type === "card" ? ["title"] : ["align"];
          check(["children", "gap", ...extras], ["children"]);
          if (node.gap !== undefined) requireValue(["none", "small", "medium", "large"].includes(node.gap), "Invalid layout gap");
          if (node.align !== undefined) requireValue(["start", "center", "end", "stretch"].includes(node.align), "Invalid layout alignment");
          if (node.columns !== undefined) requireValue(Number.isInteger(node.columns) && node.columns >= 1 && node.columns <= 4, "Invalid grid columns");
          expr("title"); nodes(node.children, context, depth + 1); break;
        }
        case "text": case "caption": case "badge": check(["text"]); expr("text"); break;
        case "heading": check(["text", "level"], ["text"]); expr("text"); if (node.level !== undefined) requireValue([1, 2, 3].includes(node.level), "Invalid heading level"); break;
        case "divider": check([]); break;
        case "if": check(["when", "then", "else"], ["when", "then"]); expr("when"); nodes(node.then, context, depth + 1); if (node.else !== undefined) nodes(node.else, context, depth + 1); break;
        case "each": check(["items", "key", "children"], ["items", "children"]); expr("items"); if (node.key !== undefined) expression(node.key, { ...context, item: true }); nodes(node.children, { ...context, item: true }, depth + 1); break;
        case "form":
          check(["label", "children", "onSubmit"]); requireValue(!context.form, "Forms cannot be nested"); expr("label"); actions(node.onSubmit, context); nodes(node.children, { ...context, form: true }, depth + 1); break;
        case "disclosure": check(["title", "children", "open"], ["title", "children"]); expr("title"); if (node.open !== undefined) requireValue(typeof node.open === "boolean", "Invalid disclosure state"); nodes(node.children, context, depth + 1); break;
        case "metric": check(["label", "value", "detail"], ["label", "value"]); expr("label"); expr("value"); expr("detail"); break;
        case "progress": check(["value", "max", "label"], ["value"]); expr("value"); expr("max"); expr("label"); break;
        case "button": check(["label", "onClick", "disabled", "variant"], ["label", "onClick"]); expr("label"); expr("disabled"); if (node.variant !== undefined) requireValue(["primary", "secondary"].includes(node.variant), "Invalid button variant"); actions(node.onClick, context); break;
        case "input": case "textarea": case "checkbox": case "slider": case "select": case "radio": case "segmented": {
          const extras = node.type === "input" ? ["inputType", "placeholder"] : node.type === "textarea" ? ["placeholder"] : node.type === "slider" ? ["min", "max", "step"] : ["select", "radio", "segmented"].includes(node.type) ? ["options"] : [];
          const required = node.type === "slider" ? ["min", "max"] : ["select", "radio", "segmented"].includes(node.type) ? ["options"] : [];
          check(["label", "state", "onChange", "required", "disabled", "invalid", ...extras], ["label", "state", ...required]); binding(); expr("label"); expr("disabled"); expr("invalid");
          if (node.required !== undefined) requireValue(typeof node.required === "boolean", "Invalid required flag");
          if (node.inputType !== undefined) requireValue(["text", "number", "date"].includes(node.inputType), "Unsupported input type");
          if (node.placeholder !== undefined) string(node.placeholder, 500, false);
          if (node.type === "slider") { number(node.min); number(node.max); requireValue(node.min < node.max, "Invalid slider range"); if (node.step !== undefined) { number(node.step); requireValue(node.step > 0, "Invalid slider step"); } }
          if (node.options !== undefined) {
            list(node.options, 1, 20);
            for (const option of node.options) { object(option, ["label", "value"]); string(option.label, 80); programScalar(option.value); }
            unique(node.options.map(option => JSON.stringify(option.value)));
          }
          validateBinding(node, states.get(node.state));
          if (node.onChange !== undefined) actions(node.onChange, { ...context, event: true }); break;
        }
        case "table":
          check(["columns", "rows", "title"], ["columns", "rows"]); expr("title"); expr("rows"); list(node.columns, 1, 8); unique(node.columns.map(column => column?.key));
          for (const column of node.columns) { object(column, ["key", "label"]); programID(column.key); string(column.label, 80); } break;
        case "chart": {
          check(["chartType", "points", "series", "stacked", "title", "xLabel", "yLabel"], ["chartType"]);
          requireValue(["bar", "line", "area", "scatter", "pie", "donut"].includes(node.chartType), "Unsupported chart type");
          requireValue((node.points !== undefined) !== (node.series !== undefined), "Chart requires points or series");
          expr("points"); expr("title");
          for (const key of ["xLabel", "yLabel"]) if (node[key] !== undefined) string(node[key], 80);
          if (node.stacked !== undefined) requireValue(typeof node.stacked === "boolean" && ["bar", "area"].includes(node.chartType), "Stacking requires bar or area charts");
          if (node.series !== undefined) {
            requireValue(!["pie", "donut"].includes(node.chartType), "Circular charts require points"); list(node.series, 1, 8); unique(node.series.map(series => series?.id));
            for (const series of node.series) {
              object(series, ["id", "label", "points", "chartType"], ["id", "label", "points"]); programID(series.id); string(series.label, 80); expression(series.points, context);
              if (series.chartType !== undefined) requireValue(["bar", "line", "area", "scatter"].includes(series.chartType), "Unsupported series type");
              if (node.stacked) requireValue(["bar", "area"].includes(series.chartType ?? node.chartType), "Stacking requires bar or area series");
            }
          }
          break;
        }
        default: throw new Error("Unsupported UI node");
      }
    }
  }
  nodes(spec.body, { item: false, event: false }, 1, true);
  return spec;
}
function validateBinding(node, value) {
  if (node.type === "checkbox") requireValue(typeof value === "boolean", "Checkbox state must be boolean");
  else if (node.options) requireValue(node.options.some(option => option.value === value), "Selection is not an available option");
  else if (node.type === "slider" || node.inputType === "number") {
    number(value);
    if (node.type === "slider") requireValue(value >= node.min && value <= node.max, "Slider value is outside its range");
  } else {
    string(value, 4000, false);
    if (node.inputType === "date" && value !== "") {
      requireValue(/^[0-9]{4}-[0-9]{2}-[0-9]{2}$/.test(value), "Date must use YYYY-MM-DD");
      const [year, month, day] = value.split("-").map(Number);
      const days = [31, year % 4 === 0 && (year % 100 !== 0 || year % 400 === 0) ? 29 : 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31];
      requireValue(year >= 1 && month >= 1 && month <= 12 && day >= 1 && day <= days[month - 1], "Date must be a valid calendar date");
    }
  }
}
function programDefaults(spec) { return Object.fromEntries(spec.state.map(state => [state.id, state.value])); }
function validateProgramValues(spec, values) {
  object(values, spec.state.map(state => state.id));
  Object.values(values).forEach(value => jsonValue(value));
  requireValue(encoder.encode(JSON.stringify(values)).length <= MAX_STATE_BYTES, "State exceeds 16 KiB");
  function walk(nodes) {
    for (const node of nodes) {
      if (node.state) validateBinding(node, values[node.state]);
      for (const key of ["children", "then", "else"]) if (node[key]) walk(node[key]);
    }
  }
  walk(spec.body);
  return values;
}
function componentDefaults(node) {
  if (node.type === "table") return { type: "table", query: "", sort: -1, descending: false };
  if (node.type === "chart") return { type: "chart", hidden: [], dataOpen: false };
  return { type: "disclosure", open: node.open ?? false };
}
function componentCatalog(spec) {
  const catalog = new Map();
  function walk(nodes, ancestors) {
    for (const node of nodes) {
      if (["table", "chart", "disclosure"].includes(node.type)) catalog.set(node.id, { node, ancestors });
      for (const field of ["children", "then", "else"]) if (node[field]) walk(node[field], node.type === "each" ? [...ancestors, node.id] : ancestors);
    }
  }
  walk(spec.body, []);
  return catalog;
}
function validateCollectionIdentity(raw, budget) {
  string(raw, MAX_STATE_BYTES);
  const encoded = JSON.parse(raw); list(encoded, 2, 2);
  requireValue(Number.isInteger(encoded[1]) && encoded[1] >= 0 && encoded[1] < PROGRAM_LIMITS.iterations, "Invalid collection occurrence");
  const identity = JSON.parse(encoded[0]);
  if (identity[0] === "key") {
    list(identity, 3, 3); requireValue(encoded[1] === 0, "Explicit collection keys cannot repeat");
    if (identity[1] === "string") string(identity[2], 80);
    else { requireValue(identity[1] === "number", "Invalid collection key type"); number(identity[2]); }
  } else {
    list(identity, 2, 2); requireValue(identity[0] === "value", "Invalid collection identity");
    const value = JSON.parse(identity[1]); jsonValue(value, 1, budget);
    requireValue(canonicalJSON(value, budget) === identity[1], "Noncanonical collection identity");
  }
  requireValue(JSON.stringify([JSON.stringify(identity), encoded[1]]) === raw, "Noncanonical collection address");
}
function validateComponentRecord(node, record) {
  requireValue(record?.type === node.type, "Saved component type changed");
  if (node.type === "table") {
    object(record, ["type", "query", "sort", "descending"]); string(record.query, 100, false);
    requireValue(Number.isInteger(record.sort) && record.sort >= -1 && record.sort < node.columns.length && typeof record.descending === "boolean", "Invalid saved table controls");
  } else if (node.type === "chart") {
    object(record, ["type", "hidden", "dataOpen"]); list(record.hidden, 0, 100); unique(record.hidden);
    requireValue(typeof record.dataOpen === "boolean", "Invalid chart disclosure state");
    if (node.chartType === "pie" || node.chartType === "donut") {
      for (const identity of record.hidden) {
        string(identity, 2000);
        const parts = JSON.parse(identity); list(parts, 2, 2);
        requireValue(Number.isInteger(parts[1]) && parts[1] >= 0 && parts[1] < 100, "Invalid chart point occurrence");
        const point = JSON.parse(parts[0]); validateChartPoints([point], node.chartType);
        requireValue(canonicalJSON(point, { operations: PROGRAM_LIMITS.operations }) === parts[0] && JSON.stringify(parts) === identity, "Invalid chart point identity");
      }
    } else {
      const ids = new Set((node.series ?? []).map(series => series.id));
      requireValue(record.hidden.every(id => typeof id === "string" && ids.has(id)), "Unknown chart series");
    }
  } else { object(record, ["type", "open"]); requireValue(typeof record.open === "boolean", "Invalid saved disclosure state"); }
}
function programSnapshotPayload(spec, snapshot) {
  // The original program already contains defaults. A small explicit marker
  // can restore them even when the declared-state limit leaves no envelope room.
  return Object.keys(snapshot.components).length === 0 && JSON.stringify(snapshot.values) === JSON.stringify(programDefaults(spec))
    ? { version: 2, defaults: true } : { version: 2, ...snapshot };
}
function validateProgramSnapshot(spec, snapshot, legacy = false) {
  object(snapshot, ["values", "components"]);
  validateProgramValues(spec, snapshot.values);
  requireValue(snapshot.components && typeof snapshot.components === "object" && !Array.isArray(snapshot.components), "Invalid saved component state");
  const catalog = componentCatalog(spec);
  const budget = { operations: PROGRAM_LIMITS.operations };
  for (const [address, record] of Object.entries(snapshot.components)) {
    string(address, MAX_STATE_BYTES);
    const path = JSON.parse(address); list(path, 2, 2);
    const known = catalog.get(path[0]); requireValue(known, "Unknown saved component");
    list(path[1], known.ancestors.length, known.ancestors.length);
    path[1].forEach((ancestor, index) => {
      list(ancestor, 2, 2); requireValue(ancestor[0] === known.ancestors[index], "Saved component ancestry changed");
      validateCollectionIdentity(ancestor[1], budget);
    });
    requireValue(JSON.stringify(path) === address, "Noncanonical component address");
    validateComponentRecord(known.node, record);
  }
  if (!legacy) requireValue(encoder.encode(JSON.stringify(programSnapshotPayload(spec, snapshot))).length <= MAX_STATE_BYTES, "Combined view state exceeds 16 KiB");
  return snapshot;
}
function restoreProgramSnapshot(spec, raw) {
  if (!raw) return { values: programDefaults(spec), components: {} };
  // Old v2 snapshots had no component records and allowed an envelope overhead.
  requireValue(encoder.encode(raw).length <= MAX_STATE_BYTES + 64, "Saved state is too large");
  const saved = JSON.parse(raw);
  if (saved && Object.hasOwn(saved, "defaults")) {
    requireValue(encoder.encode(raw).length <= MAX_STATE_BYTES, "Saved state is too large");
    object(saved, ["version", "defaults"]);
    requireValue(saved.version === 2 && saved.defaults === true, "Invalid defaults marker");
    return { values: programDefaults(spec), components: {} };
  }
  object(saved, ["version", "values", "components"], ["version", "values"]);
  requireValue(saved.version === 2, "Unsupported saved state");
  const legacy = !Object.hasOwn(saved, "components");
  if (!legacy) requireValue(encoder.encode(raw).length <= MAX_STATE_BYTES, "Saved state is too large");
  return validateProgramSnapshot(spec, { values: saved.values, components: saved.components ?? {} }, legacy);
}

function canonicalJSON(value, budget) {
  consume(budget);
  if (value === null || typeof value !== "object") return JSON.stringify(value);
  if (Array.isArray(value)) return `[${value.map(item => canonicalJSON(item, budget)).join(",")}]`;
  return `{${Object.keys(value).sort().map(key => `${JSON.stringify(key)}:${canonicalJSON(value[key], budget)}`).join(",")}}`;
}
function collectionIdentities(node, items, values, scope, budget) {
  const occurrences = new Map();
  return items.map(item => {
    let identity;
    if (node.key !== undefined) {
      const key = evaluateExpression(node.key, values, { ...scope, item }, budget);
      if (typeof key === "string") string(key, 80);
      else number(key);
      identity = JSON.stringify(["key", typeof key, key]);
      requireValue(!occurrences.has(identity), "Collection keys must be unique");
    } else identity = JSON.stringify(["value", canonicalJSON(item, budget)]);
    const ordinal = occurrences.get(identity) ?? 0;
    occurrences.set(identity, ordinal + 1);
    return JSON.stringify([identity, ordinal]);
  });
}
function consume(budget) { requireValue(--budget.operations >= 0, "Local computation limit reached"); }
function valueText(value) {
  if (value === null) return "—";
  requireValue(typeof value !== "object", "Expected text or a scalar value");
  const result = String(value); string(result, 4000, false); return result;
}
function property(value, key) {
  if (value === null || typeof value !== "object") return null;
  requireValue(typeof key === "string" || (Number.isInteger(key) && key >= 0), "Invalid property selector");
  safeKey(String(key));
  return Object.hasOwn(value, key) ? value[key] : null;
}
// Every evaluation shares a render/transaction budget, including nested map and
// filter calls. Results remain JSON; arithmetic never leaks NaN or Infinity.
export function evaluateExpression(expr, values, scope = {}, budget = { operations: PROGRAM_LIMITS.operations }) {
  consume(budget);
  if (expr === null || typeof expr !== "object") return expr;
  if (expr.op === "literal") return expr.value;
  if (expr.op === "state") return values[expr.key];
  if (expr.op === "event") return scope.event;
  if (expr.op === "item") return (expr.path ?? []).reduce(property, scope.item);
  const evaluate = arg => evaluateExpression(arg, values, scope, budget);
  const args = expr.args;
  if (expr.op === "if") return evaluate(args[0]) ? evaluate(args[1]) : evaluate(args[2]);
  if (expr.op === "and") return args.every(arg => Boolean(evaluate(arg)));
  if (expr.op === "or") return args.some(arg => Boolean(evaluate(arg)));
  if (expr.op === "map" || expr.op === "filter") {
    const items = evaluate(args[0]); list(items, 0, PROGRAM_LIMITS.iterations);
    const result = items[expr.op](item => evaluateExpression(args[1], values, { ...scope, item }, budget));
    jsonValue(result, 1, budget); return result;
  }
  const evaluated = args.map(evaluate);
  const [a, b, c] = evaluated;
  const numeric = () => evaluated.forEach(number);
  let result;
  switch (expr.op) {
    case "array": result = evaluated; break;
    case "object": {
      requireValue(evaluated.length % 2 === 0, "Object requires key and value pairs");
      result = {};
      for (let index = 0; index < evaluated.length; index += 2) { safeKey(evaluated[index]); requireValue(!Object.hasOwn(result, evaluated[index]), "Duplicate object key"); result[evaluated[index]] = evaluated[index + 1]; }
      break;
    }
    case "add": numeric(); result = evaluated.reduce((sum, item) => sum + item, 0); break;
    case "subtract": numeric(); result = a - b; break;
    case "multiply": numeric(); result = evaluated.reduce((product, item) => product * item, 1); break;
    case "divide": numeric(); requireValue(b !== 0, "Cannot divide by zero"); result = a / b; break;
    case "mod": numeric(); requireValue(b !== 0, "Cannot divide by zero"); result = a % b; break;
    case "pow": numeric(); result = Math.pow(a, b); break;
    case "abs": numeric(); result = Math.abs(a); break;
    case "min": numeric(); result = Math.min(...evaluated); break;
    case "max": numeric(); result = Math.max(...evaluated); break;
    case "round": numeric(); requireValue(b === undefined || (Number.isInteger(b) && b >= 0 && b <= 8), "Rounding precision must be 0–8"); result = Number(a.toFixed(b ?? 0)); break;
    case "eq": return a === b;
    case "ne": return a !== b;
    case "lt": case "lte": case "gt": case "gte":
      requireValue(typeof a === typeof b && ["number", "string"].includes(typeof a), "Comparison requires matching numbers or text");
      return expr.op === "lt" ? a < b : expr.op === "lte" ? a <= b : expr.op === "gt" ? a > b : a >= b;
    case "not": return !a;
    case "concat": result = evaluated.map(valueText).join(""); break;
    case "length": requireValue(typeof a === "string" || Array.isArray(a), "Length requires text or a list"); return typeof a === "string" ? [...a].length : a.length;
    case "get": return property(a, b);
    case "sum": list(a, 0, 200); a.forEach(number); result = a.reduce((sum, item) => sum + item, 0); break;
    case "toNumber": requireValue(typeof a === "number" || (typeof a === "string" && a.trim() !== ""), "Enter a number"); result = Number(a); break;
    case "range": {
      numeric(); const step = c ?? 1; requireValue(step !== 0, "Range step cannot be zero");
      const size = Math.max(0, Math.ceil((b - a) / step)); requireValue(size <= PROGRAM_LIMITS.iterations, "Range is too large");
      result = Array.from({ length: size }, (_, index) => a + index * step); break;
    }
    case "format": {
      number(a); const style = b ?? "number"; requireValue(["number", "percent", "currency"].includes(style), "Unsupported number format");
      if (style === "currency") requireValue(typeof c === "string" && /^[A-Z]{3}$/.test(c), "Currency requires a three-letter code");
      else requireValue(c === undefined, "Currency code requires currency format");
      result = new Intl.NumberFormat(undefined, style === "currency" ? { style, currency: c } : { style: style === "percent" ? "percent" : "decimal", maximumFractionDigits: 2 }).format(a); break;
    }
    default: throw new Error("Unsupported expression");
  }
  jsonValue(result, 1, budget); return result;
}

// Read only complete JSON values. Braces and commas inside escaped strings do
// not delimit nodes; unfinished strings/objects are never synthetically closed.
function jsonValueEnd(raw, start) {
  let index = start;
  while (/\s/.test(raw[index] ?? "") && index < raw.length) index++;
  const first = raw[index];
  if (first === '"') {
    for (index++; index < raw.length; index++) {
      if (raw[index] === "\\") { index++; continue; }
      if (raw[index] === '"') return index + 1;
    }
    return -1;
  }
  if (first === "{" || first === "[") {
    const stack = [first];
    for (index++; index < raw.length; index++) {
      if (raw[index] === '"') { const end = jsonValueEnd(raw, index); if (end < 0) return -1; index = end - 1; continue; }
      if (raw[index] === "{" || raw[index] === "[") stack.push(raw[index]);
      else if (raw[index] === "}" || raw[index] === "]") {
        requireValue(stack.pop() === (raw[index] === "}" ? "{" : "["), "Malformed JSON prefix");
        if (!stack.length) return index + 1;
      }
    }
    return -1;
  }
  while (index < raw.length && !/[\s,}\]]/.test(raw[index])) index++;
  return index < raw.length ? index : -1;
}
export function parseToolInput(raw) {
  requireValue(typeof raw === "string" && encoder.encode(raw).length <= MAX_BYTES + 1024, "UI input exceeds its budget");
  // The tool takes exactly {spec}. A complete input uses the ordinary validator.
  try {
    const complete = JSON.parse(raw); object(complete, ["spec"]);
    return parseSpec(JSON.stringify(complete.spec));
  } catch (error) {
    if (!(error instanceof SyntaxError)) throw error;
  }
  let index = 0;
  const space = () => { while (index < raw.length && /\s/.test(raw[index])) index++; };
  const token = expected => { space(); requireValue(raw[index++] === expected, "Incomplete UI input"); };
  token("{"); space();
  const outerKeyEnd = jsonValueEnd(raw, index); requireValue(outerKeyEnd > index && JSON.parse(raw.slice(index, outerKeyEnd)) === "spec", "Expected spec input"); index = outerKeyEnd;
  token(":"); token("{");
  const spec = {};
  while (index < raw.length) {
    space(); const keyEnd = jsonValueEnd(raw, index); requireValue(keyEnd > index, "Incomplete property name");
    const key = JSON.parse(raw.slice(index, keyEnd)); requireValue(typeof key === "string" && !Object.hasOwn(spec, key) && !FORBIDDEN_KEYS.has(key), "Duplicate or unsafe property"); index = keyEnd; token(":"); space();
    if (key === "body" || key === "blocks") {
      token("["); const children = [];
      while (index < raw.length) {
        space(); if (raw[index] === "]") break;
        const end = jsonValueEnd(raw, index); if (end < 0) break;
        children.push(JSON.parse(raw.slice(index, end))); index = end; space();
        if (raw[index] === "]") break;
        requireValue(raw[index] === "," || index === raw.length, "Malformed child separator");
        if (raw[index] === ",") index++;
      }
      spec[key] = children;
      return parseSpec(JSON.stringify(spec));
    }
    const end = jsonValueEnd(raw, index); requireValue(end > index, "Incomplete UI header");
    spec[key] = JSON.parse(raw.slice(index, end)); index = end; token(",");
  }
  throw new Error("Waiting for a complete UI section");
}

function defaultValues(spec) {
  return Object.fromEntries(spec.blocks.map(block => [block.id,
    block.type === "table" ? { query: "", sort: -1, descending: false }
      : block.type === "chart" ? { range: "all" }
        : block.type === "form" ? Object.fromEntries(block.fields.map(field => [field.id,
          field.initial ?? (field.kind === "checkbox" ? false : field.kind === "select" ? field.options[0] : "")]))
          : {},
  ]));
}
function restoreValues(spec, raw) {
  if (!raw) return defaultValues(spec);
  requireValue(raw.length <= MAX_STATE_BYTES && encoder.encode(raw).length <= MAX_STATE_BYTES, "Saved state is too large");
  const saved = JSON.parse(raw);
  object(saved, ["version", "values"]);
  requireValue(saved.version === 1, "Unsupported saved state");
  object(saved.values, spec.blocks.map(block => block.id));
  for (const block of spec.blocks) {
    const value = saved.values[block.id];
    if (block.type === "table") {
      object(value, ["query", "sort", "descending"]);
      string(value.query, 100, false);
      requireValue(Number.isInteger(value.sort) && value.sort >= -1 && value.sort < block.columns.length && typeof value.descending === "boolean", "Invalid saved sort");
    } else if (block.type === "chart") {
      object(value, ["range"]);
      requireValue(["all", "10", "25"].includes(value.range), "Invalid saved range");
    } else if (block.type === "form") {
      object(value, block.fields.map(field => field.id));
      for (const field of block.fields) {
        // An empty number input is an editable local draft, not a model value.
        if (field.kind !== "number" || value[field.id] !== "") fieldValue(field, value[field.id]);
      }
    } else object(value, []);
  }
  return saved.values;
}
async function hydrateView(host, key, restore, receive, state) {
  // Concurrent inline and expanded mounts share only this exact artifact key.
  // A late storage read must never overwrite a newer local interaction.
  if (!state.mounted) return;
  const channel = liveViews.get(key) ?? { values: undefined, listeners: new Set() };
  liveViews.set(key, channel);
  const listener = snapshot => receive(restore(JSON.stringify(snapshot)));
  channel.listeners.add(listener);
  state.unsubscribe = () => {
    channel.listeners.delete(listener);
    if (!channel.listeners.size && liveViews.get(key) === channel) liveViews.delete(key);
  };
  state.key = key;
  await writes.get(key)?.catch(() => {});
  const saved = channel.values === undefined ? await host.getStorage(key, "user") : null;
  if (!state.mounted) return;
  const values = restore(channel.values === undefined ? saved : JSON.stringify(channel.values));
  receive(values);
}

function saveValues(host, key, values, version = 1) {
  return writeSnapshot(host, key, { version, values }, MAX_STATE_BYTES + (version === 2 ? 64 : 0));
}
function writeSnapshot(host, key, snapshot, maxBytes = MAX_STATE_BYTES) {
  const raw = JSON.stringify(snapshot);
  if (encoder.encode(raw).length > maxBytes) return Promise.reject(new Error("Combined view state exceeds 16 KiB"));
  const channel = liveViews.get(key);
  if (channel) { channel.values = snapshot; for (const receive of channel.listeners) receive(snapshot); }
  const pending = (writes.get(key) ?? Promise.resolve()).catch(() => {}).then(() => host.setStorage(key, raw, "user"));
  writes.set(key, pending);
  void pending.finally(() => { if (writes.get(key) === pending) writes.delete(key); }).catch(() => {});
  return pending;
}
function display(value) { return value === null ? "—" : String(value); }

export async function activate(api) {
  const { createElement: h, useState, useEffect, useMemo, useRef } = api.react;
  const ui = api.ui;

  function GeneratedUI({ content, metadata, host }) {
    const previous = useRef(null);
    const identity = JSON.stringify([metadata.threadId, metadata.itemId, metadata.partId]);
    if (previous.current?.identity !== identity) previous.current = { identity };
    const preview = content?.preview === true;
    const parsed = useMemo(() => {
      try {
        const spec = preview ? parseToolInput(metadata.toolInput?.argumentsText ?? "") : parseSpec(content?.text);
        if (spec.version === 2) renderProgram(spec, programDefaults(spec), { disabled: true, run: () => {} });
        return { spec, raw: preview ? JSON.stringify(spec) : content.text };
      } catch (error) { return { error: error.message }; }
    }, [preview, content?.text, metadata.toolInput?.argumentsText]);
    if (parsed.spec) previous.current = { identity, preview, ...parsed };
    const valid = parsed.spec ? parsed : (preview || previous.current.preview ? previous.current : {});
    const interrupted = preview && ["failed", "interrupted"].includes(metadata.toolInput?.status);
    return valid.spec
      ? h("div", { className: "plugin-genui-surface" },
        h(valid.spec.version === 2 ? ProgramUI : InteractiveUI, {
          key: `${identity}:${preview ? "preview" : metadata.artifactId + ":" + valid.raw}`, spec: valid.spec, raw: valid.raw, metadata, host, preview: preview || !parsed.spec, stopped: interrupted || (!preview && !parsed.spec),
        }),
        interrupted || (!preview && parsed.error) ? h("p", { role: "status", className: "plugin-genui-notice" }, interrupted ? "Generation stopped. This incomplete preview is read-only." : `The final view could not be validated. ${parsed.error}`) : null)
      : preview ? h("section", { className: "plugin-genui", "data-wuu-component": "generated-ui", "data-genui-preview": "true", "aria-busy": !interrupted }, h("p", { className: "plugin-genui-muted", role: "status" }, interrupted ? "Generation stopped before a complete preview was available." : "Building interactive view…"))
        : h("div", null, h(ui.ErrorState, { title: "Interactive preview unavailable", description: parsed.error }), typeof content?.fallbackText === "string" ? h("p", { className: "plugin-genui-text" }, content.fallbackText) : null);
  }

  function ProgramUI({ spec, raw, metadata, host, preview, stopped }) {
    const defaults = useMemo(() => ({ values: programDefaults(spec), components: {} }), [spec]);
    const [snapshot, setSnapshot] = useState(defaults);
    const [ready, setReady] = useState(false);
    const [notice, setNotice] = useState("");
    const [pending, setPending] = useState(false);
    const [resetVersion, setResetVersion] = useState(0);
    const current = useRef(defaults);
    const persistence = useRef({ mounted: true });
    const pendingRef = useRef(false);
    const surface = useRef(null);
    const instanceId = useRef(null);
    if (instanceId.current === null) instanceId.current = `genui-${++instanceSequence}`;
    const hostRef = useRef(host);
    hostRef.current = host;
    useEffect(() => {
      const state = { mounted: true, key: undefined };
      persistence.current = state;
      if (preview) return () => { state.mounted = false; state.unsubscribe?.(); };
      setReady(false);
      void (async () => {
        try {
          const identity = JSON.stringify([metadata.threadId, metadata.itemId, metadata.partId, metadata.artifactId, raw]);
          const digest = await crypto.subtle.digest("SHA-256", encoder.encode(identity));
          const key = "ui-v2-" + Array.from(new Uint8Array(digest), byte => byte.toString(16).padStart(2, "0")).join("");
          await hydrateView(hostRef.current, key, saved => {
            const restored = restoreProgramSnapshot(spec, saved);
            // Invalid computed results cannot replace a valid initial view.
            renderProgram(spec, restored.values, { disabled: true, run: () => {}, components: restored.components });
            return restored;
          }, restored => { current.current = restored; setSnapshot(restored); }, state);
        } catch {
          if (state.mounted) setNotice("Saved view state is unavailable. The original values are shown.");
        } finally { if (state.mounted) setReady(true); }
      })();
      return () => { state.mounted = false; state.unsubscribe?.(); };
    }, [spec, raw, preview, metadata.threadId, metadata.itemId, metadata.partId, metadata.artifactId]);

    const disabled = preview || !ready || pending;
    const canSend = !preview && host.actions?.includes("conversation.composer.submit");
    function commit(next) {
      current.current = next; setSnapshot(next); setNotice("");
      const storage = persistence.current;
      if (storage.key) void writeSnapshot(hostRef.current, storage.key, programSnapshotPayload(spec, next)).catch(() => {
        if (storage.mounted) setNotice("This view could not be saved. Your changes are still available here.");
      });
    }
    function changeComponent(address, node, update) {
      if (disabled || pendingRef.current) return false;
      const components = { ...current.current.components };
      const record = update(components[address] ?? componentDefaults(node));
      const isDefault = node.type === "table" ? record.query === "" && record.sort === -1 && record.descending === false
        : node.type === "chart" ? record.hidden.length === 0 && record.dataOpen === false : record.open === (node.open ?? false);
      if (isDefault) delete components[address];
      else components[address] = record;
      const next = { values: current.current.values, components };
      try {
        validateProgramSnapshot(spec, next);
        renderProgram(spec, next.values, { disabled: true, run: () => {}, components });
      } catch (error) { setNotice(error.message); return false; }
      commit(next); return true;
    }
    function run(node, actions, event, item, boundValue) {
      if (disabled || pendingRef.current || !event.isTrusted) return false;
      let next = { ...current.current.values };
      const scope = { item, event: boundValue };
      const budget = { operations: PROGRAM_LIMITS.operations };
      const effects = [];
      let reset = false, changed = false;
      try {
        if (node.state) next[node.state] = boundValue;
        for (const action of actions) {
          if (action.type === "set") next[action.state] = evaluateExpression(action.value, next, scope, budget);
          else if (action.type === "reset") { next = programDefaults(spec); reset = true; }
          else {
            const text = valueText(evaluateExpression(action.text, next, scope, budget));
            if (action.type === "sendMessage") requireValue(canSend && text.trim(), metadata.interaction?.disabledReason || "Conversation continuation is unavailable here.");
            effects.push({ type: action.type, text });
          }
        }
        const components = reset ? {} : current.current.components;
        changed = reset || JSON.stringify(next) !== JSON.stringify(current.current.values);
        if (changed) validateProgramSnapshot(spec, { values: next, components });
        else validateProgramValues(spec, next);
        renderProgram(spec, next, { disabled: true, run: () => {}, components }, budget);
        if (effects.some(effect => effect.type === "sendMessage")) {
          const invalid = [...(surface.current?.querySelectorAll("input,textarea,select") ?? [])].find(control => !control.validity.valid);
          if (invalid) { invalid.reportValidity(); throw new Error("Correct the highlighted value before sending a message."); }
        }
      } catch (error) { setNotice(error.message); return false; }
      if (changed) commit({ values: next, components: reset ? {} : current.current.components });
      else setNotice("");
      if (reset) setResetVersion(value => value + 1);
      const storage = persistence.current;
      // Host invocation stays synchronous with this trusted activation. The
      // bound host checks source liveness and consumes the gesture exactly once.
      if (effects.length) { pendingRef.current = true; setPending(true); }
      const results = effects.map(effect => {
        let promise;
        try {
          promise = effect.type === "copy" ? navigator.clipboard.writeText(effect.text)
            : hostRef.current.invoke("conversation.composer.submit", { text: effect.text, context: { actionId: node.id, label: valueText(evaluateExpression(node.label, next, { item })), values: next } });
        } catch (error) { promise = Promise.reject(error); }
        return Promise.resolve(promise).then(() => effect.type === "copy" ? "Copied" : "Sent to this conversation.", error => error?.message || "The action could not be completed. Your local values are unchanged.");
      });
      if (results.length) void Promise.all(results).then(messages => {
        if (storage.mounted) setNotice(messages.join(" "));
      }).finally(() => { pendingRef.current = false; if (storage.mounted) setPending(false); });
      return true;
    }
    let tree, error;
    try {
      const visible = preview ? defaults : snapshot;
      tree = renderProgram(spec, visible.values, { disabled, canSend, run, instanceId: instanceId.current, resetVersion, components: visible.components, changeComponent });
    }
    catch (caught) { error = caught.message; }
    return h("section", { ref: surface, className: "plugin-genui plugin-genui-program", "data-wuu-component": "generated-ui", "data-genui-preview": String(Boolean(preview)), "aria-label": spec.title, "aria-busy": !stopped && Boolean(preview || !ready || pending) },
      h("header", { className: "plugin-genui-header" }, h("h2", null, spec.title), h("span", { className: "plugin-genui-muted" }, stopped ? "Incomplete preview" : preview ? "Building preview…" : "Interactive")),
      error ? h(ui.ErrorState, { title: "This view could not be calculated", description: error }) : tree,
      notice ? h("p", { role: "status", className: "plugin-genui-notice" }, notice) : null,
      !preview && hasContinuation(spec.body) && !canSend ? h("p", { className: "plugin-genui-muted" }, metadata.interaction?.disabledReason || "Conversation actions are unavailable in this view.") : null,
      h("details", { className: "plugin-genui-summary" }, h("summary", null, "Text summary"), h("p", { className: "plugin-genui-text" }, spec.fallback)),
    );
  }

  function renderProgram(spec, values, runtime, budget = { operations: PROGRAM_LIMITS.operations }) {
    let rendered = 0, expanded = 0;
    function charge(count) { expanded += count; requireValue(expanded <= 10000, "Rendered data limit reached"); }
    function nodes(nodeList, scope = {}, ancestors = []) {
      return nodeList.map(node => {
        requireValue(++rendered <= PROGRAM_LIMITS.rendered, "Rendered UI limit reached");
        charge(4 + (node.options?.length ?? 0) * 3);
        const key = node.id;
        const address = JSON.stringify([node.id, ancestors]);
        const evaluate = expression => evaluateExpression(expression, values, scope, budget);
        const text = expression => valueText(evaluate(expression));
        const title = node.title === undefined ? undefined : text(node.title);
        const props = { key, "data-genui-node": node.id };
        const children = () => nodes(node.children, scope, ancestors);
        const component = runtime.components?.[address] ?? componentDefaults(node);
        const changeView = update => runtime.changeComponent?.(address, node, update) ?? false;
        const change = (value, event) => runtime.run(node, node.onChange ?? [], event, scope.item, value);
        const controlInvalid = Boolean(node.state && node.invalid !== undefined && evaluate(node.invalid));
        const controlProps = node.state ? {
          disabled: runtime.disabled || Boolean(node.disabled !== undefined && evaluate(node.disabled)),
          required: node.required,
          "aria-invalid": controlInvalid || undefined,
          ref: element => { if (element?.setCustomValidity) element.setCustomValidity(controlInvalid ? "Please check this value." : ""); },
        } : {};
        switch (node.type) {
          case "stack": case "row": case "flow": case "grid": case "card":
            return h(node.type === "card" ? ui.Card : "div", { ...props, className: `plugin-genui-layout plugin-genui-${node.type}`, "data-gap": node.gap ?? "medium", "data-align": node.align ?? "stretch", ...(node.type === "grid" ? { style: { "--genui-columns": node.columns ?? 2 } } : {}) },
              title ? h("h3", { className: "plugin-genui-card-title" }, title) : null, children());
          case "text": return h("p", { ...props, className: "plugin-genui-text" }, text(node.text));
          case "caption": return h("p", { ...props, className: "plugin-genui-text plugin-genui-muted" }, text(node.text));
          case "heading": return h(`h${(node.level ?? 2) + 1}`, { ...props, className: "plugin-genui-heading", "data-level": node.level ?? 2 }, text(node.text));
          case "badge": return h("span", { ...props, className: "plugin-genui-badge" }, text(node.text));
          case "divider": return h("hr", { ...props, className: "plugin-genui-divider" });
          case "if": return h("div", { ...props, className: "plugin-genui-branch" }, nodes(evaluate(node.when) ? node.then : node.else ?? [], scope, ancestors));
          case "each": {
            const items = evaluate(node.items); list(items, 0, PROGRAM_LIMITS.iterations);
            const identities = collectionIdentities(node, items, values, scope, budget);
            return h("div", { ...props, className: "plugin-genui-repeat" }, items.map((item, index) => h("div", { key: identities[index], className: "plugin-genui-repeat-item" }, nodes(node.children, { ...scope, item }, [...ancestors, [node.id, identities[index]]]))));
          }
          case "form": {
            const sends = node.onSubmit.some(action => action.type === "sendMessage");
            return h("form", { ...props, className: "plugin-genui-form", onSubmit: event => { event.preventDefault(); if (event.currentTarget.reportValidity()) runtime.run(node, node.onSubmit, event, scope.item); } },
              children(), h("div", { className: "plugin-genui-action" }, h(ui.Button, { type: "submit", disabled: runtime.disabled || (sends && !runtime.canSend), variant: "primary" }, text(node.label)), sends ? h("span", { className: "plugin-genui-muted plugin-genui-action-hint" }, "Sends a message") : null));
          }
          case "disclosure": return h(ProgramDetails, { ...props, title, className: "plugin-genui-disclosure", disabled: runtime.disabled, open: component.open, change: open => changeView(current => ({ ...current, open })) }, h("div", { className: "plugin-genui-stack plugin-genui-layout", "data-gap": "medium" }, children()));
          case "metric": return h("div", { ...props, className: "plugin-genui-metric" }, h("span", { className: "plugin-genui-muted" }, text(node.label)), h("strong", null, text(node.value)), node.detail === undefined ? null : h("span", { className: "plugin-genui-muted" }, text(node.detail)));
          case "progress": {
            const value = evaluate(node.value), max = node.max === undefined ? 100 : evaluate(node.max); number(value); number(max); requireValue(max > 0 && value >= 0 && value <= max, "Progress must be within its range");
            const label = node.label === undefined ? "Progress" : text(node.label);
            return h("label", { ...props, className: "plugin-genui-progress" }, h("span", null, label), h("progress", { value, max, "aria-label": label }), h("span", { className: "plugin-genui-muted" }, new Intl.NumberFormat(undefined, { style: "percent", maximumFractionDigits: 0 }).format(value / max)));
          }
          case "button": {
            const sends = node.onClick.some(action => action.type === "sendMessage");
            return h("div", { ...props, className: "plugin-genui-action" }, h(ui.Button, { variant: node.variant ?? "secondary", disabled: runtime.disabled || Boolean(node.disabled !== undefined && evaluate(node.disabled)) || (sends && !runtime.canSend), onClick: event => runtime.run(node, node.onClick, event, scope.item) }, text(node.label)), sends ? h("span", { className: "plugin-genui-muted plugin-genui-action-hint" }, "Sends a message") : null);
          }
          case "input": case "textarea": {
            const numberInput = node.inputType === "number";
            if (numberInput) return h(ProgramNumberInput, { ...props, label: text(node.label), ...controlProps, placeholder: node.placeholder, value: values[node.state], change, resetVersion: runtime.resetVersion });
            return h(node.type === "textarea" ? ui.TextArea : ui.TextInput, { ...props, label: text(node.label), ...controlProps, type: node.inputType ?? "text", placeholder: node.placeholder, maxLength: 4000, value: values[node.state], ...(numberInput ? { min: -1e12, max: 1e12, step: "any" } : {}), onChange: event => {
              const next = numberInput ? event.target.valueAsNumber : event.target.value;
              if (!numberInput || Number.isFinite(next)) change(next, event);
            } });
          }
          case "checkbox": return h(ui.Checkbox, { ...props, label: text(node.label), ...controlProps, checked: values[node.state], onChange: event => change(event.target.checked, event) });
          case "slider": return h("label", { ...props, className: "plugin-ui-field plugin-genui-slider" }, h("span", { className: "plugin-genui-slider-label" }, text(node.label), h("output", null, valueText(values[node.state]))), h("input", { type: "range", ...controlProps, min: node.min, max: node.max, step: node.step ?? 1, value: values[node.state], onChange: event => change(event.target.valueAsNumber, event) }), h("span", { className: "plugin-genui-slider-bounds plugin-genui-muted", "aria-hidden": true }, h("span", null, node.min), h("span", null, node.max)));
          case "select": return h(ui.Select, { ...props, label: text(node.label), ...controlProps, value: String(node.options.findIndex(option => option.value === values[node.state])), onChange: event => change(node.options[Number(event.target.value)].value, event) }, node.options.map((option, index) => h("option", { key: index, value: String(index) }, option.label)));
          case "radio": case "segmented": return h("fieldset", { ...props, disabled: controlProps.disabled, className: `plugin-genui-options plugin-genui-${node.type}` }, h("legend", null, text(node.label)), h("div", { className: "plugin-genui-options-list" }, node.options.map((option, index) => h("label", { key: index, "data-selected": option.value === values[node.state] }, h("input", { type: "radio", ...controlProps, name: `${runtime.instanceId}/${address}`, checked: option.value === values[node.state], onChange: event => change(option.value, event) }), h("span", null, option.label)))));
          case "table": {
            const rows = evaluate(node.rows); list(rows, 0, 200);
            charge((rows.length + 1) * (node.columns.length + 1));
            for (const row of rows) { list(row, node.columns.length, node.columns.length); row.forEach(scalar); }
            return h(ProgramTable, { ...props, node: { ...node, title, rows }, disabled: runtime.disabled, value: component, change: changeView });
          }
          case "chart": {
            const chart = { ...node, title };
            if (node.points !== undefined) { chart.points = evaluate(node.points); validateChartPoints(chart.points, node.chartType); }
            else chart.series = node.series.map(series => { const points = evaluate(series.points); validateChartPoints(points, series.chartType ?? node.chartType); return { ...series, points }; });
            charge((chart.points?.length ?? chart.series.reduce((sum, series) => sum + series.points.length, 0)) * 7 + (chart.series?.length ?? 1) * 4);
            if (chart.series && chart.series.every(series => (series.chartType ?? node.chartType) !== "scatter")) {
              const labels = chart.series[0].points.map(point => point.label);
              requireValue(chart.series.every(series => series.points.length === labels.length && series.points.every((point, index) => point.label === labels[index])), "Category series must share the same ordered labels");
            }
            if (["pie", "donut"].includes(node.chartType)) {
              const seen = new Map();
              chart.pointIdentities = chart.points.map(point => {
                const identity = canonicalJSON(point, budget), ordinal = seen.get(identity) ?? 0;
                seen.set(identity, ordinal + 1); return JSON.stringify([identity, ordinal]);
              });
            }
            return h(ProgramChart, { ...props, node: chart, disabled: runtime.disabled, value: component, change: changeView });
          }
          default: throw new Error("Unsupported UI node");
        }
      });
    }
    return h("fieldset", { className: "plugin-genui-program-body", disabled: runtime.disabled }, nodes(spec.body));
  }
  function ProgramNumberInput({ label, value, disabled, placeholder, change, resetVersion, "aria-invalid": modelInvalid, ref: modelRef, ...props }) {
    const [draft, setDraft] = useState(String(value));
    const [invalid, setInvalid] = useState(false);
    useEffect(() => { setDraft(String(value)); setInvalid(false); }, [value, resetVersion]);
    return h(ui.TextInput, { ...props, label, type: "number", step: "any", min: -1e12, max: 1e12, disabled, placeholder, value: draft, "aria-invalid": invalid || modelInvalid || undefined, ref: element => { modelRef?.(element); if (element && invalid) element.setCustomValidity("Enter a valid number before continuing."); },
      onChange: event => {
        setDraft(event.target.value);
        const next = event.target.valueAsNumber;
        const valid = Number.isFinite(next) && Math.abs(next) <= 1e12;
        setInvalid(!valid);
        if (valid && change(next, event) === false) setInvalid(true);
      },
      onKeyDown: event => { if (event.key === "Escape" && invalid) { event.preventDefault(); event.stopPropagation(); setDraft(String(value)); setInvalid(false); } },
    });
  }
  function ProgramDetails({ title, open, change, disabled, children, ...props }) {
    return h("details", { ...props, open, onToggle: event => {
      if (event.currentTarget.open === open) return;
      if (disabled || change(event.currentTarget.open) === false) event.currentTarget.open = open;
    } }, h("summary", { onClick: event => { if (disabled) event.preventDefault(); } }, title), children);
  }
  function ProgramTable({ node, disabled, value, change }) {
    const update = next => {
      const patch = Object.fromEntries(["query", "sort", "descending"].filter(key => next[key] !== value[key]).map(key => [key, next[key]]));
      return change(current => ({ ...current, ...patch }));
    };
    return h("fieldset", { disabled, className: "plugin-genui-data-fieldset", "data-genui-node": node.id }, h(TableBlock, { block: node, value, change: update }));
  }
  function ProgramChart({ node, disabled, value, change }) {
    const hidden = value.hidden;
    const setHidden = update => change(current => ({ ...current, hidden: update(current.hidden).sort() }));
    const [active, setActive] = useState(-1);
    const circular = node.chartType === "pie" || node.chartType === "donut";
    const sources = node.series ?? [{ id: node.id, label: node.title ?? "Values", chartType: node.chartType, points: node.points }];
    const series = sources.filter(series => !hidden.includes(series.id));
    const palette = index => `var(--genui-series-${index % 8})`;
    const entries = series.flatMap(series => series.points.map((point, index) => ({ ...point, index, series: series.id, labelText: `${series.label}: ${point.label}: ${point.value}`, color: palette(sources.indexOf(series)), chartType: series.chartType ?? node.chartType, base: 0, top: point.value })));
    if (node.stacked) {
      const positive = [], negative = [];
      for (const entry of entries) {
        const sums = entry.value >= 0 ? positive : negative;
        entry.base = sums[entry.index] ?? 0; entry.top = entry.base + entry.value; sums[entry.index] = entry.top;
      }
    }
    const low = Math.min(0, ...entries.map(point => Math.min(point.base, point.top)));
    const maximum = Math.max(0, ...entries.map(point => Math.max(point.base, point.top)));
    const high = maximum === low ? low + 1 : maximum;
    const y = value => 200 - (value - low) / (high - low) * 200;
    const count = Math.max(1, ...series.map(series => series.points.length));
    const scatterPoints = entries.filter(entry => entry.chartType === "scatter");
    const minX = scatterPoints.length ? Math.min(...entries.map(point => point.x ?? point.index)) : 0;
    const maxX = scatterPoints.length ? Math.max(...entries.map(point => point.x ?? point.index)) : 1;
    const x = point => scatterPoints.length ? 8 + ((point.x ?? point.index) - minX) / (maxX - minX || 1) * 504 : (point.index + 0.5) * 520 / count;
    const selected = entries[active];
    const chartLabel = `${node.title ?? "Chart"}. ${entries.length} values. Use arrow keys to inspect values or open the data table.`;
    const plotProps = { className: "plugin-genui-chart plugin-genui-program-chart", viewBox: "0 0 520 200", preserveAspectRatio: "none", role: "img", tabIndex: disabled ? -1 : 0, "aria-label": selected ? `${chartLabel} ${selected.labelText}` : chartLabel,
      onKeyDown: event => { if (["ArrowLeft", "ArrowRight", "Home", "End"].includes(event.key)) { event.preventDefault(); setActive(event.key === "Home" ? 0 : event.key === "End" ? entries.length - 1 : (active + (event.key === "ArrowRight" ? 1 : -1) + entries.length) % entries.length); } },
    };
    let chart;
    if (circular) {
      const points = node.points.map((point, index) => ({ point, index })).filter(entry => !hidden.includes(node.pointIdentities[entry.index]));
      const total = points.reduce((sum, entry) => sum + entry.point.value, 0);
      let angle = -Math.PI / 2;
      chart = total > 0 ? h("svg", { className: "plugin-genui-pie", viewBox: "0 0 240 240", role: "img", "aria-label": `${node.title ?? "Chart"}. Total ${formatAxis(total)}. Exact values are in the chart data table.` },
        h("title", null, node.title ?? "Chart"), points.map(({ point, index }) => {
          const start = angle; angle += point.value / total * Math.PI * 2;
          const end = angle;
          const radius = 104, inner = node.chartType === "donut" ? 64 : 0;
          const polar = (angle, r) => [120 + Math.cos(angle) * r, 120 + Math.sin(angle) * r].join(" ");
          // Two half arcs also represent a single full-circle segment correctly.
          const mid = (start + end) / 2;
          const path = `M ${polar(start, radius)} A ${radius} ${radius} 0 0 1 ${polar(mid, radius)} A ${radius} ${radius} 0 0 1 ${polar(end, radius)} ${inner ? `L ${polar(end, inner)} A ${inner} ${inner} 0 0 0 ${polar(mid, inner)} A ${inner} ${inner} 0 0 0 ${polar(start, inner)}` : "L 120 120"} Z`;
          return h("path", { key: node.pointIdentities[index], d: path, fill: palette(index), stroke: "var(--wuu-color-canvas)", strokeWidth: 2 }, h("title", null, `${point.label}: ${point.value} (${Math.round(point.value / total * 100)}%)`));
        })) : h("p", { className: "plugin-genui-muted" }, "No positive values to plot");
    } else {
      chart = h("div", { className: "plugin-genui-plot" },
        h("div", { className: "plugin-genui-y-labels", "aria-hidden": true }, [high, (low + high) / 2, low].map((value, index) => h("span", { key: index }, formatAxis(value)))),
        h("svg", plotProps,
          h("title", null, node.title ?? "Chart"),
          [0, 100, 200].map(value => h("line", { key: value, x1: 0, x2: 520, y1: value, y2: value, className: "plugin-genui-grid" })),
          h("line", { x1: 0, x2: 520, y1: y(0), y2: y(0), className: "plugin-genui-axis" }),
          series.map(series => {
            const points = entries.filter(point => point.series === series.id);
            const type = series.chartType ?? node.chartType;
            const color = palette(sources.indexOf(series));
            return h("g", { key: series.id },
              type === "area" ? h("path", { d: `M ${points.map(point => `${x(point)} ${y(point.top)}`).join(" L ")} L ${[...points].reverse().map(point => `${x(point)} ${y(point.base)}`).join(" L ")} Z`, fill: color, fillOpacity: 0.18 }) : null,
              type === "line" || type === "area" ? h("polyline", { points: points.map(point => `${x(point)},${y(point.top)}`).join(" "), fill: "none", stroke: color, strokeWidth: 2.5, vectorEffect: "non-scaling-stroke" }) : null,
              points.map(point => {
                const index = entries.indexOf(point);
                const bars = seriesListForBars();
                const width = 360 / count / (node.stacked ? 1 : Math.max(1, bars.length));
                const xOffset = node.stacked ? -width / 2 : -180 / count + Math.max(0, bars.findIndex(item => item.id === series.id)) * width;
                const shape = type === "bar" ? { x: x(point) + xOffset, y: Math.min(y(point.base), y(point.top)), width, height: Math.max(1, Math.abs(y(point.top) - y(point.base))) } : { cx: x(point), cy: y(point.top), r: type === "scatter" ? 4 : active === index ? 5 : 3 };
                return h(type === "bar" ? "rect" : "circle", { key: point.index, ...shape, fill: color, opacity: active < 0 || active === index ? 1 : 0.6, onMouseEnter: () => setActive(index), onMouseLeave: () => setActive(-1) }, h("title", null, point.labelText));
              }));
          })),
        scatterPoints.length ? h("div", { className: "plugin-genui-x-range plugin-genui-scatter-range", "aria-hidden": true }, h("span", null, formatAxis(minX)), h("span", null, formatAxis(maxX)))
          : count <= 8 ? h("div", { className: "plugin-genui-x-categories", "aria-hidden": true, style: { gridTemplateColumns: `repeat(${count},minmax(0,1fr))` } }, (series[0]?.points ?? []).map((point, index) => h("span", { key: index, title: point.label }, point.label)))
            : h("div", { className: "plugin-genui-x-range", "aria-hidden": true }, `${series[0]?.points[0]?.label ?? ""} – ${series[0]?.points.at(-1)?.label ?? ""}`),
      );
    }
    function seriesListForBars() { return series.filter(series => (series.chartType ?? node.chartType) === "bar"); }
    const legend = circular ? node.points.map((point, index) => ({ id: node.pointIdentities[index], label: point.label, color: palette(index) })) : sources.map((series, index) => ({ ...series, color: palette(index) }));
    return h(ui.Section, { title: node.title, "data-genui-block": node.id, "data-genui-node": node.id, className: "plugin-genui-chart-section" },
      circular || sources.length > 1 ? h("div", { className: "plugin-genui-legend", "aria-label": "Chart series" }, legend.map(item => h(ui.Button, { key: item.id, variant: "ghost", disabled, "aria-pressed": !hidden.includes(item.id), onClick: () => { setActive(-1); setHidden(current => current.includes(item.id) ? current.filter(id => id !== item.id) : [...current, item.id]); } }, h("span", { className: "plugin-genui-legend-swatch", style: { background: item.color }, "aria-hidden": true }), item.label))) : null,
      chart,
      !circular ? h("p", { className: "plugin-genui-chart-caption", role: "status" }, selected?.labelText ?? ([node.xLabel, node.yLabel].filter(Boolean).join(" · ") || (series.length ? `${entries.length} values` : "Select a series to show data"))) : null,
      h(ProgramDetails, { title: "Chart data", open: value.dataOpen, disabled, change: dataOpen => change(current => ({ ...current, dataOpen })) }, h("div", { className: "plugin-genui-table-scroll", tabIndex: 0, role: "region", "aria-label": "Chart data" }, h("table", null,
        h("thead", null, h("tr", null, sources.length > 1 ? h("th", { scope: "col" }, "Series") : null, h("th", { scope: "col" }, node.xLabel ?? "Label"), scatterPoints.length ? h("th", { scope: "col" }, "X") : null, h("th", { scope: "col" }, node.yLabel ?? "Value"))),
        h("tbody", null, sources.flatMap(series => series.points.map((point, index) => h("tr", { key: `${series.id}/${index}` }, sources.length > 1 ? h("td", null, series.label) : null, h("td", null, point.label), scatterPoints.length ? h("td", null, point.x ?? "—") : null, h("td", null, point.value)))))))),
    );
  }

  function InteractiveUI({ spec, raw, metadata, host, preview, stopped }) {
    const initial = useMemo(() => defaultValues(spec), [spec]);
    const [storedValues, setValues] = useState(initial);
    const values = preview ? initial : storedValues;
    const [ready, setReady] = useState(false);
    const [notice, setNotice] = useState("");
    const current = useRef(initial);
    const persistence = useRef({ key: undefined, mounted: true });
    const hostRef = useRef(host);
    hostRef.current = host;
    useEffect(() => {
      const state = { key: undefined, mounted: true };
      persistence.current = state;
      if (preview) return () => { state.mounted = false; state.unsubscribe?.(); };
      void (async () => {
        try {
          const identity = JSON.stringify([metadata.threadId, metadata.itemId, metadata.partId, metadata.artifactId, raw]);
          const digest = await crypto.subtle.digest("SHA-256", encoder.encode(identity));
          const key = "ui-v1-" + Array.from(new Uint8Array(digest), byte => byte.toString(16).padStart(2, "0")).join("");
          await hydrateView(hostRef.current, key, saved => restoreValues(spec, saved), restored => { current.current = restored; setValues(restored); }, state);
        } catch {
          if (state.mounted) setNotice("Saved view state is unavailable. Changes may not be restored.");
        } finally {
          if (state.mounted) setReady(true);
        }
      })();
      return () => { state.mounted = false; state.unsubscribe?.(); };
    }, [spec, raw, preview, metadata.threadId, metadata.itemId, metadata.partId, metadata.artifactId]);
    function change(id, value) {
      if (!ready) return;
      const next = { ...current.current, [id]: value };
      current.current = next;
      setValues(next);
      const state = persistence.current;
      if (state.key) void saveValues(hostRef.current, state.key, next).catch(() => {
        if (state.mounted) setNotice("This view could not be saved. Your current changes are still available here.");
      });
    }
    return h("section", { className: "plugin-genui", "data-wuu-component": "generated-ui", "data-genui-preview": String(Boolean(preview)), "aria-label": spec.title, "aria-busy": !stopped && (preview || !ready) },
      h("header", { className: "plugin-genui-header" }, h("h2", null, spec.title), h("span", { className: "plugin-genui-muted" }, stopped ? "Incomplete preview" : preview ? "Building preview…" : "Experimental")),
      notice ? h("p", { role: "status", className: "plugin-genui-muted" }, notice) : null,
      h("fieldset", { disabled: preview || !ready, className: "plugin-genui-blocks" }, spec.blocks.map(block => {
        const props = { key: block.id, block, value: values[block.id], change: value => change(block.id, value) };
        return block.type === "text" ? h("p", { key: block.id, className: "plugin-genui-text" }, block.text)
          : block.type === "table" ? h(TableBlock, props)
            : block.type === "chart" ? h(ChartBlock, props) : h(FormBlock, props);
      })),
      h("details", { className: "plugin-genui-summary" }, h("summary", null, "Text summary"), h("p", { className: "plugin-genui-text" }, spec.fallback)),
    );
  }

  function TableBlock({ block, value, change }) {
    const rows = block.rows.filter(row => row.some(cell => display(cell).toLocaleLowerCase().includes(value.query.toLocaleLowerCase())));
    if (value.sort >= 0) rows.sort((left, right) => {
      const a = left[value.sort], b = right[value.sort];
      const result = typeof a === "number" && typeof b === "number" ? a - b : display(a).localeCompare(display(b));
      return value.descending ? -result : result;
    });
    return h(ui.Section, { title: block.title, "data-genui-block": block.id },
      h(ui.TextInput, { label: "Filter rows", type: "search", value: value.query, maxLength: 100, onChange: event => change({ ...value, query: event.target.value }) }),
      h("div", { className: "plugin-genui-table-scroll", tabIndex: 0, role: "region", "aria-label": block.title ?? "Data table" },
        h("table", null,
          h("thead", null, h("tr", null, block.columns.map((column, index) => h("th", {
            key: column.key, scope: "col", "aria-sort": value.sort === index ? (value.descending ? "descending" : "ascending") : "none",
          }, h(ui.Button, { variant: "ghost", onClick: () => change({ ...value, sort: index, descending: value.sort === index && !value.descending }) }, column.label, value.sort === index ? (value.descending ? " ↓" : " ↑") : " ↕"))))),
          h("tbody", null, rows.map((row, index) => h("tr", { key: index }, row.map((cell, column) => h("td", { key: column }, display(cell))))))),
      ),
      h("p", { className: "plugin-genui-muted", role: "status" }, rows.length ? `${rows.length} of ${block.rows.length} rows` : "No matching rows"),
    );
  }

  function ChartBlock({ block, value, change }) {
    const [active, setActive] = useState(-1);
    const points = value.range === "all" ? block.points : block.points.slice(-Number(value.range));
    const low = Math.min(0, ...points.map(point => point.value));
    const maximum = Math.max(0, ...points.map(point => point.value));
    const high = maximum === low ? low + 1 : maximum;
    const span = high - low;
    const x = index => (index + 0.5) * 520 / points.length;
    const y = number => 180 - (number - low) / span * 180;
    const selected = points[active];
    return h(ui.Section, { title: block.title, "data-genui-block": block.id },
      h(ui.Select, { label: "Show points", value: value.range, onChange: event => { setActive(-1); change({ range: event.target.value }); } },
        h("option", { value: "all" }, "All points"), h("option", { value: "10" }, "Last 10"), h("option", { value: "25" }, "Last 25")),
      h("div", { className: "plugin-genui-plot" },
        // Keep labels outside the scalable SVG so narrow windows and large UI
        // font preferences never shrink the text with the chart geometry.
        h("div", { className: "plugin-genui-y-labels", "aria-hidden": true },
          ...[high, low + span / 2, low].map((value, index) => h("span", { key: index }, formatAxis(value)))),
        h("svg", { className: "plugin-genui-chart", viewBox: "0 0 520 180", preserveAspectRatio: "none", role: "img", "aria-label": `${block.title ?? "Chart"}. ${points.length} points. Use the data table for exact values.` },
          h("title", null, block.title ?? "Chart"),
          ...[0, 0.5, 1].map((fraction, index) => h("line", { key: `grid-${index}`, x1: 0, x2: 520, y1: fraction * 180, y2: fraction * 180, className: "plugin-genui-grid" })),
          h("line", { x1: 0, x2: 520, y1: y(0), y2: y(0), className: "plugin-genui-axis" }),
          block.chartType === "line" ? h("polyline", { points: points.map((point, index) => `${x(index)},${y(point.value)}`).join(" "), className: "plugin-genui-line" }) : null,
          ...points.map((point, index) => h("g", { key: `point-${index}`, onMouseEnter: () => setActive(index), onMouseLeave: () => setActive(-1) },
            block.chartType === "bar" ? h("rect", { x: x(index) - 180 / points.length, width: 360 / points.length, y: Math.min(y(0), y(point.value)), height: Math.max(1, Math.abs(y(point.value) - y(0))), className: "plugin-genui-mark" })
              : h("circle", { cx: x(index), cy: y(point.value), r: points.length > 40 ? 2 : 4, className: "plugin-genui-mark" }),
            h("title", null, `${point.label}: ${point.value}`))),
        ),
        points.length <= 8
          // Both chart types use the SVG's (index + 0.5) / count centers.
          ? h("div", { className: "plugin-genui-x-categories", "aria-hidden": true, style: { gridTemplateColumns: `repeat(${points.length}, minmax(0, 1fr))` } },
            points.map((point, index) => h("span", { key: index, title: point.label }, point.label)))
          // Dense series use an explicit summary, not misleading endpoint ticks.
          : h("div", { className: "plugin-genui-x-range", "aria-hidden": true }, `Range: ${points[0].label} – ${points.at(-1).label}`),
      ),
      h("p", { className: "plugin-genui-chart-caption" }, selected ? `${selected.label}: ${selected.value}` : [block.xLabel, block.yLabel].filter(Boolean).join(" · ") || `${points.length} points`),
      h("details", null, h("summary", null, "Chart data"),
        h("div", { className: "plugin-genui-table-scroll", tabIndex: 0, role: "region", "aria-label": "Chart data" }, h("table", null,
          h("thead", null, h("tr", null, h("th", { scope: "col" }, block.xLabel ?? "Label"), h("th", { scope: "col" }, block.yLabel ?? "Value"))),
          h("tbody", null, points.map((point, index) => h("tr", { key: index }, h("td", null, point.label), h("td", null, point.value))))))),
    );
  }

  function FormBlock({ block, value, change }) {
    const [preview, setPreview] = useState(false);
    const [message, setMessage] = useState("");
    const summary = block.fields.map(field => `${field.label}: ${display(value[field.id])}`).join("\n");
    function update(field, next) { setPreview(false); setMessage(""); change({ ...value, [field.id]: next }); }
    return h(ui.Section, { title: block.title, "data-genui-block": block.id },
      h("form", { className: "plugin-genui-form", onSubmit: event => { event.preventDefault(); setPreview(true); setMessage(""); } },
        block.fields.map(field => field.kind === "checkbox"
          ? h(ui.Checkbox, { key: field.id, label: field.label, checked: value[field.id], onChange: event => update(field, event.target.checked) })
          : field.kind === "select" ? h(ui.Select, { key: field.id, label: field.label, value: value[field.id], onChange: event => update(field, event.target.value) }, field.options.map(option => h("option", { key: option, value: option }, option)))
            : h(ui.TextInput, { key: field.id, label: field.label, type: field.kind === "number" ? "number" : "text", value: value[field.id], maxLength: 500,
              ...(field.kind === "number" ? { min: -1e12, max: 1e12, step: "any" } : {}),
              onChange: event => { const next = field.kind === "number" && event.target.value !== "" ? event.target.valueAsNumber : event.target.value;
                if (typeof next !== "number" || (Number.isFinite(next) && Math.abs(next) <= 1e12)) update(field, next); },
            })),
        h("p", { className: "plugin-genui-muted" }, "Local draft. Preview and copy do not send a message or submit data."),
        h(ui.Row, null, h(ui.Button, { type: "submit" }, "Preview values"), h(ui.Button, { variant: "ghost", onClick: () => { setPreview(false); setMessage(""); change(defaultValues({ blocks: [block] })[block.id]); } }, "Reset")),
      ),
      preview ? h("div", { className: "plugin-genui-form-preview" }, h("pre", null, summary), h(ui.Button, { onClick: async (event) => {
        if (!event.isTrusted) return;
        try { await navigator.clipboard.writeText(summary); setMessage("Copied"); }
        catch { setMessage("Copy unavailable. Select the preview text to copy it."); }
      } }, "Copy values")) : null,
      message ? h("p", { role: "status", className: "plugin-genui-muted" }, message) : null,
    );
  }

  api.registerRenderer({ id: "generated-ui-v1", category: "tool-result", match: MIME, toolInput: { capability: "generative-ui" }, render: GeneratedUI });
  api.registerCSSSnippet({ id: "generated-ui-layout", css: styles });
}

function hasContinuation(nodes) {
  return nodes.some(node => (node.onClick ?? node.onSubmit)?.some(action => action.type === "sendMessage") || ["children", "then", "else"].some(key => node[key] && hasContinuation(node[key])));
}
function validateChartPoints(points, type) {
  list(points, 1, 100);
  for (const point of points) {
    object(point, ["label", "value", "x"], ["label", "value"]); string(point.label, 80); number(point.value);
    if (point.x !== undefined || type === "scatter") number(point.x);
    if (type === "pie" || type === "donut") requireValue(point.value >= 0, "Circular charts require nonnegative values");
  }
}
function formatAxis(value) {
  return new Intl.NumberFormat(undefined, { notation: "compact", maximumFractionDigits: 1 }).format(value);
}

const styles = `
.plugin-genui { --genui-border:var(--wuu-color-border-subtle, color-mix(in srgb, currentColor 16%, transparent)); display:grid; gap:20px; min-width:0; padding:20px; border:1px solid var(--genui-border); border-radius:var(--wuu-radius-panel, 16px); color:var(--wuu-color-text, currentColor); font-size:var(--wuu-font-size-ui, var(--font-ui, 14px)); line-height:1.5; }
.plugin-genui-header { display:flex; flex-wrap:wrap; gap:8px 16px; align-items:baseline; justify-content:space-between; }
.plugin-genui-header h2 { font:inherit; font-weight:600; margin:0; overflow-wrap:anywhere; }
.plugin-genui-muted { color:var(--wuu-color-text-muted, currentColor); margin:0; }
.plugin-genui-blocks { display:grid; gap:24px; margin:0; padding:0; border:0; min-width:0; }
.plugin-genui-blocks > * { min-width:0; }
.plugin-genui-text { white-space:pre-wrap; overflow-wrap:anywhere; margin:0; }
.plugin-genui-table-scroll { max-width:100%; overflow:auto; border:1px solid var(--genui-border); border-radius:var(--wuu-radius-control, 8px); }
.plugin-genui table { width:100%; border-collapse:collapse; font:inherit; text-align:left; }
.plugin-genui th, .plugin-genui td { padding:8px 12px; border-bottom:1px solid var(--genui-border); max-width:320px; overflow-wrap:anywhere; vertical-align:top; }
.plugin-genui th { font-weight:500; }
.plugin-genui th button { white-space:normal; text-align:left; padding:0; }
.plugin-genui tr:last-child td { border-bottom:0; }
.plugin-genui-plot { display:grid; grid-template-columns:minmax(3ch, max-content) minmax(0, 1fr); gap:8px 12px; padding-top:4px; }
.plugin-genui-y-labels { display:flex; flex-direction:column; justify-content:space-between; text-align:right; line-height:1; height:180px; }
.plugin-genui-y-labels span:first-child { transform:translateY(-50%); }
.plugin-genui-y-labels span:last-child { transform:translateY(50%); }
.plugin-genui-chart { display:block; width:100%; height:180px; overflow:visible; }
.plugin-genui-chart line, .plugin-genui-chart polyline { vector-effect:non-scaling-stroke; }
.plugin-genui-x-categories { grid-column:2; display:grid; min-width:0; text-align:center; }
.plugin-genui-x-categories span { min-width:0; padding:0 4px; overflow:hidden; text-overflow:ellipsis; white-space:nowrap; }
.plugin-genui-x-range { grid-column:2; text-align:center; overflow-wrap:anywhere; }
line.plugin-genui-grid { stroke:var(--genui-border); stroke-dasharray:3 4; }
.plugin-genui-axis { stroke:var(--wuu-color-text-muted, currentColor); }
.plugin-genui-line { fill:none; stroke:var(--wuu-color-accent, currentColor); stroke-width:2.5; }
.plugin-genui-mark { fill:var(--wuu-color-accent, currentColor); }
.plugin-genui-chart-caption { min-height:1.5em; margin:0; overflow-wrap:anywhere; }
.plugin-genui-form { display:grid; gap:12px; }
.plugin-genui-form-preview { display:grid; gap:12px; margin-top:12px; justify-items:start; }
.plugin-genui-form-preview pre { font:inherit; white-space:pre-wrap; overflow-wrap:anywhere; max-width:100%; margin:0; }
.plugin-genui summary { cursor:pointer; color:var(--wuu-color-text-muted, currentColor); }
.plugin-genui details[open] > summary { margin-bottom:12px; }
.plugin-genui :focus-visible { outline:2px solid var(--wuu-color-accent, currentColor); outline-offset:3px; }
@media (max-width:600px) { .plugin-genui { padding:12px; } .plugin-genui th, .plugin-genui td { padding:8px; } }
.plugin-genui-surface { container-type:inline-size; min-width:0; font-size:var(--wuu-font-size-ui,var(--font-ui,14px)); }
.plugin-genui-program { --genui-series-0:var(--wuu-color-accent,currentColor); --genui-series-1:var(--wuu-color-info,#0068da); --genui-series-2:var(--wuu-color-success,#137a3d); --genui-series-3:var(--wuu-color-warning,#8a5b10); --genui-series-4:var(--wuu-color-danger,#b42318); --genui-series-5:color-mix(in srgb,var(--genui-series-1) 65%,var(--genui-series-4)); --genui-series-6:color-mix(in srgb,var(--genui-series-2) 65%,var(--genui-series-1)); --genui-series-7:color-mix(in srgb,var(--genui-series-3) 65%,var(--genui-series-4)); }
.plugin-genui-program :is(button,input,textarea,select) { font:inherit; }
.plugin-genui-program :is(.plugin-ui-field,.plugin-ui-section,.plugin-ui-card) { min-width:0; }
.plugin-genui-program :is(.plugin-ui-field-label,.plugin-ui-section-header h2) { font-size:inherit; overflow-wrap:anywhere; }
.plugin-genui-program .plugin-ui-button { white-space:normal; overflow-wrap:anywhere; height:auto; min-height:2.3em; }
.plugin-genui-program :is(.plugin-ui-input,.plugin-ui-textarea) { width:100%; min-width:0; box-sizing:border-box; }
.plugin-genui-program-body,.plugin-genui-data-fieldset { min-width:0; border:0; padding:0; margin:0; }
.plugin-genui-program-body { display:grid; gap:1.4em; }
.plugin-genui-layout { min-width:0; gap:var(--genui-gap,1em); }
.plugin-genui-layout[data-gap="none"] { --genui-gap:0; }
.plugin-genui-layout[data-gap="small"] { --genui-gap:0.5em; }
.plugin-genui-layout[data-gap="medium"] { --genui-gap:1em; }
.plugin-genui-layout[data-gap="large"] { --genui-gap:1.7em; }
.plugin-genui-layout > * { min-width:0; max-width:100%; }
.plugin-genui-stack,.plugin-genui-card { display:flex; flex-direction:column; }
.plugin-genui-row,.plugin-genui-flow { display:flex; flex-wrap:wrap; }
.plugin-genui-row > * { flex:1 1 12em; }
.plugin-genui-flow > * { flex:0 1 auto; }
.plugin-genui-layout[data-align="start"] { align-items:flex-start; }
.plugin-genui-layout[data-align="center"] { align-items:center; }
.plugin-genui-layout[data-align="end"] { align-items:flex-end; }
.plugin-genui-layout[data-align="stretch"] { align-items:stretch; }
.plugin-genui-layout.plugin-genui-grid { display:grid; grid-template-columns:repeat(var(--genui-columns),minmax(0,1fr)); }
.plugin-genui-card { padding:1.15em; }
/* Grid tracks give these cards a definite width; descendants query that width. */
.plugin-genui-grid > .plugin-genui-card { container-type:inline-size; }
.plugin-genui-card-title { margin:0; font:inherit; font-weight:600; overflow-wrap:anywhere; }
.plugin-genui-heading { margin:0; font-weight:600; line-height:1.3; overflow-wrap:anywhere; }
.plugin-genui-heading[data-level="1"] { font-size:1.6em; }
.plugin-genui-heading[data-level="2"] { font-size:1.25em; }
.plugin-genui-heading[data-level="3"] { font-size:1.05em; }
.plugin-genui-badge { width:fit-content; display:inline-block; padding:0.15em 0.65em; border:1px solid var(--genui-border); border-radius:999px; background:var(--wuu-color-surface-muted,transparent); overflow-wrap:anywhere; }
.plugin-genui-divider { width:100%; margin:0; border:0; border-top:1px solid var(--genui-border); }
.plugin-genui-branch,.plugin-genui-repeat,.plugin-genui-repeat-item { display:grid; gap:1em; min-width:0; }
.plugin-genui-metric { display:flex; flex-direction:column; gap:0.3em; overflow-wrap:anywhere; }
.plugin-genui-metric strong { font-size:1.8em; font-weight:600; line-height:1.2; font-variant-numeric:tabular-nums; }
.plugin-genui-progress { display:grid; grid-template-columns:minmax(0,1fr) auto; gap:0.4em 1em; align-items:center; }
.plugin-genui-progress > span:first-child { grid-column:1/-1; overflow-wrap:anywhere; }
.plugin-genui-progress progress { width:100%; min-width:0; height:0.65em; border:0; accent-color:var(--wuu-color-accent); }
.plugin-genui-progress progress::-webkit-progress-bar { border-radius:1em; background:var(--wuu-color-surface-muted,var(--surface-2,color-mix(in srgb,currentColor 14%,transparent))); }
.plugin-genui-progress progress::-webkit-progress-value { border-radius:1em; background:var(--wuu-color-accent,var(--ink-strong,currentColor)); }
.plugin-genui-slider { gap:0.4em; }
.plugin-genui-slider input { width:100%; min-width:0; margin:0; min-height:2em; accent-color:var(--wuu-color-accent); }
.plugin-genui-slider-label,.plugin-genui-slider-bounds { display:flex; justify-content:space-between; gap:1em; overflow-wrap:anywhere; }
.plugin-genui-slider output { font-variant-numeric:tabular-nums; }
.plugin-genui-options { padding:0; margin:0; border:0; min-width:0; }
.plugin-genui-options legend { padding:0; margin-bottom:0.55em; overflow-wrap:anywhere; }
.plugin-genui-options-list { display:flex; flex-wrap:wrap; gap:0.5em; }
.plugin-genui-options-list label { display:flex; align-items:center; gap:0.45em; min-height:2.4em; padding:0.2em 0.65em; border:1px solid var(--genui-border); border-radius:var(--wuu-radius-control,8px); cursor:pointer; overflow-wrap:anywhere; }
.plugin-genui-options-list input { flex:none; accent-color:var(--wuu-color-accent); }
.plugin-genui-options-list label:has(:focus-visible) { outline:2px solid var(--wuu-color-focus,var(--focus-ring,currentColor)); outline-offset:2px; }
.plugin-genui-options-list label[data-selected="true"] { background:var(--wuu-color-surface-muted); border-color:var(--wuu-color-accent); }
.plugin-genui-options:disabled label { cursor:default; opacity:0.65; }
.plugin-genui-segmented .plugin-genui-options-list { gap:0.25em; padding:0.25em; width:fit-content; max-width:100%; background:var(--wuu-color-surface-muted); border-radius:var(--wuu-radius-control,8px); }
.plugin-genui-segmented .plugin-genui-options-list label { position:relative; border-color:transparent; padding:0.25em 0.75em; }
.plugin-genui-segmented .plugin-genui-options-list label[data-selected="true"] { background:var(--wuu-color-canvas); border-color:var(--genui-border); }
.plugin-genui-segmented input { position:absolute; width:1px; height:1px; opacity:0; }
.plugin-genui-action { display:flex; flex-wrap:wrap; gap:0.4em 0.8em; align-items:center; }
.plugin-genui-action-hint { font-size:0.9em; }
.plugin-genui-notice { margin:0; padding:0.7em 0; color:var(--wuu-color-text-muted,currentColor); overflow-wrap:anywhere; }
.plugin-genui-disclosure > summary { font-weight:500; color:inherit; }
.plugin-genui-program-chart,.plugin-genui-chart-section .plugin-genui-y-labels { height:200px; }
.plugin-genui-pie { display:block; width:min(100%,260px); height:auto; margin-inline:auto; }
.plugin-genui-legend { display:flex; flex-wrap:wrap; gap:0.4em 0.8em; }
.plugin-genui-legend button { display:inline-flex; align-items:center; gap:0.45em; }
.plugin-genui-legend button[aria-pressed="false"] { opacity:0.55; text-decoration:line-through; }
.plugin-genui-legend-swatch { display:inline-block; flex:none; width:0.8em; height:0.8em; border-radius:0.2em; }
.plugin-genui-scatter-range { display:flex; justify-content:space-between; }
.plugin-genui-program [data-genui-node] { scroll-margin-block:1em; }
@container (max-width:50em) { .plugin-genui-layout.plugin-genui-grid:has(> .plugin-genui-chart-section) { grid-template-columns:minmax(0,1fr); } }
@container (max-width:30em) { .plugin-genui-layout.plugin-genui-grid { grid-template-columns:minmax(0,1fr); } .plugin-genui-program { padding:1em; } .plugin-genui-row { flex-direction:column; } .plugin-genui-row > * { flex-basis:auto; } }
@media (prefers-reduced-motion:reduce) { .plugin-genui *, .plugin-genui *::before, .plugin-genui *::after { scroll-behavior:auto; animation:none; transition:none; } }
@media (forced-colors:active) { .plugin-genui-options-list label[data-selected="true"], .plugin-genui-badge { border-color:Highlight; } .plugin-genui-chart :is(circle,rect,path,polyline) { forced-color-adjust:none; } }

`;
