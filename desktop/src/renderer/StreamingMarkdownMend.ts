// Adapted from Zeron's markdown/mend.rs (MIT), Copyright (c) 2026 Wing.
// The complete upstream notice is retained in desktop/vendor/zeron/LICENSE.
export const PENDING_STREAM_LINK = "wuu:pending-stream-link";

type Delimiter = { char: string; length: number; position: number };

/** Repair only the live display tail. Never persist or send the repaired text. */
export function mendStreamingMarkdown(text: string): string {
  // Fences are handled by the canonical Markdown parser, including unclosed ones.
  // Do not reinterpret code or math as incomplete prose.
  if (/^ {0,3}(`{3,}|~{3,})/m.test(text)) return text;
  const delimiters: Delimiter[] = [];
  const brackets: number[] = [];
  let code: { length: number; position: number } | undefined;
  let lastContent = -1;
  let pendingURL = -1;
  const runLength = (index: number): number => {
    let end = index + 1;
    while (text[end] === text[index]) end++;
    return end - index;
  };
  const word = (char: string | undefined): boolean => !!char && /[\p{L}\p{N}]/u.test(char);
  for (let i = 0; i < text.length;) {
    const char = text[i];
    if (!code && char === "\\") { if (i + 1 < text.length) lastContent = i + 1; i += 2; continue; }
    if (char === "`") {
      const length = runLength(i);
      if (code?.length === length) code = undefined;
      else if (code) lastContent = i + length - 1;
      else code = { length, position: i + length };
      i += length;
      continue;
    }
    if (code) { lastContent = i++; continue; }
    if (char === "*" || char === "_" || char === "~") {
      const length = runLength(i);
      const end = i + length;
      const previous = text[i - 1];
      const next = text[end];
      if ((char === "~" && length > 2) ||
          (word(previous) && word(next) && (char === "_" || (char === "*" && length === 1)))) {
        lastContent = end - 1; i = end; continue;
      }
      let rest = length;
      if (previous && /\S/.test(previous)) {
        const k = delimiters.findLastIndex(delimiter => delimiter.char === char);
        if (k >= 0) {
          const take = Math.min(rest, delimiters[k].length);
          delimiters[k].length -= take;
          rest -= take;
          delimiters.length = delimiters[k].length ? k + 1 : k;
        }
      }
      if (rest) {
        if (next && /\S/.test(next) && (char !== "~" || rest === 2)) {
          delimiters.push({ char, length: rest, position: end });
        } else lastContent = end - 1;
      }
      i = end; continue;
    }
    if (char === "[") { brackets.push(i++); continue; }
    if (char === "]") {
      const open = brackets.pop();
      if (open !== undefined) {
        for (let k = delimiters.length - 1; k >= 0; k--) {
          if (delimiters[k].position >= open) delimiters.splice(k, 1);
        }
        if (text[i + 1] === "(") {
          let j = i + 2;
          let depth = 0;
          for (; j < text.length; j++) {
            if (text[j] === "\\") { j++; continue; }
            if (text[j] === "(") depth++;
            if (text[j] === ")") { if (!depth) break; depth--; }
          }
          if (j === text.length || j > text.length) { pendingURL = i; break; }
          lastContent = j; i = j + 1; continue;
        }
      }
      lastContent = i++; continue;
    }
    if (/\S/.test(char)) lastContent = i;
    i++;
  }
  // The renderer treats this sentinel as noninteractive text, including image alt text.
  if (pendingURL >= 0) return `${text.slice(0, pendingURL)}](${PENDING_STREAM_LINK})`;
  const pending: Array<{ position: number; closer: string }> = [];
  if (code && lastContent >= code.position) pending.push({ position: code.position, closer: "`".repeat(code.length) });
  for (const delimiter of delimiters) {
    if (lastContent >= delimiter.position) pending.push({ position: delimiter.position, closer: delimiter.char.repeat(delimiter.length) });
  }
  const open = brackets.at(-1);
  if (open !== undefined && lastContent > open) pending.push({ position: open, closer: `](${PENDING_STREAM_LINK})` });
  const closers = pending.sort((a, b) => b.position - a.position).map(item => item.closer).join("");
  const newline = text.lastIndexOf("\n");
  if (newline >= 0 && /^\s*(?:-{1,2}|={1,2})$/.test(text.slice(newline + 1)) &&
      text.slice(0, newline).split("\n").at(-1)?.trim()) {
    return `${text.slice(0, newline)}${closers}${text.slice(newline)}\u200B`;
  }
  const end = text.trimEnd().length;
  return `${text.slice(0, end)}${closers}${text.slice(end)}`;
}
