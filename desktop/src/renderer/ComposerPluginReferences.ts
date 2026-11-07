import type { MessageContentPart, PluginReference } from "../shared/protocol";

export type DraftPluginReference = { start: number; end: number; text: string; plugin: PluginReference; token: string };

// Drafts already have an application-owned, durable text field. Encode identities
// there so a renderer reload never depends on a private in-memory mention registry.
export function pluginReferenceToken(text: string, plugin: PluginReference): string {
  return `@[${encodeURIComponent(text)}](wuu-plugin:${encodeURIComponent(JSON.stringify(plugin))})`;
}

export function draftPluginReferences(value: string): DraftPluginReference[] {
  const result: DraftPluginReference[] = [];
  for (const match of value.matchAll(/@\[([^\]]*)\]\(wuu-plugin:([^)]*)\)/g)) {
    try {
      const plugin: PluginReference = JSON.parse(decodeURIComponent(match[2]));
      if (!plugin || typeof plugin !== "object" || (!plugin.plugin_id && !plugin.mcp_server)) continue;
      if (Object.values(plugin).some(field => typeof field !== "string")) continue;
      result.push({ start: match.index, end: match.index + match[0].length, text: decodeURIComponent(match[1]), plugin, token: match[0] });
    } catch { /* A pasted, incomplete token remains ordinary editable text. */ }
  }
  return result;
}

export function pluginReferenceParts(value: string, existing?: MessageContentPart[]): MessageContentPart[] | undefined {
  let found = false;
  const parts = (existing ?? [{ type: "text" as const, text: value }]).flatMap(part => {
    if (part.type !== "text") return [part];
    const refs = draftPluginReferences(part.text);
    if (!refs.length) return [part];
    found = true;
    const result: MessageContentPart[] = [];
    let offset = 0;
    for (const ref of refs) {
      if (ref.start > offset) result.push({ type: "text", text: part.text.slice(offset, ref.start) });
      result.push({ type: "plugin_reference", text: ref.text, plugin: ref.plugin });
      offset = ref.end;
    }
    if (offset < part.text.length) result.push({ type: "text", text: part.text.slice(offset) });
    return result;
  });
  return found || existing ? parts : undefined;
}

export function pluginReferenceDraft(text: string, parts?: MessageContentPart[]): string {
  return parts?.some(part => part.type === "plugin_reference")
    ? parts.map(part => part.type === "plugin_reference" ? pluginReferenceToken(part.text, part.plugin) : part.text).join("")
    : text;
}

export function pluginReferencePlainText(value: string): string {
  return pluginReferenceParts(value)?.map(part => part.text).join("") ?? value;
}
