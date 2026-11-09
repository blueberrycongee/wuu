export function conversationSearchVisibleSnippet({
  query,
  snippet,
  title,
}: {
  query: string;
  snippet?: string;
  title: string;
}): string {
  const trimmedSnippet = snippet?.trim().replace(/\s+/g, " ") ?? "";
  if (!trimmedSnippet || !query.trim()) {
    return "";
  }
  if (normalizeSearchPreview(trimmedSnippet) === normalizeSearchPreview(title)) {
    return "";
  }
  // Keep the match near the start even when a short backend excerpt fits
  // its character budget but not the narrow result column.
  const match = conversationSearchPattern(query)?.exec(trimmedSnippet);
  if (match) {
    const prefix = Array.from(trimmedSnippet.slice(0, match.index));
    const context = prefix.slice(-16).join("");
    const tail = trimmedSnippet.slice(match.index + match[0].length);
    return (prefix.length > 16 ? "…" : "") + context + match[0] + capSnippetText(tail, 120);
  }
  return capSnippetText(trimmedSnippet);
}

export function conversationSearchPattern(query: string): RegExp | null {
  const words = query.trim().split(/\s+/).filter(Boolean);
  if (!words.length) return null;
  return new RegExp(words.map((word) => word.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")).join("\\s+"), "giu");
}

// Cut a snippet at the last whitespace inside the cap so a long
// snippet never splits a word or markdown token mid-stream. Falls back
// to a hard slice when the cap lands inside a single long token (e.g.
// a base64 blob) where there is no whitespace to break on.
function capSnippetText(s: string, maxChars = 280): string {
  if (s.length <= maxChars) return s;
  const slice = s.slice(0, maxChars);
  const lastSpace = slice.lastIndexOf(" ");
  const trimmed =
    lastSpace > maxChars * 0.6 ? slice.slice(0, lastSpace) : slice;
  return trimmed.trimEnd() + "…";
}

function normalizeSearchPreview(value: string): string {
  return value.trim().replace(/\s+/g, " ").toLocaleLowerCase();
}
