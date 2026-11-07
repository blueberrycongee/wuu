/**
 * remark plugin that fixes bare-URL autolink boundaries in CJK prose.
 *
 * remark-gfm (micromark-extension-gfm-autolink-literal) only trims trailing
 * ASCII punctuation when deciding where a literal autolink ends, so in
 * Chinese text the link swallows the following fullwidth punctuation — and
 * everything after it — into both the label and the href
 * (`https://example.com/a。` links the `。`). Clicking such a link opens a
 * URL with a percent-encoded garbage tail and usually lands on a 404.
 *
 * This plugin runs after remark-gfm, finds link nodes that came from the
 * autolink literal tokenizer (raw source text equals the link label), and
 * moves the CJK tail back out of the link into a following plain-text node.
 * Explicit markdown links `[text](url)` are never touched: their href is
 * author-specified.
 */

type MdastPosition = {
  start: { offset?: number };
  end: { offset?: number };
};

type MdastNode = {
  type: string;
  url?: string;
  value?: string;
  children?: MdastNode[];
  position?: MdastPosition;
};

type MdastFile = {
  value?: unknown;
};

// Sentence-level CJK punctuation that can never be part of a URL someone
// meant to share. Fullwidth parentheses are NOT in this set: they get
// bracket-balancing semantics below, mirroring how GFM treats ASCII parens.
const CJK_URL_TERMINATORS = new Set([
  "。", "，", "、", "；", "：", "？", "！", "…",
  "【", "】", "《", "》", "「", "」", "『", "』",
  "“", "”", "‘", "’", "〈", "〉", "～",
]);

/**
 * Split an autolink literal into the part that belongs to the URL and the
 * trailing CJK tail that does not. Returns the index at which the URL ends;
 * `url.length` means nothing to trim.
 *
 * Rules:
 * - A terminator at top level ends the URL immediately.
 * - Fullwidth parens balance: paired `（…）` stays inside the URL; an
 *   unmatched trailing `（` ends the URL at that paren; an unmatched `）`
 *   ends the URL before it.
 * - Terminators inside a paired `（…）` group are left alone — the whole
 *   group is kept, so their context is unambiguous.
 */
export function cjkAutolinkUrlEnd(url: string): number {
  let depth = 0;
  let firstUnmatchedOpen = -1;
  for (let index = 0; index < url.length; index += 1) {
    const char = url[index];
    if (char === "（") {
      if (depth === 0) {
        firstUnmatchedOpen = index;
      }
      depth += 1;
    } else if (char === "）") {
      if (depth === 0) {
        return index;
      }
      depth -= 1;
    } else if (depth === 0 && CJK_URL_TERMINATORS.has(char)) {
      return index;
    }
  }
  return depth > 0 ? firstUnmatchedOpen : url.length;
}

// GFM drops these characters from the end of a literal autolink (`?`, `!`,
// `.`, `,`, `:`, `*`, `_`, `~`). micromark only applies that strip at the end
// of the link token, so when CJK text follows the URL without whitespace a
// closing `**` or `__` stays glued inside the href. Once the CJK trim above
// finds where the URL really ends, the same trailing strip runs at that
// boundary.
const GFM_TRAILING_PUNCTUATION = new Set(["?", "!", ".", ",", ":", "*", "_", "~"]);

function isAutolinkLiteral(node: MdastNode, source: string): boolean {
  if (node.type !== "link" || typeof node.url !== "string") {
    return false;
  }
  const children = node.children ?? [];
  if (children.length !== 1 || children[0].type !== "text") {
    return false;
  }
  const label = children[0].value ?? "";
  if (!label || !node.url.endsWith(label)) {
    return false;
  }
  // Explicit `[text](url)` links can also have label === url; tell them
  // apart by the raw source the node was parsed from. Autolink literals
  // cover exactly the URL text.
  const start = node.position?.start.offset;
  const end = node.position?.end.offset;
  if (typeof start !== "number" || typeof end !== "number") {
    return false;
  }
  return source.slice(start, end) === label;
}

function trimLinkNode(node: MdastNode): string {
  const text = node.children?.[0];
  if (!text || typeof text.value !== "string" || typeof node.url !== "string") {
    return "";
  }
  let end = cjkAutolinkUrlEnd(text.value);
  while (end > 0 && GFM_TRAILING_PUNCTUATION.has(text.value[end - 1])) {
    end -= 1;
  }
  if (end >= text.value.length) {
    return "";
  }
  const tail = text.value.slice(end);
  text.value = text.value.slice(0, end);
  node.url = node.url.slice(0, node.url.length - tail.length);
  return tail;
}

function visit(parent: MdastNode, source: string): void {
  const children = parent.children;
  if (!children) {
    return;
  }
  for (let index = 0; index < children.length; index += 1) {
    const node = children[index];
    if (isAutolinkLiteral(node, source)) {
      const tail = trimLinkNode(node);
      if (tail) {
        children.splice(index + 1, 0, { type: "text", value: tail });
        index += 1;
        continue;
      }
    }
    visit(node, source);
  }
}

export function remarkCjkAutolinkBoundary() {
  return (tree: MdastNode, file: MdastFile): void => {
    visit(tree, typeof file?.value === "string" ? file.value : "");
  };
}

export default remarkCjkAutolinkBoundary;
