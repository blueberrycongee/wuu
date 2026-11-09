type MdastPosition = {
  start: { offset?: number };
  end: { offset?: number };
};

type MdastNode = {
  type: string;
  value?: string;
  children?: MdastNode[];
  position?: MdastPosition;
};

type MdastFile = {
  value?: unknown;
};

const CJK_CHARACTER = /[\p{Script=Han}\p{Script=Hiragana}\p{Script=Katakana}\p{Script=Hangul}]/u;
const PUNCTUATION_OR_SYMBOL = /[\p{P}\p{S}]/u;
const STRONG_MARKERS = /\*\*([^*\n]+?)\*\*/g;

function hasBrokenCjkBoundary(value: string, start: number, end: number, content: string): boolean {
  if (!CJK_CHARACTER.test(content)) {
    return false;
  }
  const before = value[start - 1] ?? "";
  const after = value[end] ?? "";
  const first = content[0] ?? "";
  const last = content.at(-1) ?? "";
  const openerIsBlocked = Boolean(before)
    && !/\s/u.test(before)
    && !PUNCTUATION_OR_SYMBOL.test(before)
    && PUNCTUATION_OR_SYMBOL.test(first);
  const closerIsBlocked = Boolean(after)
    && !/\s/u.test(after)
    && !PUNCTUATION_OR_SYMBOL.test(after)
    && PUNCTUATION_OR_SYMBOL.test(last);
  return openerIsBlocked || closerIsBlocked;
}

function repairTextNode(node: MdastNode, source: string): MdastNode[] | undefined {
  if (node.type !== "text" || typeof node.value !== "string") {
    return undefined;
  }
  const startOffset = node.position?.start.offset;
  const endOffset = node.position?.end.offset;
  if (
    typeof startOffset !== "number"
    || typeof endOffset !== "number"
    || source.slice(startOffset, endOffset) !== node.value
  ) {
    return undefined;
  }

  const replacements: MdastNode[] = [];
  let cursor = 0;
  STRONG_MARKERS.lastIndex = 0;
  for (let match = STRONG_MARKERS.exec(node.value); match; match = STRONG_MARKERS.exec(node.value)) {
    const content = match[1];
    const matchEnd = match.index + match[0].length;
    if (!hasBrokenCjkBoundary(node.value, match.index, matchEnd, content)) {
      continue;
    }
    if (match.index > cursor) {
      replacements.push({ type: "text", value: node.value.slice(cursor, match.index) });
    }
    replacements.push({ type: "strong", children: [{ type: "text", value: content }] });
    cursor = matchEnd;
  }
  if (cursor === 0) {
    return undefined;
  }
  if (cursor < node.value.length) {
    replacements.push({ type: "text", value: node.value.slice(cursor) });
  }
  return replacements;
}

/**
 * A bold label written as `**label:**` immediately followed (no whitespace) by
 * a bare URL — e.g. `**入口:**http://localhost:5174/` — cannot survive the
 * CommonMark emphasis pass as the author intended. When the label's closing
 * `**` abuts the URL's first character, the parser pairs the middle `**` with
 * the URL's trailing `**` instead, producing a literal text node (`**label:`)
 * followed by a `strong` that wraps the whole link. The label loses its bold
 * and the link is rendered bold instead.
 *
 * This pass runs after remark-gfm and recovers the intended reading: the text
 * immediately before a bare autolink is a label whose `**` pair was swallowed,
 * so it is re-wrapped in `strong` and the link is pulled out as a sibling.
 */
function linkInStrong(node: MdastNode): MdastNode | undefined {
  if (node.type !== "strong") {
    return undefined;
  }
  const children = node.children ?? [];
  if (children.length !== 1 || children[0].type !== "link") {
    return undefined;
  }
  return children[0];
}

/**
 * Close-adjacent autolinks are either already bare links (when the source URL
 * carried no trailing `**`) or a `strong` that swallowed the link. In both
 * shapes the preceding text node carries an unmatched `**` that is the label's
 * marker, so the label is re-wrapped in `strong` and the link is left bare.
 */
const BOLD_LABEL_MARKER = /(^|\s)\*\*([^\s*][^*]*?)(?:\*\*)?$/u;

function repairBoldAutolinkPair(children: MdastNode[], index: number): MdastNode[] | undefined {
  const text = children[index];
  if (text.type !== "text" || typeof text.value !== "string") {
    return undefined;
  }
  const value = text.value;
  const match = BOLD_LABEL_MARKER.exec(value);
  if (!match) {
    return undefined;
  }
  const prefix = value.slice(0, match.index + match[1].length);
  const label = match[2];
  if (!label) {
    return undefined;
  }
  const next = children[index + 1];
  // The link is either the `strong` that already swallowed it, or a bare
  // autolink follow-on. `next` may be undefined at the end of a paragraph.
  const bareLink = next?.type === "link" ? next : undefined;
  const strong = next ? linkInStrong(next) : undefined;
  const link = bareLink ?? strong;
  if (!link) {
    return undefined;
  }
  const replacements: MdastNode[] = [];
  if (prefix) {
    replacements.push({ type: "text", value: prefix });
  }
  replacements.push({ type: "strong", children: [{ type: "text", value: label }] });
  replacements.push(link);
  // `index` is the text node; the link (bare or strong-wrapped) is the
  // immediate sibling, so both are replaced together.
  return replacements;
}

/**
 * `**label https://url**（下文` breaks differently: the closing `**` is glued
 * to the following CJK text, so the autolink tokenizer swallows it into the
 * link token before emphasis resolution ever sees it.
 * remarkCjkAutolinkBoundary later strips it back out into a text node that
 * starts with `**`, leaving a three-node shape — a text node ending in
 * `**head`, the bare autolink, and a text node starting with `**`. The
 * opener and closer are re-paired so the head and the link sit inside one
 * `strong`.
 */
const BOLD_AUTOLINK_HEAD = /\*\*([^*]*)$/u;

function repairBoldAutolinkSandwich(children: MdastNode[], index: number): MdastNode[] | undefined {
  const text = children[index];
  const link = children[index + 1];
  const closer = children[index + 2];
  if (
    text?.type !== "text" || typeof text.value !== "string"
    || link?.type !== "link"
    || closer?.type !== "text" || typeof closer.value !== "string"
    || !closer.value.startsWith("**")
  ) {
    return undefined;
  }
  const match = BOLD_AUTOLINK_HEAD.exec(text.value);
  if (!match) {
    return undefined;
  }
  const head = match[1];
  // `** url**…` — an all-whitespace head was never meant to open emphasis.
  if (head && !/\S/u.test(head)) {
    return undefined;
  }
  const prefix = text.value.slice(0, match.index);
  const tail = closer.value.slice(2);
  const replacements: MdastNode[] = [];
  if (prefix) {
    replacements.push({ type: "text", value: prefix });
  }
  replacements.push({
    type: "strong",
    children: head ? [{ type: "text", value: head }, link] : [link],
  });
  if (tail) {
    replacements.push({ type: "text", value: tail });
  }
  return replacements;
}

function visit(parent: MdastNode, source: string): void {
  const children = parent.children;
  if (!children) {
    return;
  }
  for (let index = 0; index < children.length; index += 1) {
    const replacements = repairTextNode(children[index], source);
    if (replacements) {
      children.splice(index, 1, ...replacements);
      index += replacements.length - 1;
      continue;
    }
    // The three-node sandwich is checked first: when it matches, the pair
    // repair would wrap only the head text and leave the link's closing
    // `**` orphaned.
    const sandwichRepair = repairBoldAutolinkSandwich(children, index);
    if (sandwichRepair) {
      children.splice(index, 3, ...sandwichRepair);
      index += sandwichRepair.length - 1;
      continue;
    }
    const pairRepair = repairBoldAutolinkPair(children, index);
    if (pairRepair) {
      // The text node and the link (bare or strong-wrapped) it collides with
      // are replaced together.
      children.splice(index, 2, ...pairRepair);
      index += pairRepair.length - 1;
      continue;
    }
    visit(children[index], source);
  }
}

/** Repairs otherwise-literal `**` markers blocked by CJK punctuation boundaries. */
export function remarkCjkStrongBoundary() {
  return (tree: MdastNode, file: MdastFile): void => {
    visit(tree, typeof file?.value === "string" ? file.value : "");
  };
}

export default remarkCjkStrongBoundary;
